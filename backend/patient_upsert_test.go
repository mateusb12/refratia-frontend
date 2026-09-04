package main

import "testing"

func TestPatientIdentityNormalizesNameAndBirthDate(t *testing.T) {
	analysis := map[string]any{
		"patient": map[string]any{
			"full_name":  "  Paciente   Teste-Silva ",
			"birth_date": "1980-03-04",
		},
	}

	identity, ok := patientIdentity(analysis)
	if !ok {
		t.Fatal("expected patient to be identifiable")
	}

	if identity != "paciente teste silva|1980-03-04" {
		t.Fatalf("unexpected identity: %q", identity)
	}
}

func TestPatientIdentityRequiresBirthDate(t *testing.T) {
	analysis := map[string]any{
		"patient": map[string]any{
			"full_name": "Paciente Teste Silva",
		},
	}

	if _, ok := patientIdentity(analysis); ok {
		t.Fatal("patient without birth date must not be automatically merged")
	}
}

func TestMergePatientCaseAddsPreviouslyMissingExam(t *testing.T) {
	existing := map[string]any{
		"patient": map[string]any{
			"full_name":  "Paciente Teste Silva",
			"birth_date": "1980-03-04",
		},
		"source_files": []any{},
		"exams": map[string]any{
			"iol_calculation": map[string]any{
				"source": []any{"bio.pdf"},
				"eyes": map[string]any{
					"OD": map[string]any{"axial_length_mm": 24.1},
				},
			},
		},
	}

	incoming := map[string]any{
		"patient": map[string]any{
			"full_name":  "Paciente Teste Silva",
			"birth_date": "1980-03-04",
		},
		"source_files": []any{},
		"exams": map[string]any{
			"specular_microscopy": map[string]any{
				"source": []any{"micro.jpeg"},
				"eyes": map[string]any{
					"AO": map[string]any{"cell_density_cells_per_mm2": 2450.0},
				},
			},
		},
	}

	merged := mergePatientCase(existing, incoming)
	exams := merged["exams"].(map[string]any)

	if _, ok := exams["iol_calculation"]; !ok {
		t.Fatal("existing exam must be preserved")
	}

	if _, ok := exams["specular_microscopy"]; !ok {
		t.Fatal("new exam must be added")
	}
}

func TestMergePatientCaseReplacesOnlyIncomingEye(t *testing.T) {
	existing := map[string]any{
		"source_files": []any{},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"source": []any{"old-od.pdf", "old-os.pdf"},
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{"kmax_d": 44.0},
					},
					"OS": map[string]any{
						"anterior_cornea": map[string]any{"kmax_d": 45.0},
					},
				},
			},
		},
	}

	incoming := map[string]any{
		"source_files": []any{},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"source": []any{"new-od.pdf"},
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{"kmax_d": 47.0},
					},
				},
			},
		},
	}

	merged := mergePatientCase(existing, incoming)

	exams := merged["exams"].(map[string]any)
	pentacam := exams["pentacam_corneal_tomography"].(map[string]any)
	eyes := pentacam["eyes"].(map[string]any)

	od := eyes["OD"].(map[string]any)
	odCornea := od["anterior_cornea"].(map[string]any)

	os := eyes["OS"].(map[string]any)
	osCornea := os["anterior_cornea"].(map[string]any)

	if odCornea["kmax_d"] != 47.0 {
		t.Fatalf("OD must be replaced by incoming exam, got %#v", odCornea["kmax_d"])
	}

	if osCornea["kmax_d"] != 45.0 {
		t.Fatalf("OS must be preserved, got %#v", osCornea["kmax_d"])
	}
}

func TestMergeSourceFilesDeduplicatesBySHA256(t *testing.T) {
	existing := []any{
		map[string]any{
			"path":   "cases/case-x/old.pdf",
			"sha256": "same-hash",
		},
	}

	incoming := []any{
		map[string]any{
			"path":   "cases/case-x/new-copy.pdf",
			"sha256": "same-hash",
		},
		map[string]any{
			"path":   "cases/case-x/another.pdf",
			"sha256": "different-hash",
		},
	}

	merged := mergeSourceFiles(existing, incoming)

	if len(merged) != 2 {
		t.Fatalf("expected 2 unique sources, got %d", len(merged))
	}
}

