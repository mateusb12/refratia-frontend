package main

import "testing"

func TestParsePentacamPageTextsComplete(t *testing.T) {
	pages := map[int]string{
		4: `
Córnea Ant.
K1 43.50 D
K2 43.80 D
Km 43.60 D
Astig. 0.20 D
`,
		6: `
Topométrico / Estadiamento KC
Thinnest Pachy: 529 um
ISV: 10
IVA: 0.09
IHA: 4.20
KI: 1.01
CKI: 1.00
TKC: -
`,
		7: `
PENTACAM Cataract Pre-OP
BAD D: 0.65
Total Corneal Z40 (6mm): 0.287 um
Prof.Câm.Ant.(Int.) 3.36 mm
`,
		8: `
Ectasia Reforçada Belin / Ambrósio
ARTmax: 416
`,
		9: `
Anéis Corneanos
Z31 (Coma): 0.097 um
`,
	}

	eye, err := parsePentacamPageTexts(pages)
	if err != nil {
		t.Fatal(err)
	}

	if err := pentacamContractComplete(eye); err != nil {
		t.Fatal(err)
	}

	anterior := eye["anterior_cornea"].(map[string]any)
	if anterior["k1_d"] != float64(43.5) {
		t.Fatalf("K1 inesperado: %v", anterior["k1_d"])
	}

	segment := eye["anterior_segment"].(map[string]any)
	if segment["internal_anterior_chamber_depth_mm"] != float64(3.36) {
		t.Fatalf("ACD Int inesperado: %v", segment["internal_anterior_chamber_depth_mm"])
	}
}

func TestPentacamDoesNotInventMissingDecimal(t *testing.T) {
	pages := map[int]string{
		4: "K1 435 D\nK2 43.80 D\nKm 43.60 D\nAstig 0.20 D",
		6: "Thinnest Pachy 529\nISV 10\nIVA 0.09\nIHA 4.2\nKI 1.01\nCKI 1.00\nTKC -",
		7: "BAD D 0.65\nTotal Corneal Z40 (6mm) 0.287\nProf.Cam.Ant.(Int.) 3.36",
		8: "ARTmax 416",
		9: "Z31 (Coma) 0.097",
	}

	if _, err := parsePentacamPageTexts(pages); err == nil {
		t.Fatal("K1 sem decimal não pode ser aceito como valor clínico")
	}
}
