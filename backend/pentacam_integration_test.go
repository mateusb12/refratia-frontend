package main

import (
	"strings"
	"testing"
)

func TestPentacamPartialMergePreservesLocalAuthority(t *testing.T) {
	local := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{
							"k1_d": 43.5,
						},
						"cataract_preop": map[string]any{
							"total_corneal_z40_6mm_um": nil,
						},
					},
				},
			},
		},
	}

	fallback := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{
							"k1_d": 99.9,
						},
						"cataract_preop": map[string]any{
							"total_corneal_z40_6mm_um": 0.25,
						},
					},
				},
			},
		},
	}

	mergeFallbackAnalysis(local, fallback)

	exams := local["exams"].(map[string]any)
	pentacam := exams["pentacam_corneal_tomography"].(map[string]any)
	eyes := pentacam["eyes"].(map[string]any)
	od := eyes["OD"].(map[string]any)

	anterior := od["anterior_cornea"].(map[string]any)
	if anterior["k1_d"] != 43.5 {
		t.Fatalf("fallback sobrescreveu valor local: %v", anterior["k1_d"])
	}

	cataract := od["cataract_preop"].(map[string]any)
	if cataract["total_corneal_z40_6mm_um"] != 0.25 {
		t.Fatalf(
			"fallback não preencheu gap: %v",
			cataract["total_corneal_z40_6mm_um"],
		)
	}
}

func TestPentacamPartialProducesFieldLevelGaps(t *testing.T) {
	analysis := map[string]any{
		"exams": map[string]any{
			"pentacam_corneal_tomography": map[string]any{
				"eyes": map[string]any{
					"OD": map[string]any{
						"anterior_cornea": map[string]any{
							"k1_d": 43.5,
						},
					},
				},
				"source": []any{"pentacam-od.pdf"},
			},
		},
	}

	gaps := collectLocalGaps(
		analysis,
		[]uploadedFile{
			{
				Metadata: intakeFile{
					Filename:    "pentacam-od.pdf",
					ContentType: "application/pdf",
				},
			},
		},
	)

	found := false
	for _, gap := range gaps {
		if gap ==
			"pentacam.OD.cataract_preop.total_corneal_z40_6mm_um" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("gap específico do Pentacam não encontrado: %v", gaps)
	}
}

func TestFallbackPromptExistsForPartialExam(t *testing.T) {
	prompt := extractionPromptForLocalGaps(
		map[string]any{
			"exams": map[string]any{},
		},
		[]string{
			"pentacam.OD.cataract_preop.total_corneal_z40_6mm_um",
		},
	)

	if !strings.Contains(prompt, "MODO FALLBACK LOCAL-FIRST") {
		t.Fatal("fallback parcial não ativado")
	}

	if !strings.Contains(
		prompt,
		"pentacam.OD.cataract_preop.total_corneal_z40_6mm_um",
	) {
		t.Fatal("prompt não contém gap exato")
	}
}
