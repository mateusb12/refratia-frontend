package main

import "context"

type pentacamAdaptiveCrop struct {
	X int
	Y int
	W int
	H int
}

type pentacamAdaptiveSpec struct {
	Page           int
	Crops          []pentacamAdaptiveCrop
	RequireDecimal bool
	MinValue       float64
	MaxValue       float64
}

func readPentacamAdaptiveNumber(
	ctx context.Context,
	pdf []byte,
	spec pentacamAdaptiveSpec,
) (float64, bool) {
	page, err := renderPDFPageAtDPI(
		ctx,
		pdf,
		spec.Page,
		300,
	)
	if err != nil {
		return 0, false
	}

	type evidence struct {
		thresholds map[uint8]bool
		crops      map[int]bool
	}

	candidates := map[float64]*evidence{}

	for cropIndex, c := range spec.Crops {
		cell, err := cropReferencePNG(
			page,
			referenceCrop{
				X: c.X,
				Y: c.Y,
				W: c.W,
				H: c.H,
			},
		)
		if err != nil {
			continue
		}

		for _, threshold := range []uint8{
			140, 150, 160, 170, 180, 190,
		} {
			img, err := pentacamBinaryUpscale(
				cell,
				4,
				threshold,
			)
			if err != nil {
				continue
			}

			// Um bitmap só pode gerar um voto.
			observed := map[float64]bool{}

			for _, psm := range []int{7, 8, 13} {
				text, err := pentacamNumericOCR(
					ctx,
					img,
					psm,
				)
				if err != nil {
					continue
				}

				value, ok := pentacamParseCellNumber(
					text,
					spec.RequireDecimal,
					spec.MinValue,
					spec.MaxValue,
				)
				if ok {
					observed[value] = true
				}
			}

			// PSMs discordaram ou nenhum conseguiu ler:
			// este bitmap não conta como evidência.
			if len(observed) != 1 {
				continue
			}

			for value := range observed {
				if candidates[value] == nil {
					candidates[value] = &evidence{
						thresholds: map[uint8]bool{},
						crops:      map[int]bool{},
					}
				}

				candidates[value].thresholds[threshold] = true
				candidates[value].crops[cropIndex] = true
			}
		}
	}

	var winner float64
	found := false

	for value, evidence := range candidates {
		// Critério conservador:
		// mesmo decimal em >=3 thresholds
		// e >=2 janelas espaciais.
		if len(evidence.thresholds) < 3 ||
			len(evidence.crops) < 2 {
			continue
		}

		// Mais de um valor satisfazendo o critério:
		// ambíguo, portanto rejeitar.
		if found {
			return 0, false
		}

		winner = value
		found = true
	}

	return winner, found
}

func readPentacamK1Adaptive(
	ctx context.Context,
	pdf []byte,
) (float64, bool) {
	return readPentacamAdaptiveNumber(
		ctx,
		pdf,
		pentacamAdaptiveSpec{
			Page:           4,
			RequireDecimal: true,
			MinValue:       20,
			MaxValue:       80,
			Crops: []pentacamAdaptiveCrop{
				{X: 540, Y: 1275, W: 150, H: 60},
				{X: 525, Y: 1265, W: 175, H: 75},
				{X: 550, Y: 1265, W: 160, H: 75},
				{X: 515, Y: 1255, W: 200, H: 90},
			},
		},
	)
}

func readPentacamACDAdaptive(
	ctx context.Context,
	pdf []byte,
) (float64, bool) {
	// Cataract Pre-OP — Prof.Cam.Ant.(Int.).
	//
	// A linha Ext. fica imediatamente acima.
	// Estas janelas contêm apenas a célula numérica Int.
	return readPentacamAdaptiveNumber(
		ctx,
		pdf,
		pentacamAdaptiveSpec{
			Page:           7,
			RequireDecimal: true,
			MinValue:       1,
			MaxValue:       10,
			Crops: []pentacamAdaptiveCrop{
				{X: 2290, Y: 2460, W: 135, H: 55},
				{X: 2298, Y: 2466, W: 120, H: 46},
				{X: 2285, Y: 2456, W: 145, H: 62},
				{X: 2300, Y: 2463, W: 115, H: 50},
			},
		},
	)
}
