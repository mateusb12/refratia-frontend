package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// preparedFile is deliberately kept compatible with uploadedFile. A PDF is
// replaced by either its text layer or one PNG per page before it reaches the
// vision model. The original filename is retained so source_files continue to
// point to the user's document.
type preparedFile struct {
	File      uploadedFile
	Page      int
	TextLayer string
}

func prepareExtractionFiles(ctx context.Context, files []uploadedFile) ([]preparedFile, error) {
	prepared := make([]preparedFile, 0, len(files))
	for _, file := range files {
		if file.Metadata.ContentType != "application/pdf" {
			prepared = append(prepared, preparedFile{File: file})
			continue
		}

		pages, textLayer, err := inspectPDF(ctx, file.Data)
		if err != nil {
			return nil, fmt.Errorf("não foi possível preparar %s: %w", file.Metadata.Filename, err)
		}
		if strings.TrimSpace(textLayer) != "" {
			prepared = append(prepared, preparedFile{File: file, TextLayer: textLayer})
			continue
		}

		pageImages, err := renderPDFPages(ctx, file.Data, pages)
		if err != nil {
			return nil, fmt.Errorf("não foi possível renderizar %s: %w", file.Metadata.Filename, err)
		}
		for index, image := range pageImages {
			metadata := file.Metadata
			metadata.ContentType = "image/png"
			metadata.Size = int64(len(image))
			prepared = append(prepared, preparedFile{
				File: uploadedFile{Metadata: metadata, Data: image},
				Page: index + 1,
			})
		}
	}
	return prepared, nil
}

func inspectPDF(ctx context.Context, data []byte) (int, string, error) {
	pagesOutput, err := runPDFCommand(ctx, "pdfinfo", data)
	if err != nil {
		return 0, "", err
	}
	pages := 0
	for _, line := range strings.Split(pagesOutput, "\n") {
		if strings.HasPrefix(line, "Pages:") {
			pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
		}
	}
	if pages < 1 {
		return 0, "", fmt.Errorf("número de páginas não identificado")
	}
	textLayer, err := runPDFCommand(ctx, "pdftotext", data)
	if err != nil {
		return 0, "", err
	}
	return pages, textLayer, nil
}

func runPDFCommand(ctx context.Context, name string, data []byte) (string, error) {
	input, err := os.CreateTemp("", "refratia-pdf-*.pdf")
	if err != nil {
		return "", err
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err := input.Write(data); err != nil {
		input.Close()
		return "", err
	}
	if err := input.Close(); err != nil {
		return "", err
	}
	args := []string{inputPath}
	if name == "pdftotext" {
		args = []string{"-layout", inputPath, "-"}
	}
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(output), nil
}

func renderPDFPages(ctx context.Context, data []byte, pages int) ([][]byte, error) {
	return renderPDFPagesAtDPI(ctx, data, pages, 180)
}

func renderPDFPageAtDPI(ctx context.Context, data []byte, page, dpi int) ([]byte, error) {
	input, err := os.CreateTemp("", "refratia-page-*.pdf")
	if err != nil {
		return nil, err
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)

	if _, err := input.Write(data); err != nil {
		input.Close()
		return nil, err
	}
	if err := input.Close(); err != nil {
		return nil, err
	}

	directory, err := os.MkdirTemp("", "refratia-page-png-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)

	prefix := filepath.Join(directory, "page")
	command := exec.CommandContext(
		ctx,
		"pdftoppm",
		"-f", strconv.Itoa(page),
		"-singlefile",
		"-png",
		"-r", strconv.Itoa(dpi),
		inputPath,
		prefix,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %s", strings.TrimSpace(string(output)))
	}

	image, err := os.ReadFile(prefix + ".png")
	if err != nil {
		return nil, fmt.Errorf("página %d: %w", page, err)
	}
	return image, nil
}

func renderPDFPagesAtDPI(ctx context.Context, data []byte, pages, dpi int) ([][]byte, error) {
	input, err := os.CreateTemp("", "refratia-pages-*.pdf")
	if err != nil {
		return nil, err
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err := input.Write(data); err != nil {
		input.Close()
		return nil, err
	}
	if err := input.Close(); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "refratia-png-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	prefix := filepath.Join(directory, "page")
	command := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", strconv.Itoa(dpi), inputPath, prefix)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %s", strings.TrimSpace(string(output)))
	}
	images := make([][]byte, 0, pages)
	for page := 1; page <= pages; page++ {
		path := fmt.Sprintf("%s-%d.png", prefix, page)
		image, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("página %d: %w", page, err)
		}
		images = append(images, image)
	}
	return images, nil
}

func preparedPromptLabel(file preparedFile) string {
	if file.Page > 0 {
		return fmt.Sprintf("Arquivo: %s — página %d", file.File.Metadata.Filename, file.Page)
	}
	return "Arquivo: " + file.File.Metadata.Filename
}
