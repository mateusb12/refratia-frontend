package main

import (
	"context"
	"fmt"
	"strings"
)

func detectPentacamEyeLocal(ctx context.Context, data []byte) (string, error) {
	image, err := renderPDFPageAtDPI(ctx, data, 9, 220)
	if err != nil {
		return "", err
	}

	text, err := runTesseractTextPSM(ctx, image, 11)
	if err != nil {
		return "", err
	}

	text = strings.ToLower(text)

	if !strings.Contains(text, "pentacam") &&
		!strings.Contains(text, "oculus") {
		return "", fmt.Errorf("documento não identificado como Pentacam")
	}

	hasRight := strings.Contains(text, "direito")
	hasLeft := strings.Contains(text, "esquerdo")

	switch {
	case hasRight && !hasLeft:
		return "OD", nil
	case hasLeft && !hasRight:
		return "OS", nil
	default:
		return "", fmt.Errorf("lateralidade Pentacam não resolvida")
	}
}

func tryExtractPentacamLocal(
	ctx context.Context,
	file uploadedFile,
	analysis map[string]any,
) bool {
	eye, err := detectPentacamEyeLocal(ctx, file.Data)
	if err != nil {
		return false
	}

	exam, err := extractPentacamPDFLocal(ctx, file.Data)
	if err != nil {
		return false
	}

	mergeLocalPentacamExam(
		analysis,
		eye,
		file.Metadata.Filename,
		exam,
	)

	return true
}

func mergeLocalPentacamExam(
	analysis map[string]any,
	eye string,
	filename string,
	eyeExam map[string]any,
) {
	exams, _ := analysis["exams"].(map[string]any)
	if exams == nil {
		exams = map[string]any{}
		analysis["exams"] = exams
	}

	exam, _ := exams["pentacam_corneal_tomography"].(map[string]any)
	if exam == nil {
		exam = map[string]any{}
		exams["pentacam_corneal_tomography"] = exam
	}

	eyes, _ := exam["eyes"].(map[string]any)
	if eyes == nil {
		eyes = map[string]any{}
		exam["eyes"] = eyes
	}

	eyes[eye] = eyeExam

	sources, _ := exam["source"].([]any)
	for _, source := range sources {
		if fmt.Sprint(source) == filename {
			return
		}
	}

	exam["source"] = append(sources, filename)
}

var pentacamRequiredNumericPaths = [][]string{
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

func pentacamEyeLocalGaps(
	eyeName string,
	eye map[string]any,
) []string {
	result := []string{}

	for _, path := range pentacamRequiredNumericPaths {
		if hasNumberAtAnyPath(eye, path) {
			continue
		}

		result = append(
			result,
			"pentacam."+eyeName+"."+strings.Join(path, "."),
		)
	}

	topometric, _ := eye["topometric_indices_8mm"].(map[string]any)
	tkc := ""

	if topometric != nil && topometric["tkc"] != nil {
		tkc = strings.TrimSpace(fmt.Sprint(topometric["tkc"]))
	}

	if tkc == "" {
		result = append(
			result,
			"pentacam."+eyeName+".topometric_indices_8mm.tkc",
		)
	}

	return result
}

func pentacamLocalGaps(analysis map[string]any) []string {
	exams, _ := analysis["exams"].(map[string]any)
	exam, _ := exams["pentacam_corneal_tomography"].(map[string]any)

	if exam == nil {
		return nil
	}

	eyes, _ := exam["eyes"].(map[string]any)
	if eyes == nil {
		return nil
	}

	result := []string{}

	for _, eyeName := range []string{"OD", "OS"} {
		eye, _ := eyes[eyeName].(map[string]any)
		if eye == nil {
			continue
		}

		result = append(
			result,
			pentacamEyeLocalGaps(eyeName, eye)...,
		)
	}

	return result
}

func pentacamLocalComplete(analysis map[string]any) bool {
	exams, _ := analysis["exams"].(map[string]any)
	exam, _ := exams["pentacam_corneal_tomography"].(map[string]any)

	if exam == nil {
		return false
	}

	eyes, _ := exam["eyes"].(map[string]any)
	if eyes == nil {
		return false
	}

	foundEye := false

	for _, eyeName := range []string{"OD", "OS"} {
		eye, _ := eyes[eyeName].(map[string]any)
		if eye == nil {
			continue
		}

		foundEye = true

		if len(pentacamEyeLocalGaps(eyeName, eye)) > 0 {
			return false
		}
	}

	return foundEye
}
