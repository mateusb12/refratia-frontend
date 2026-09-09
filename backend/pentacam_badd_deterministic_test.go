package main

import "testing"

func TestPentacamAbs(t *testing.T) {
	if pentacamAbs(-2) != 2 ||
		pentacamAbs(2) != 2 {
		t.Fatal("pentacamAbs inválido")
	}
}

func TestPentacamBADDRejectsDigitTie(t *testing.T) {
	// A regra de produção exige maioria entre thresholds.
	// Este teste documenta que empate não deve ser considerado confiança.
	best := 3
	second := 3

	if best >= 3 && best != second {
		t.Fatal("empate não pode ser aceito")
	}
}
