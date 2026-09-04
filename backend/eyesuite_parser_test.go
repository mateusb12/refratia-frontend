package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseEyeSuiteTSVCompleteBothEyes(t *testing.T) {
	var lines []string
	lines = append(lines,
		"level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext",
		tsvWord(1, 0, 0, 2480, 3508, ""),
	)

	add := func(x, y int, words ...string) {
		for i, word := range words {
			lines = append(lines, tsvWord(5, x+i*100, y, 80, 30, word))
		}
	}

	// OD — dados inteiramente sintéticos.
	add(300, 840, "AL", "[mm]", "23,11", "K1", "[D/mm]", "40,25/8,12", "@", "80")
	add(300, 880, "CCT", "540", "K2", "[D/mm]", "41,75/8,01", "@", "170")
	add(300, 920, "AD", "3,00", "K", "[D/mm]", "41,00/8,06")
	add(300, 960, "ACD", "[mm]", "3,20", "-AST", "[D/]-1,50", "@", "90")
	add(300, 1000, "LT", "[mm]", "4,10")
	add(300, 1110, "WTW", "[mm]", "11,80")
	add(300, 1270, "Target", "Refraction:", "-0,25")

	// OS — dados inteiramente sintéticos.
	add(1400, 840, "AL", "[mm]", "24,22", "K1", "[D/mm]", "42,10/8,00", "@", "10")
	add(1400, 880, "CCT", "545", "K2", "[D/mm]", "43,30/7,90", "@", "100")
	add(1400, 920, "AD", "3,05", "K", "[D/mm]", "42,70/7,95")
	add(1400, 960, "ACD", "[mm]", "3,40", "-AST", "[D/]-1,20", "@", "10")
	add(1400, 1000, "LT", "[mm]", "4,30")
	add(1400, 1110, "WTW", "[mm]", "12,10")
	add(1400, 1270, "Target", "Refraction:", "0,00")

	exam, err := parseEyeSuiteTSV(strings.Join(lines, "\n"))
	if err != nil {
		t.Fatal(err)
	}

	eyes := exam["eyes"].(map[string]any)

	assertEyeSuiteValue(t, eyes, "OD", "axial_length_mm", 23.11)
	assertEyeSuiteK(t, eyes, "OD", "k1_d", 40.25)
	assertEyeSuiteK(t, eyes, "OD", "k2_d", 41.75)
	assertEyeSuiteK(t, eyes, "OD", "mean_k_d", 41.00)
	assertEyeSuiteK(t, eyes, "OD", "astigmatism_d", -1.50)
	assertEyeSuiteK(t, eyes, "OD", "astigmatism_axis_deg", 90)
	assertEyeSuiteValue(t, eyes, "OD", "anterior_chamber_depth_mm", 3.20)
	assertEyeSuiteValue(t, eyes, "OD", "lens_thickness_mm", 4.10)
	assertEyeSuiteValue(t, eyes, "OD", "white_to_white_mm", 11.80)
	assertEyeSuiteValue(t, eyes, "OD", "target_refraction_d", -0.25)

	assertEyeSuiteValue(t, eyes, "OS", "axial_length_mm", 24.22)
	assertEyeSuiteK(t, eyes, "OS", "k1_d", 42.10)
	assertEyeSuiteK(t, eyes, "OS", "k2_d", 43.30)
	assertEyeSuiteK(t, eyes, "OS", "mean_k_d", 42.70)
	assertEyeSuiteK(t, eyes, "OS", "astigmatism_d", -1.20)
	assertEyeSuiteK(t, eyes, "OS", "astigmatism_axis_deg", 10)
	assertEyeSuiteValue(t, eyes, "OS", "anterior_chamber_depth_mm", 3.40)
	assertEyeSuiteValue(t, eyes, "OS", "lens_thickness_mm", 4.30)
	assertEyeSuiteValue(t, eyes, "OS", "white_to_white_mm", 12.10)
	assertEyeSuiteValue(t, eyes, "OS", "target_refraction_d", 0)
}

func tsvWord(level, left, top, width, height int, text string) string {
	return fmt.Sprintf(
		"%d\t1\t1\t1\t1\t1\t%d\t%d\t%d\t%d\t95\t%s",
		level, left, top, width, height, text,
	)
}

func assertEyeSuiteValue(t *testing.T, eyes map[string]any, eye, key string, want float64) {
	t.Helper()
	payload := eyes[eye].(map[string]any)
	got := payload[key].(float64)
	if got != want {
		t.Fatalf("%s %s: got %v want %v", eye, key, got, want)
	}
}

func assertEyeSuiteK(t *testing.T, eyes map[string]any, eye, key string, want float64) {
	t.Helper()
	payload := eyes[eye].(map[string]any)
	k := payload["keratometry"].(map[string]any)
	got := k[key].(float64)
	if got != want {
		t.Fatalf("%s %s: got %v want %v", eye, key, got, want)
	}
}

func TestParseEyeSuiteIdentityWords(t *testing.T) {
	words := []ocrWord{
		{Text: "PESSOA,", Left: 100, Top: 420},
		{Text: "TESTE", Left: 300, Top: 421},
		{Text: "SILVA,", Left: 500, Top: 421},
		{Text: "03/14/1980", Left: 700, Top: 420},

		{Text: "Measurement", Left: 1200, Top: 440},
		{Text: "ID", Left: 1450, Top: 440},
		{Text: "(CID):", Left: 1510, Top: 440},
		{Text: "1234", Left: 1620, Top: 440},

		{Text: "08/31/26", Left: 1400, Top: 490},
		{Text: "11:52", Left: 1600, Top: 490},
	}

	got, err := parseEyeSuiteIdentityWords(words)
	if err != nil {
		t.Fatal(err)
	}

	if got.FullName != "PESSOA TESTE SILVA" {
		t.Fatalf("nome inesperado: %q", got.FullName)
	}
	if got.BirthDateRaw != "03/14/1980" {
		t.Fatalf("nascimento inesperado: %q", got.BirthDateRaw)
	}
	if got.TimestampRaw != "08/31/26 11:52" {
		t.Fatalf("timestamp inesperado: %q", got.TimestampRaw)
	}
}

func TestParseEyeSuiteIdentityRejectsIncompleteHeader(t *testing.T) {
	words := []ocrWord{
		{Text: "PESSOA", Left: 100, Top: 420},
		{Text: "TESTE", Left: 300, Top: 420},
		{Text: "03/14/1980", Left: 500, Top: 420},
		{Text: "Measurement", Left: 1200, Top: 440},
		{Text: "ID", Left: 1450, Top: 440},
		{Text: "(CID):", Left: 1510, Top: 440},
	}

	if _, err := parseEyeSuiteIdentityWords(words); err == nil {
		t.Fatal("cabeçalho sem timestamp deveria ser rejeitado")
	}
}
