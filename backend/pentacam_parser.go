package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func parsePentacamTSVPages(pages map[int]string) (map[string]any, error) {
	texts := map[int]string{}

	for page, tsv := range pages {
		words, _, err := parseTSVWords(tsv)
		if err != nil {
			return nil, fmt.Errorf("página %d: %w", page, err)
		}

		rows := groupOCRRows(words, 10)
		parts := make([]string, 0, len(rows))
		for _, row := range rows {
			parts = append(parts, rowText(row))
		}
		texts[page] = strings.Join(parts, "\n")
	}

	return parsePentacamPageTexts(texts)
}

func parsePentacamPageTexts(pages map[int]string) (map[string]any, error) {
	for _, page := range []int{4, 6, 7, 8, 9} {
		if strings.TrimSpace(pages[page]) == "" {
			return nil, fmt.Errorf("Pentacam: página %d ausente", page)
		}
	}

	p4 := normalizePentacamText(pages[4])
	p6 := normalizePentacamText(pages[6])
	p7 := normalizePentacamText(pages[7])
	p8 := normalizePentacamText(pages[8])
	p9 := normalizePentacamText(pages[9])

	values := map[string]float64{}
	missing := []string{}

	requireDecimal := func(key, text, label string) {
		if value, ok := pentacamNumberAfter(text, label, true); ok {
			values[key] = value
		} else {
			missing = append(missing, key)
		}
	}

	requireNumber := func(key, text, label string) {
		if value, ok := pentacamNumberAfter(text, label, false); ok {
			values[key] = value
		} else {
			missing = append(missing, key)
		}
	}

	// Página 4: primeiro bloco = córnea anterior.
	requireDecimal("k1_d", p4, `\bk[1i]\b`)
	requireDecimal("k2_d", p4, `\bk2\b`)
	requireDecimal("km_d", p4, `\bkm\b`)
	requireDecimal("astigmatism_d", p4, `\bastig\w*\b`)

	// Página 6: Topométrico / Estadiamento KC.
	requireNumber("thinnest_um", p6, `\bthinnest\W+pachy\b`)
	requireNumber("isv", p6, `\bisv\b`)
	requireDecimal("iva", p6, `\biva\b`)
	requireDecimal("iha", p6, `\biha\b`)
	requireDecimal("ki", p6, `\bki\b`)
	requireDecimal("cki", p6, `\bcki\b`)

	tkc, tkcOK := pentacamTKC(p6)
	if !tkcOK {
		missing = append(missing, "tkc")
	}

	// Página 7: Cataract Pre-OP.
	requireDecimal("bad_d", p7, `\bbad\W*d\b`)
	requireDecimal(
		"acd_internal_mm",
		p7,
		`\bprof\W*cam\W*ant\W*int\b`,
	)
	requireDecimal(
		"z40_6mm_um",
		p7,
		`\btotal\W+corneal\W+(?:z40|240)\W*(?:6\W*mm|bmm|emm)`,
	)

	// Página 8: Belin / Ambrósio.
	requireNumber("art_max", p8, `\bartmax\b`)

	// Página 9: Anéis Corneanos. O primeiro Z31 é a zona 5 mm.
	requireDecimal("z31_5mm_um", p9, `\bz31\W*\(?coma\w*\)?`)

	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"Pentacam: campos ausentes: %s",
			strings.Join(missing, ", "),
		)
	}

	return map[string]any{
		"anterior_cornea": map[string]any{
			"k1_d":          values["k1_d"],
			"k2_d":          values["k2_d"],
			"km_d":          values["km_d"],
			"astigmatism_d": values["astigmatism_d"],
		},
		"pachymetry": map[string]any{
			"thinnest_um": values["thinnest_um"],
		},
		"belin_ambrosio": map[string]any{
			"d":       values["bad_d"],
			"art_max": values["art_max"],
		},
		"topometric_indices_8mm": map[string]any{
			"isv": values["isv"],
			"iva": values["iva"],
			"iha": values["iha"],
			"ki":  values["ki"],
			"cki": values["cki"],
			"tkc": tkc,
		},
		"corneal_rings": map[string]any{
			"zernike": map[string]any{
				"5mm": map[string]any{
					"z31_coma": values["z31_5mm_um"],
				},
			},
		},
		"anterior_segment": map[string]any{
			"internal_anterior_chamber_depth_mm": values["acd_internal_mm"],
		},
		"cataract_preop": map[string]any{
			"total_corneal_z40_6mm_um": values["z40_6mm_um"],
		},
	}, nil
}

func normalizePentacamText(value string) string {
	value = strings.ToLower(value)

	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a",
		"é", "e", "ê", "e",
		"í", "i",
		"ó", "o", "ô", "o", "õ", "o",
		"ú", "u",
		"ç", "c",
		",", ".",
	)
	return replacer.Replace(value)
}

func pentacamNumberAfter(
	text string,
	labelPattern string,
	requireDecimal bool,
) (float64, bool) {
	numberPattern := `[+-]?\d+(?:\.\d+)?`
	if requireDecimal {
		numberPattern = `[+-]?\d+\.\d+`
	}

	re := regexp.MustCompile(
		`(?i)` + labelPattern +
			`[^0-9+\-]{0,36}(` + numberPattern + `)`,
	)

	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return 0, false
	}

	value, err := strconv.ParseFloat(match[1], 64)
	return value, err == nil
}

func pentacamTKC(text string) (string, bool) {
	re := regexp.MustCompile(`(?i)\btkc\b\W*([^\s]+)`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}

	token := strings.TrimSpace(match[1])

	// Campo localizado e impresso como traço = sem classificação.
	if strings.Contains(token, "-") ||
		strings.Contains(token, "—") ||
		strings.Contains(token, "–") {
		return "—", true
	}

	// Não transformamos OCR ambíguo como "I", "E" etc. em classificação.
	return "", false
}

func pentacamContractComplete(eye map[string]any) error {
	required := [][]string{
		{"anterior_cornea", "k1_d"},
		{"anterior_cornea", "k2_d"},
		{"anterior_cornea", "km_d"},
		{"anterior_cornea", "astigmatism_d"},
		{"pachymetry", "thinnest_um"},
		{"belin_ambrosio", "d"},
		{"belin_ambrosio", "art_max"},
		{"topometric_indices_8mm", "isv"},
		{"topometric_indices_8mm", "iva"},
		{"topometric_indices_8mm", "iha"},
		{"topometric_indices_8mm", "ki"},
		{"topometric_indices_8mm", "cki"},
		{"corneal_rings", "zernike", "5mm", "z31_coma"},
		{"anterior_segment", "internal_anterior_chamber_depth_mm"},
		{"cataract_preop", "total_corneal_z40_6mm_um"},
	}

	for _, path := range required {
		if !hasNumberAtAnyPath(eye, path) {
			return fmt.Errorf("campo numérico ausente: %s", strings.Join(path, "."))
		}
	}

	topometric, _ := eye["topometric_indices_8mm"].(map[string]any)
	if topometric == nil || strings.TrimSpace(fmt.Sprint(topometric["tkc"])) == "" {
		return errors.New("campo ausente: topometric_indices_8mm.tkc")
	}

	return nil
}