func TestMergeSourceFilesReplacesSameExamAndEye(t *testing.T) {
	existing := []any{
		map[string]any{
			"path":   "cases/case-x/old-od.pdf",
			"sha256": "old-od",
			"exam":   "pentacam_corneal_tomography",
			"eye":    "OD",
		},
		map[string]any{
			"path":   "cases/case-x/old-os.pdf",
			"sha256": "old-os",
			"exam":   "pentacam_corneal_tomography",
			"eye":    "OS",
		},
	}

	incoming := []any{
		map[string]any{
			"path":   "cases/case-x/new-od.pdf",
			"sha256": "new-od",
			"exam":   "pentacam_corneal_tomography",
			"eye":    "OD",
		},
	}

	merged := mergeSourceFiles(existing, incoming)

	if len(merged) != 2 {
		t.Fatalf("expected current OD + preserved OS, got %d sources", len(merged))
	}

	paths := map[string]bool{}
	for _, raw := range merged {
		source := raw.(map[string]any)
		paths[source["path"].(string)] = true
	}

	if paths["cases/case-x/old-od.pdf"] {
		t.Fatal("superseded OD source must not remain in current source_files")
	}

	if !paths["cases/case-x/new-od.pdf"] {
		t.Fatal("new OD source must become current")
	}

	if !paths["cases/case-x/old-os.pdf"] {
		t.Fatal("OS source must be preserved")
	}
}

func TestMergeSourceFilesKeepsExistingPathForExactReupload(t *testing.T) {
	existing := []any{
		map[string]any{
			"path":   "cases/case-x/original.pdf",
			"sha256": "same",
			"exam":   "pentacam_corneal_tomography",
			"eye":    "OD",
		},
	}

	incoming := []any{
		map[string]any{
			"path":   "cases/case-x/reuploaded-copy.pdf",
			"sha256": "same",
			"exam":   "pentacam_corneal_tomography",
			"eye":    "OD",
		},
	}

	merged := mergeSourceFiles(existing, incoming)

	if len(merged) != 1 {
		t.Fatalf("expected one source, got %d", len(merged))
	}

	source := merged[0].(map[string]any)
	if source["path"] != "cases/case-x/original.pdf" {
		t.Fatalf("expected original persisted source, got %#v", source["path"])
	}
}

func TestMergePatientCaseRebuildsPentacamSourceInEyeOrder(t *testing.T) {
	existing := map[string]any{
		"source_files": []any{
			map[string]any{
				"path":   "cases/case-x/old-od.pdf",
				"sha256": "old-od",
				"exam":   "pentacam_corneal_tomography",
				"eye":    "OD",
			},
			map[string]any{
				"path":   "cases/case-x/old-os.pdf",
				"sha256": "old-os",
				"exam":   "pentacam_corneal_tomography",
				"eye":    "OS",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"source": []any{"old-od.pdf", "old-os.pdf"},
				"eyes": map[string]any{
					"OD": map[string]any{"value": "old OD"},
					"OS": map[string]any{"value": "old OS"},
				},
			},
		},
	}

	incoming := map[string]any{
		"source_files": []any{
			map[string]any{
				"path":   "cases/case-x/new-od.pdf",
				"sha256": "new-od",
				"exam":   "pentacam_corneal_tomography",
				"eye":    "OD",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"source": []any{"new-od.pdf"},
				"eyes": map[string]any{
					"OD": map[string]any{"value": "new OD"},
				},
			},
		},
	}

	merged := mergePatientCase(existing, incoming)

	exams := merged["exams"].(map[string]any)
	pentacam := exams["pentacam_corneal_tomography"].(map[string]any)
	sources := pentacam["source"].([]any)

	if len(sources) != 2 {
		t.Fatalf("expected OD + OS sources, got %#v", sources)
	}

	if sources[0] != "cases/case-x/new-od.pdf" {
		t.Fatalf("source[0] must be current OD, got %#v", sources[0])
	}

	if sources[1] != "cases/case-x/old-os.pdf" {
		t.Fatalf("source[1] must preserve OS, got %#v", sources[1])
	}

	eyes := pentacam["eyes"].(map[string]any)

	if eyes["OD"].(map[string]any)["value"] != "new OD" {
		t.Fatal("OD payload must be updated")
	}

	if eyes["OS"].(map[string]any)["value"] != "old OS" {
		t.Fatal("OS payload must remain untouched")
	}
}

func TestPatientIdentityUsesDocumentDateOrderInsteadOfModelNormalizedDate(t *testing.T) {
	analysis := map[string]any{
		"patient": map[string]any{
			"full_name": "PACIENTE TESTE SILVA",
			"birth_date": map[string]any{
				"normalized": "1980-04-03",
			},
		},
		"verificacao_identidade": []any{
			map[string]any{
				"nascimento_lido": "03/04/1980",
				"timestamp_lido":  "08/31/2026 10:15:00",
			},
		},
	}

	identity, ok := patientIdentity(analysis)
	if !ok {
		t.Fatal("expected patient identity to be deterministically resolved")
	}

	if identity != "paciente teste silva|1980-03-04" {
		t.Fatalf("unexpected identity: %q", identity)
	}
}

