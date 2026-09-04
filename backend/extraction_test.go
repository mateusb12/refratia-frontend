package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreparedPromptLabelIncludesPDFPage(t *testing.T) {
	label := preparedPromptLabel(preparedFile{File: uploadedFile{Metadata: intakeFile{Filename: "exame.pdf"}}, Page: 8})
	if label != "Arquivo: exame.pdf — página 8" {
		t.Fatalf("unexpected page label: %q", label)
	}
}

func TestPrepareExtractionFilesRejectsInvalidPDF(t *testing.T) {
	_, err := prepareExtractionFiles(context.Background(), []uploadedFile{{Metadata: intakeFile{Filename: "exame.pdf", ContentType: "application/pdf"}, Data: []byte("not a pdf")}})
	if err == nil {
		t.Fatal("expected invalid PDF to fail during preprocessing")
	}
}

func TestStoredAnalysisRejectsDifferentFile(t *testing.T) {
	files := []intakeFile{{Filename: "exame.pdf", SHA256: "hash-correto", Key: "drafts/intake/exame.pdf"}}
	valid := map[string]any{"source_files": []any{map[string]any{"path": "exame.pdf", "sha256": "hash-correto"}}}
	if err := validateStoredAnalysis(valid, files); err != nil {
		t.Fatalf("expected matching analysis: %v", err)
	}

	tampered := map[string]any{"source_files": []any{map[string]any{"path": "exame.pdf", "sha256": "outro-hash"}}}
	if err := validateStoredAnalysis(tampered, files); err == nil {
		t.Fatal("expected mismatched file to be rejected")
	}
}

func TestValidIntakeID(t *testing.T) {
	if !validIntakeID("intake-20260815T155045Z-da4bf173") || validIntakeID("../../case") {
		t.Fatal("expected only generated intake IDs to be accepted")
	}
}

func TestDeleteCaseRequiresToken(t *testing.T) {
	t.Setenv("CASE_DELETE_TOKEN", "secret")
	request := httptest.NewRequest(http.MethodDelete, "/api/cases/case-20260815T155045Z-da4bf173", nil)
	response := httptest.NewRecorder()
	caseHandler(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized deletion, got %d", response.Code)
	}
}

func TestValidatePatientJSONUsesOfficialExamKeys(t *testing.T) {
	valid := `{"patient":{},"exams":{"pentacam_corneal_tomography":{"source":[]}}}`
	if err := validatePatientJSON(valid); err != nil {
		t.Fatalf("expected official contract: %v", err)
	}

	unknown := `{"patient":{},"exams":{"pentacam":{"source":[]}}}`
	if err := validatePatientJSON(unknown); err == nil {
		t.Fatal("expected unknown exam key to be rejected")
	}
}

func TestDecodeAnalysisAllowsSingleExamWithMalformedAbsentExam(t *testing.T) {
	analysis, err := decodeAnalysis(`{"patient":{"full_name":"Paciente"},"exams":{"fundus_retinography":{"source":["olho.jpeg"]},"iol_calculation":null}}`)
	if err != nil {
		t.Fatalf("expected isolated exam to be accepted: %v", err)
	}
	exams := analysis["exams"].(map[string]any)
	if _, exists := exams["iol_calculation"]; exists {
		t.Fatal("expected malformed absent exam to be removed")
	}
}

func TestDecodeAnalysisMovesExtractionNotesOutOfExams(t *testing.T) {
	analysis, err := decodeAnalysis(`{"patient":{"full_name":"Paciente"},"exams":{"pentacam_corneal_tomography":{"source":[]},"extraction_notes":{"method":"vision"}}}`)
	if err != nil {
		t.Fatalf("expected misplaced metadata to be normalized: %v", err)
	}
	if _, ok := analysis["extraction_notes"]; !ok {
		t.Fatal("expected extraction_notes at the root")
	}
	if _, ok := analysis["exams"].(map[string]any)["extraction_notes"]; ok {
		t.Fatal("expected extraction_notes to be removed from exams")
	}
}

