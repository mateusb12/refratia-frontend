package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type referenceCrop struct {
	X, Y, W, H int
}

func cropReferencePNG(data []byte, crop referenceCrop) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	b := img.Bounds()
	sx := float64(b.Dx()) / 2480.0
	sy := float64(b.Dy()) / 3508.0

	r := image.Rect(
		int(float64(crop.X)*sx),
		int(float64(crop.Y)*sy),
		int(float64(crop.X+crop.W)*sx),
		int(float64(crop.Y+crop.H)*sy),
	).Intersect(b)

	if r.Empty() {
		return nil, fmt.Errorf("crop vazio")
	}

	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func runTesseractTextPSM(ctx context.Context, pngData []byte, psm int) (string, error) {
	f, err := os.CreateTemp("", "refratia-focus-*.png")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer os.Remove(path)

	if _, err := f.Write(pngData); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(
		ctx,
		"tesseract",
		path,
		"stdout",
		"--psm",
		strconv.Itoa(psm),
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tesseract focused: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func focusedOCRVariants(ctx context.Context, page []byte, crop referenceCrop) ([]string, error) {
	image, err := cropReferencePNG(page, crop)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, 3)
	for _, psm := range []int{6, 11, 12} {
		text, err := runTesseractTextPSM(ctx, image, psm)
		if err == nil && strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("OCR focado vazio")
	}
	return result, nil
}
