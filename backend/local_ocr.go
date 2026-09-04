package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func runTesseractTSV(ctx context.Context, png []byte) (string, error) {
	input, err := os.CreateTemp("", "refratia-ocr-*.png")
	if err != nil {
		return "", err
	}
	path := input.Name()
	defer os.Remove(path)

	if _, err := input.Write(png); err != nil {
		input.Close()
		return "", err
	}
	if err := input.Close(); err != nil {
		return "", err
	}

	command := exec.CommandContext(ctx, "tesseract", path, "stdout", "tsv")
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("tesseract: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("tesseract: %w", err)
	}

	return string(output), nil
}

type eyeSuiteLocalBundle struct {
	Exam     map[string]any
	Identity *eyeSuiteIdentity
}

func extractEyeSuitePDFLocal(ctx context.Context, data []byte) (map[string]any, error) {
	bundle, err := extractEyeSuitePDFLocalBundle(ctx, data)
	if err != nil {
		return nil, err
	}
	return bundle.Exam, nil
}

func extractEyeSuitePDFLocalBundle(ctx context.Context, data []byte) (eyeSuiteLocalBundle, error) {
	if _, _, err := inspectPDF(ctx, data); err != nil {
		return eyeSuiteLocalBundle{}, err
	}

	var bestExam map[string]any
	var lastErr error

	for _, dpi := range []int{300, 450} {
		image, err := renderPDFPageAtDPI(ctx, data, 1, dpi)
		if err != nil {
			lastErr = err
			continue
		}

		tsv, err := runTesseractTSV(ctx, image)
		if err != nil {
			lastErr = err
			continue
		}

		exam, err := parseEyeSuiteTSV(tsv)
		if err != nil {
			lastErr = fmt.Errorf("%d DPI: %w", dpi, err)
			continue
		}

		if bestExam == nil {
			bestExam = exam
		}

		identity, identityErr := parseEyeSuiteIdentityTSV(tsv)
		if identityErr == nil {
			return eyeSuiteLocalBundle{
				Exam:     exam,
				Identity: &identity,
			}, nil
		}

		lastErr = fmt.Errorf("%d DPI identidade: %w", dpi, identityErr)
	}

	if bestExam != nil {
		return eyeSuiteLocalBundle{Exam: bestExam}, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("extração local não produziu resultado")
	}
	return eyeSuiteLocalBundle{}, fmt.Errorf("EyeSuite: %w", lastErr)
}
