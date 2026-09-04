package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ocrWord struct {
	Text          string
	Left, Top     int
	Width, Height int
}

type ocrRow struct {
	Y     int
	Words []ocrWord
}

var decimalPattern = regexp.MustCompile(`[+-]?\d+[,.]\d+`)

func parseEyeSuiteTSV(tsv string) (map[string]any, error) {
	words, pageWidth, err := parseTSVWords(tsv)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, errors.New("EyeSuite OCR vazio")
	}

	if pageWidth <= 0 {
		for _, word := range words {
			if right := word.Left + word.Width; right > pageWidth {
				pageWidth = right
			}
		}
	}
	if pageWidth <= 0 {
		return nil, errors.New("largura da página OCR inválida")
	}

	mid := pageWidth / 2
	eyes := map[string]any{}

	for _, eye := range []struct {
		Name       string
		MinX, MaxX int
	}{
		{Name: "OD", MinX: 0, MaxX: mid},
		{Name: "OS", MinX: mid, MaxX: pageWidth + 1},
	} {
		selected := make([]ocrWord, 0)
		for _, word := range words {
			center := word.Left + word.Width/2
			if center >= eye.MinX && center < eye.MaxX {
				selected = append(selected, word)
			}
		}

		payload, err := parseEyeSuiteEye(selected)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", eye.Name, err)
		}
		eyes[eye.Name] = payload
	}

	return map[string]any{"eyes": eyes}, nil
}

func parseTSVWords(tsv string) ([]ocrWord, int, error) {
	lines := strings.Split(tsv, "\n")
	words := make([]ocrWord, 0)
	pageWidth := 0

	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 12 {
			continue
		}

		level, _ := strconv.Atoi(fields[0])
		left, _ := strconv.Atoi(fields[6])
		top, _ := strconv.Atoi(fields[7])
		width, _ := strconv.Atoi(fields[8])
		height, _ := strconv.Atoi(fields[9])

		if level == 1 && width > pageWidth {
			pageWidth = width
		}
		if level != 5 || strings.TrimSpace(fields[11]) == "" {
			continue
		}

		words = append(words, ocrWord{
			Text:   strings.TrimSpace(fields[11]),
			Left:   left,
			Top:    top,
			Width:  width,
			Height: height,
		})
	}

	return words, pageWidth, nil
}

func parseEyeSuiteEye(words []ocrWord) (map[string]any, error) {
	rows := groupOCRRows(words, 10)

	values := map[string]float64{}
	var astigAxis float64
	axisFound := false

	for _, row := range rows {
		text := rowText(row)

		if value, ok := decimalAfter(text, `\bAL\b`); ok {
			values["axial_length_mm"] = value
		}
		if value, ok := decimalAfter(text, `\bK1\b`); ok {
			values["k1_d"] = value
		}
		if value, ok := decimalAfter(text, `\bK2\b`); ok {
			values["k2_d"] = value
		}
		if value, ok := decimalAfter(text, `(?:^|\s)K(?:\s|\[)`); ok {
			values["mean_k_d"] = value
		}
		if value, ok := decimalAfter(text, `-?\s*AST\b`); ok {
			values["astigmatism_d"] = value
			if axis, found := axisAfter(text); found {
				astigAxis = axis
				axisFound = true
			}
		}
		if value, ok := decimalAfter(text, `\bACD\b`); ok {
			values["anterior_chamber_depth_mm"] = value
		}
		if value, ok := decimalAfter(text, `\bLT\b`); ok {
			values["lens_thickness_mm"] = value
		}
		if value, ok := decimalAfter(text, `\bWTW\b`); ok {
			values["white_to_white_mm"] = value
		}
		if value, ok := decimalAfter(text, `Target\s+Refraction`); ok {
			values["target_refraction_d"] = value
		}
	}

	required := []string{
		"axial_length_mm",
		"k1_d",
		"k2_d",
		"mean_k_d",
		"astigmatism_d",
		"anterior_chamber_depth_mm",
		"lens_thickness_mm",
		"white_to_white_mm",
		"target_refraction_d",
	}

	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := values[key]; !ok {
			missing = append(missing, key)
		}
	}
	if !axisFound {
		missing = append(missing, "astigmatism_axis_deg")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("campos ausentes: %s", strings.Join(missing, ", "))
	}

	return map[string]any{
		"axial_length_mm": values["axial_length_mm"],
		"keratometry": map[string]any{
			"k1_d":                 values["k1_d"],
			"k2_d":                 values["k2_d"],
			"mean_k_d":             values["mean_k_d"],
			"astigmatism_d":        values["astigmatism_d"],
			"astigmatism_axis_deg": astigAxis,
		},
		"anterior_chamber_depth_mm": values["anterior_chamber_depth_mm"],
		"lens_thickness_mm":         values["lens_thickness_mm"],
		"white_to_white_mm":         values["white_to_white_mm"],
		"target_refraction_d":       values["target_refraction_d"],
	}, nil
}