func TestPatientIdentityMatchesBothObservedPentacamShapes(t *testing.T) {
	od := map[string]any{
		"patient": map[string]any{
			"full_name": "PACIENTE TESTE SILVA",
			"birth_date": map[string]any{
				"normalized": "1980-04-03",
			},
		},
		"verificacao_identidade": []any{
			map[string]any{
				"nascimento_lido": "03/04/1980",
				"timestamp_lido":  "08/31/2026 10:15:00",
			},
		},
	}

	os := map[string]any{
		"patient": map[string]any{
			"full_name":             "PACIENTE TESTE SILVA",
			"birth_date_normalized": "1980-04-03",
		},
		"verificacao_identidade": []any{
			map[string]any{
				"nascimento_lido": "03/04/1980",
				"timestamp_lido":  "08/31/2026 10:16:00",
			},
		},
	}

	odIdentity, odOK := patientIdentity(od)
	osIdentity, osOK := patientIdentity(os)

	if !odOK || !osOK {
		t.Fatal("both Pentacam shapes should produce a patient identity")
	}

	if odIdentity != osIdentity {
		t.Fatalf(
			"OD and OS should resolve to same identity: %q != %q",
			odIdentity,
			osIdentity,
		)
	}

	if odIdentity != "paciente teste silva|1980-03-04" {
		t.Fatalf("unexpected canonical identity: %q", odIdentity)
	}
}

func TestPatientIdentityRejectsAmbiguousSlashDateWithoutContext(t *testing.T) {
	analysis := map[string]any{
		"patient": map[string]any{
			"full_name":             "PACIENTE TESTE SILVA",
			"birth_date_normalized": "1980-04-03",
		},
		"verificacao_identidade": []any{
			map[string]any{
				"nascimento_lido": "03/04/1980",
			},
		},
	}

	if _, ok := patientIdentity(analysis); ok {
		t.Fatal("ambiguous raw birth date must not fall back to model normalization")
	}
}

func TestSourceExamArrayMatchesRealPentacamShape(t *testing.T) {
	source := map[string]any{
		"exam": []any{"pentacam_corneal_tomography"},
		"eye":  "OD",
	}

	if sourceExamName(source) != "pentacam_corneal_tomography" {
		t.Fatal("single-item exam array should resolve to its exam name")
	}

	if !sourceHasExam(source, "pentacam_corneal_tomography") {
		t.Fatal("source should match Pentacam")
	}

	if sourceGroupKey(source) != "pentacam_corneal_tomography|OD" {
		t.Fatalf("unexpected source group: %q", sourceGroupKey(source))
	}
}

func TestMergeSourceFilesReplacesEyeWhenExamIsArray(t *testing.T) {
	existing := []any{
		map[string]any{
			"path":   "cases/x/old-od.pdf",
			"sha256": "old-od",
			"exam":   []any{"pentacam_corneal_tomography"},
			"eye":    "OD",
		},
		map[string]any{
			"path":   "cases/x/old-os.pdf",
			"sha256": "old-os",
			"exam":   []any{"pentacam_corneal_tomography"},
			"eye":    "OS",
		},
	}

	incoming := []any{
		map[string]any{
			"path":   "cases/x/new-od.pdf",
			"sha256": "new-od",
			"exam":   []any{"pentacam_corneal_tomography"},
			"eye":    "OD",
		},
	}

	merged := mergeSourceFiles(existing, incoming)

	paths := map[string]bool{}
	for _, raw := range merged {
		source := raw.(map[string]any)
		paths[source["path"].(string)] = true
	}

	if paths["cases/x/old-od.pdf"] {
		t.Fatal("old OD source should have been replaced")
	}

	if !paths["cases/x/new-od.pdf"] {
		t.Fatal("new OD source should be current")
	}

	if !paths["cases/x/old-os.pdf"] {
		t.Fatal("OS source should be preserved")
	}
}

func TestNormalizePatientIdentityFieldsRemovesWrongNormalizedAlias(t *testing.T) {
	analysis := map[string]any{
		"patient": map[string]any{
			"full_name":             "PACIENTE TESTE SILVA",
			"birth_date":            "1980-03-04",
			"birth_date_normalized": "1980-04-03",
		},
	}

	normalizePatientIdentityFields(analysis)

	patient := analysis["patient"].(map[string]any)

	if patient["birth_date"] != "1980-03-04" {
		t.Fatalf("unexpected canonical birth date: %#v", patient["birth_date"])
	}

	if _, exists := patient["birth_date_normalized"]; exists {
		t.Fatal("birth_date_normalized contraditório deve ser removido")
	}
}