func TestPentacamRepairFillsOnlyMissingMetrics(t *testing.T) {
	analysis := map[string]any{"exams": map[string]any{
		"pentacam_corneal_tomography": map[string]any{
			"source": []any{"od.pdf", "os.pdf"},
			"eyes": map[string]any{
				"OD": map[string]any{"general": map[string]any{"pachymetry_thinnest_um": 529.0, "k_max_anterior_diopters": 44.2}},
				"OS": map[string]any{"general": map[string]any{"pachymetry_thinnest_um": 526.0, "k_max_anterior_diopters": 45.5}},
			},
		},
	}}
	files := []uploadedFile{
		{Metadata: intakeFile{Filename: "od.pdf"}},
		{Metadata: intakeFile{Filename: "os.pdf"}},
		{Metadata: intakeFile{Filename: "biometria.pdf"}},
	}

	selected := pentacamFilesNeedingRepair(analysis, files)
	if len(selected) != 2 || selected[0].Metadata.Filename != "od.pdf" || selected[1].Metadata.Filename != "os.pdf" {
		t.Fatalf("expected only Pentacam sources, got %#v", selected)
	}

	mergePentacamRepair(analysis, map[string]any{"eyes": map[string]any{
		"OD": map[string]any{
			"general":                map[string]any{"k_max_anterior_diopters": 99.0},
			"belin_ambrosio":         map[string]any{"d": 0.65, "art_max": 416.0},
			"topometric_indices_8mm": map[string]any{"isv": 10.0, "iva": 0.09, "iha": 4.2, "ki": 1.01, "cki": 1.0},
			"corneal_rings":          map[string]any{"zernike": map[string]any{"5mm": map[string]any{"z31_coma": 0.097}}},
			"cataract_preop":         map[string]any{"total_corneal_z40_6mm_um": 0.287},
		},
		"OS": map[string]any{
			"belin_ambrosio":         map[string]any{"d": 2.27, "art_max": 366.0},
			"topometric_indices_8mm": map[string]any{"isv": 20.0, "iva": 0.18, "iha": 12.8, "ki": 1.01, "cki": 0.98},
			"corneal_rings":          map[string]any{"zernike": map[string]any{"5mm": map[string]any{"z31_coma": 0.299}}},
			"cataract_preop":         map[string]any{"total_corneal_z40_6mm_um": 0.602},
		},
	}})

	exam := pentacamExam(analysis)
	if pentacamNeedsRepair(exam) {
		t.Fatal("expected targeted repair to complete Pentacam metrics")
	}
	od := exam["eyes"].(map[string]any)["OD"].(map[string]any)
	if od["general"].(map[string]any)["k_max_anterior_diopters"] != 44.2 {
		t.Fatal("repair must not overwrite an existing extracted value")
	}
}

func TestIOLRepairFillsMissingMetricsWithoutOverwriting(t *testing.T) {
	analysis := map[string]any{"exams": map[string]any{
		"iol_calculation": map[string]any{
			"source": []any{"bio.pdf"},
			"eyes": map[string]any{
				"OD": map[string]any{"axial_length_mm": 24.6},
				"OS": map[string]any{},
			},
		},
	}}
	if !iolNeedsRepair(analysis["exams"].(map[string]any)["iol_calculation"].(map[string]any)) {
		t.Fatal("expected incomplete IOL payload to need repair")
	}
	mergeIOLRepair(analysis, map[string]any{"eyes": map[string]any{
		"OD": map[string]any{"axial_length_mm": 99.0, "keratometry": map[string]any{"k1_d": 43.68}},
		"OS": map[string]any{"axial_length_mm": 24.74},
	}})
	od := analysis["exams"].(map[string]any)["iol_calculation"].(map[string]any)["eyes"].(map[string]any)["OD"].(map[string]any)
	if od["axial_length_mm"] != 24.6 || od["keratometry"].(map[string]any)["k1_d"] != 43.68 {
		t.Fatal("IOL repair should preserve existing values and fill missing ones")
	}
}

func TestNormalizeExtractionMetadataMovesIdentityVerification(t *testing.T) {
	analysis := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"source": []any{"pentacam.pdf"},
			},
			"verificacao_identidade": []any{
				map[string]any{
					"nome":       "PACIENTE TESTE SILVA",
					"nascimento": "03/04/1980",
				},
			},
		},
	}

	normalizeExtractionMetadata(analysis)

	if _, exists := analysis["verificacao_identidade"]; !exists {
		t.Fatal("verificacao_identidade deveria ter sido movida para a raiz")
	}

	exams, ok := analysis["exams"].(map[string]any)
	if !ok {
		t.Fatal("exams deveria continuar sendo um objeto")
	}

	if _, exists := exams["verificacao_identidade"]; exists {
		t.Fatal("verificacao_identidade não pode permanecer dentro de exams")
	}

	if _, exists := exams["pentacam_corneal_tomography"]; !exists {
		t.Fatal("o exame real não pode ser removido pela normalização")
	}
}
