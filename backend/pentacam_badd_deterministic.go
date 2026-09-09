package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
)

var pentacamNonDigit = regexp.MustCompile(`[^0-9]`)

func readPentacamBADDDeterministic(
	ctx context.Context,
	pdf []byte,
) (float64, bool) {
	page, err := renderPDFPageAtDPI(ctx, pdf, 7, 300)
	if err != nil {
		return 0, false
	}

	cell, err := cropReferencePNG(
		page,
		referenceCrop{
			X: 1745,
			Y: 2137,
			W: 100,
			H: 62,
		},
	)
	if err != nil {
		return 0, false
	}

	// Não inferir o decimal pela semântica do BAD-D.
	// O ponto precisa existir fisicamente no bitmap.
	if !pentacamBADDHasStableDot(cell) {
		return 0, false
	}

	// Coordenadas determinadas geometricamente no bitmap 4x.
	//
	// Primeiro dígito:  x≈176..224
	// ponto físico:     x≈227
	// segundo dígito:   x≈248..296
	// terceiro dígito:  x≈296..344
	boxes := []image.Rectangle{
		image.Rect(176, 70, 224, 180),
		image.Rect(248, 70, 296, 180),
		image.Rect(296, 70, 344, 180),
	}

	digits := make([]byte, 3)

	for index, box := range boxes {
		digit, ok := pentacamBADDDigitConsensus(
			ctx,
			cell,
			box,
		)
		if !ok {
			return 0, false
		}

		digits[index] = digit
	}

	raw := string([]byte{
		digits[0],
		'.',
		digits[1],
		digits[2],
	})

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 20 {
		return 0, false
	}

	return value, true
}

func pentacamBADDDigitConsensus(
	ctx context.Context,
	cell []byte,
	box image.Rectangle,
) (byte, bool) {
	votes := map[byte]int{}

	for _, threshold := range []uint8{
		150, 160, 170, 180, 190,
	} {
		binary, err := pentacamBinaryUpscale(
			cell,
			4,
			threshold,
		)
		if err != nil {
			continue
		}

		digitImage, err := pentacamCropPNGRect(
			binary,
			box,
		)
		if err != nil {
			continue
		}

		// Um threshold = um voto no máximo.
		// PSMs diferentes não contam como evidências independentes.
		observed := map[byte]bool{}

		for _, psm := range []int{10, 13} {
			text, err := pentacamNumericOCR(
				ctx,
				digitImage,
				psm,
			)
			if err != nil {
				continue
			}

			digits := pentacamNonDigit.ReplaceAllString(
				text,
				"",
			)

			if len(digits) == 1 {
				observed[digits[0]] = true
			}
		}

		// Se os PSMs discordarem no mesmo threshold,
		// este threshold é descartado.
		if len(observed) != 1 {
			continue
		}

		for digit := range observed {
			votes[digit]++
		}
	}

	var winner byte
	best := 0
	second := 0

	for digit, count := range votes {
		if count > best {
			second = best
			best = count
			winner = digit
		} else if count > second {
			second = count
		}
	}

	// O mesmo dígito deve sobreviver a pelo menos
	// três binarizações independentes, sem empate.
	if best < 3 || best == second {
		return 0, false
	}

	return winner, true
}

func pentacamBADDHasStableDot(data []byte) bool {
	first, err := pentacamBADDDotComponents(data, 150)
	if err != nil {
		return false
	}

	second, err := pentacamBADDDotComponents(data, 160)
	if err != nil {
		return false
	}

	for _, a := range first {
		for _, b := range second {
			if pentacamAbs(a.x-b.x) <= 1 &&
				pentacamAbs(a.y-b.y) <= 1 &&
				a.x >= 54 && a.x <= 61 &&
				a.y >= 33 && a.y <= 43 {
				return true
			}
		}
	}

	return false
}

type pentacamDotComponent struct {
	x int
	y int
	w int
	h int
	n int
}

func pentacamBADDDotComponents(
	data []byte,
	threshold uint8,
) ([]pentacamDotComponent, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	seen := make([]bool, width*height)
	result := []pentacamDotComponent{}

	isBlack := func(x, y int) bool {
		gray := color.GrayModel.Convert(
			src.At(
				bounds.Min.X+x,
				bounds.Min.Y+y,
			),
		).(color.Gray).Y

		return gray < threshold
	}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x

			if seen[index] || !isBlack(x, y) {
				continue
			}

			queue := [][2]int{{x, y}}
			seen[index] = true

			minX, maxX := x, x
			minY, maxY := y, y
			count := 0

			for len(queue) > 0 {
				point := queue[0]
				queue = queue[1:]

				px := point[0]
				py := point[1]
				count++

				if px < minX {
					minX = px
				}
				if px > maxX {
					maxX = px
				}
				if py < minY {
					minY = py
				}
				if py > maxY {
					maxY = py
				}

				for _, delta := range [][2]int{
					{1, 0},
					{-1, 0},
					{0, 1},
					{0, -1},
				} {
					nx := px + delta[0]
					ny := py + delta[1]

					if nx < 0 ||
						ny < 0 ||
						nx >= width ||
						ny >= height {
						continue
					}

					next := ny*width + nx

					if seen[next] || !isBlack(nx, ny) {
						continue
					}

					seen[next] = true
					queue = append(
						queue,
						[2]int{nx, ny},
					)
				}
			}

			component := pentacamDotComponent{
				x: minX,
				y: minY,
				w: maxX - minX + 1,
				h: maxY - minY + 1,
				n: count,
			}

			if component.n >= 2 &&
				component.n <= 40 &&
				component.w <= 8 &&
				component.h <= 8 {
				result = append(
					result,
					component,
				)
			}
		}
	}

	return result, nil
}

func pentacamCropPNGRect(
	data []byte,
	rect image.Rectangle,
) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	bounds := src.Bounds()

	if rect.Min.X < bounds.Min.X ||
		rect.Min.Y < bounds.Min.Y ||
		rect.Max.X > bounds.Max.X ||
		rect.Max.Y > bounds.Max.Y {
		return nil, image.ErrFormat
	}

	dst := image.NewRGBA(
		image.Rect(
			0,
			0,
			rect.Dx(),
			rect.Dy(),
		),
	)

	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			dst.Set(
				x-rect.Min.X,
				y-rect.Min.Y,
				src.At(x, y),
			)
		}
	}

	var output bytes.Buffer

	if err := png.Encode(&output, dst); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

func pentacamAbs(value int) int {
	if value < 0 {
		return -value
	}

	return value
}