func TestMergePatientCaseRemovesLegacyBirthDateAlias(t *testing.T) {
	existing := map[string]any{
		"patient": map[string]any{
			"full_name":             "PACIENTE TESTE SILVA",
			"birth_date":            "1980-03-04",
			"birth_date_normalized": "1980-04-03",
		},
		"source_files": []any{},
		"exams":        map[string]any{},
	}

	incoming := map[string]any{
		"patient": map[string]any{
			"full_name":  "PACIENTE TESTE SILVA",
			"birth_date": "1980-03-04",
		},
		"source_files": []any{},
		"exams":        map[string]any{},
	}

	merged := mergePatientCase(existing, incoming)
	patient := merged["patient"].(map[string]any)

	if patient["birth_date"] != "1980-03-04" {
		t.Fatalf("unexpected canonical birth date: %#v", patient["birth_date"])
	}

	if _, exists := patient["birth_date_normalized"]; exists {
		t.Fatal("legacy birth_date_normalized should be removed during merge")
	}
}

func TestBuildPatientChangePreviewChangedField(t *testing.T) {
	existing := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{"kmax_d": 44.2},
					},
				},
			},
		},
	}

	incoming := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{"kmax_d": 44.4},
					},
				},
			},
		},
	}

	preview := buildPatientChangePreview(existing, incoming)

	if preview.Changed != 1 || preview.Added != 0 || preview.Removed != 0 {
		t.Fatalf("unexpected counters: %#v", preview)
	}

	row := preview.Rows[0]
	if row.Eye != "OD" ||
		row.Field != "anterior_cornea.kmax_d" ||
		row.Before != 44.2 ||
		row.After != 44.4 ||
		row.Kind != "changed" {
		t.Fatalf("unexpected row: %#v", row)
	}
}

func TestBuildPatientChangePreviewAddsOSWithoutTouchingOD(t *testing.T) {
	existing := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{"value": 44.2},
				},
			},
		},
	}

	incoming := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OS": map[string]any{"value": 45.5},
				},
			},
		},
	}

	preview := buildPatientChangePreview(existing, incoming)

	if preview.Added != 1 ||
		preview.Changed != 0 ||
		preview.Removed != 0 {
		t.Fatalf("unexpected counters: %#v", preview)
	}

	if len(preview.Rows) != 1 {
		t.Fatalf("expected only incoming OS changes, got %#v", preview.Rows)
	}

	row := preview.Rows[0]
	if row.Eye != "OS" ||
		row.Field != "value" ||
		row.Before != nil ||
		row.After != 45.5 ||
		row.Kind != "added" {
		t.Fatalf("unexpected OS row: %#v", row)
	}
}

func TestMergePatientCaseExactReuploadKeepsExistingPayload(t *testing.T) {
	existing := map[string]any{
		"source_files": []any{
			map[string]any{
				"exam": "pentacam_corneal_tomography",
				"eye":  "OS", "sha256": "same",
				"path": "cases/x/original.pdf",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OS": map[string]any{"value": 45.5},
				},
			},
		},
	}

	incoming := map[string]any{
		"source_files": []any{
			map[string]any{
				"exam": "pentacam_corneal_tomography",
				"eye":  "OS", "sha256": "same",
				"path": "copy.pdf",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OS": map[string]any{"value": 99.9},
				},
			},
		},
	}

	merged := mergePatientCase(existing, incoming)
	os := merged["exams"].(map[string]any)["pentacam_corneal_tomography"].(map[string]any)["eyes"].(map[string]any)["OS"].(map[string]any)

	if os["value"] != 45.5 {
		t.Fatalf("exact reupload must preserve existing payload: %#v", os)
	}
}

func TestBuildPatientChangePreviewExactReuploadHasNoChanges(t *testing.T) {
	existing := map[string]any{
		"source_files": []any{
			map[string]any{
				"exam": "pentacam_corneal_tomography",
				"eye":  "OS", "sha256": "same",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OS": map[string]any{"value": 45.5},
				},
			},
		},
	}

	incoming := map[string]any{
		"source_files": []any{
			map[string]any{
				"exam": "pentacam_corneal_tomography",
				"eye":  "OS", "sha256": "same",
			},
		},
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OS": map[string]any{"value": 99.9},
				},
			},
		},
	}

	preview := buildPatientChangePreview(existing, incoming)

	if len(preview.Rows) != 0 ||
		preview.Added != 0 ||
		preview.Changed != 0 ||
		preview.Removed != 0 ||
		preview.Unchanged != 0 {
		t.Fatalf("exact reupload must produce empty preview: %#v", preview)
	}
}
