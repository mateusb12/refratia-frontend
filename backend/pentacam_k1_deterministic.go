package main

import (
	"context"
)

func readPentacamK1Deterministic(
	ctx context.Context,
	pdf []byte,
) (float64, bool) {
	page, err := renderPDFPageAtDPI(ctx, pdf, 4, 300)
	if err != nil {
		return 0, false
	}

	crops := []referenceCrop{
		{X: 535, Y: 1270, W: 180, H: 70},
		{X: 540, Y: 1275, W: 150, H: 60},
		{X: 520, Y: 1255, W: 220, H: 100},
	}

	type evidence struct {
		thresholds map[uint8]bool
		crops      map[int]bool
	}

	values := map[float64]*evidence{}

	for cropIndex, cropSpec := range crops {
		cell, err := cropReferencePNG(page, cropSpec)
		if err != nil {
			continue
		}

		for _, threshold := range []uint8{
			150, 160, 170, 180, 190,
		} {
			img, err := pentacamBinaryUpscale(
				cell,
				4,
				threshold,
			)
			if err != nil {
				continue
			}

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
					true,
					20,
					80,
				)
				if ok {
					observed[value] = true
				}
			}

			// Um bitmap só vota quando não existe
			// ambiguidade entre os PSMs.
			if len(observed) != 1 {
				continue
			}

			for value := range observed {
				if values[value] == nil {
					values[value] = &evidence{
						thresholds: map[uint8]bool{},
						crops:      map[int]bool{},
					}
				}

				values[value].thresholds[threshold] = true
				values[value].crops[cropIndex] = true
			}
		}
	}

	var winner float64
	found := false

	for value, evidence := range values {
		if len(evidence.thresholds) < 3 ||
			len(evidence.crops) < 2 {
			continue
		}

		// Mais de um candidato passando o critério
		// significa ambiguidade: não aceitar.
		if found {
			return 0, false
		}

		winner = value
		found = true
	}

	return winner, found
}