func groupOCRRows(words []ocrWord, tolerance int) []ocrRow {
	sort.Slice(words, func(i, j int) bool {
		if words[i].Top == words[j].Top {
			return words[i].Left < words[j].Left
		}
		return words[i].Top < words[j].Top
	})

	rows := make([]ocrRow, 0)
	for _, word := range words {
		if len(rows) == 0 || int(math.Abs(float64(rows[len(rows)-1].Y-word.Top))) > tolerance {
			rows = append(rows, ocrRow{Y: word.Top, Words: []ocrWord{word}})
			continue
		}
		row := &rows[len(rows)-1]
		row.Words = append(row.Words, word)
	}

	for index := range rows {
		sort.Slice(rows[index].Words, func(i, j int) bool {
			return rows[index].Words[i].Left < rows[index].Words[j].Left
		})
	}

	return rows
}

func rowText(row ocrRow) string {
	parts := make([]string, len(row.Words))
	for index, word := range row.Words {
		parts[index] = word.Text
	}
	return strings.Join(parts, " ")
}

func decimalAfter(text, labelPattern string) (float64, bool) {
	label := regexp.MustCompile(`(?i)` + labelPattern)
	location := label.FindStringIndex(text)
	if location == nil {
		return 0, false
	}

	match := decimalPattern.FindString(text[location[1]:])
	if match == "" {
		return 0, false
	}

	value, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", "."), 64)
	return value, err == nil
}

func axisAfter(text string) (float64, bool) {
	match := regexp.MustCompile(`@\s*(\d{1,3})`).FindStringSubmatch(text)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	return value, err == nil
}

type eyeSuiteIdentity struct {
	FullName     string
	BirthDateRaw string
	TimestampRaw string
}

var (
	eyeSuiteBirthDatePattern = regexp.MustCompile(`^\d{1,2}/\d{1,2}/\d{4}$`)
	eyeSuiteExamDatePattern  = regexp.MustCompile(`^\d{1,2}/\d{1,2}/(?:\d{2}|\d{4})$`)
	eyeSuiteTimePattern      = regexp.MustCompile(`^\d{1,2}:\d{2}(?::\d{2})?$`)
	eyeSuiteIDPattern        = regexp.MustCompile(`(?i)\bID\b.*\bCID\b`)
	eyeSuiteLetterPattern    = regexp.MustCompile(`\p{L}`)
)

func parseEyeSuiteIdentityTSV(tsv string) (eyeSuiteIdentity, error) {
	words, _, err := parseTSVWords(tsv)
	if err != nil {
		return eyeSuiteIdentity{}, err
	}
	return parseEyeSuiteIdentityWords(words)
}

func parseEyeSuiteIdentityWords(words []ocrWord) (eyeSuiteIdentity, error) {
	rows := groupOCRRows(words, 10)

	anchor := -1
	for i, row := range rows {
		if eyeSuiteIDPattern.MatchString(rowText(row)) {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		return eyeSuiteIdentity{}, errors.New("EyeSuite: âncora ID (CID) ausente")
	}

	var fullName, birthDate string

	for i := anchor - 1; i >= 0 && i >= anchor-4; i-- {
		dateIndex := -1

		for j, word := range rows[i].Words {
			clean := strings.Trim(word.Text, " ,;:")
			if eyeSuiteBirthDatePattern.MatchString(clean) {
				dateIndex = j
				birthDate = clean
				break
			}
		}
		if dateIndex < 0 {
			continue
		}

		parts := make([]string, 0, dateIndex)
		for _, word := range rows[i].Words[:dateIndex] {
			clean := strings.Trim(word.Text, " ,;:")
			if clean != "" && eyeSuiteLetterPattern.MatchString(clean) {
				parts = append(parts, clean)
			}
		}

		if len(parts) >= 2 {
			fullName = strings.Join(parts, " ")
			break
		}
	}

	if fullName == "" || birthDate == "" {
		return eyeSuiteIdentity{}, errors.New("EyeSuite: nome/nascimento não localizados")
	}

	var examDate, examTime string

	for i := anchor + 1; i < len(rows) && i <= anchor+5; i++ {
		for _, word := range rows[i].Words {
			clean := strings.Trim(word.Text, " ,;:")

			if examDate == "" && eyeSuiteExamDatePattern.MatchString(clean) {
				examDate = clean
			}
			if examTime == "" && eyeSuiteTimePattern.MatchString(clean) {
				examTime = clean
			}
		}

		if examDate != "" && examTime != "" {
			break
		}
	}

	if examDate == "" || examTime == "" {
		return eyeSuiteIdentity{}, errors.New("EyeSuite: timestamp não localizado")
	}

	return eyeSuiteIdentity{
		FullName:     fullName,
		BirthDateRaw: birthDate,
		TimestampRaw: examDate + " " + examTime,
	}, nil
}
