package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func extractPatientLocal(ctx context.Context, files []uploadedFile) map[string]any {
	exams := map[string]any{}
	analysis := map[string]any{"exams": exams}

	for _, file := range files {
		if file.Metadata.ContentType != "application/pdf" {
			continue
		}

		bundle, err := extractEyeSuitePDFLocalBundle(ctx, file.Data)
		if err != nil {
			continue
		}

		bundle.Exam["source"] = []any{file.Metadata.Filename}
		exams["iol_calculation"] = bundle.Exam

		if bundle.Identity != nil {
			patient := map[string]any{
				"full_name": bundle.Identity.FullName,
			}
			analysis["patient"] = patient

			analysis["verificacao_identidade"] = []any{
				map[string]any{
					"source":          file.Metadata.Filename,
					"nome_lido":       bundle.Identity.FullName,
					"nascimento_lido": bundle.Identity.BirthDateRaw,
					"timestamp_lido":  bundle.Identity.TimestampRaw,
					"confidence":      "deterministic_template",
					"method":          "local_ocr_tesseract",
				},
			}

			if birthDate, ok := canonicalPatientBirthDate(analysis); ok {
				patient["birth_date"] = birthDate
			}
		}

		break
	}

	return analysis
}

func localResolvedExamKeys(analysis map[string]any) map[string]bool {
	resolved := map[string]bool{}
	exams, _ := analysis["exams"].(map[string]any)

	if exam, _ := exams["iol_calculation"].(map[string]any); exam != nil && !iolNeedsRepair(exam) {
		resolved["iol_calculation"] = true
	}

	return resolved
}

func localClaimedFiles(analysis map[string]any) map[string]bool {
	claimed := map[string]bool{}
	exams, _ := analysis["exams"].(map[string]any)

	for _, raw := range exams {
		exam, _ := raw.(map[string]any)
		sources, _ := exam["source"].([]any)
		for _, source := range sources {
			claimed[fmt.Sprint(source)] = true
		}
	}

	return claimed
}

func localPatientIdentityComplete(analysis map[string]any) bool {
	patient, _ := analysis["patient"].(map[string]any)
	name, _ := patient["full_name"].(string)
	birth, _ := patient["birth_date"].(string)

	return strings.TrimSpace(name) != "" && strings.TrimSpace(birth) != ""
}

func localIdentitySources(analysis map[string]any) map[string]bool {
	result := map[string]bool{}

	entries, _ := analysis["verificacao_identidade"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		source, _ := entry["source"].(string)
		if source != "" {
			result[source] = true
		}
	}

	return result
}

func localFallbackFiles(analysis map[string]any, files []uploadedFile) []uploadedFile {
	claimed := localClaimedFiles(analysis)
	identitySources := localIdentitySources(analysis)
	identityComplete := localPatientIdentityComplete(analysis)

	result := make([]uploadedFile, 0, len(files))
	for _, file := range files {
		if identityComplete &&
			claimed[file.Metadata.Filename] &&
			identitySources[file.Metadata.Filename] {
			continue
		}
		result = append(result, file)
	}

	return result
}

func collectLocalGaps(analysis map[string]any, files []uploadedFile) []string {
	gaps := []string{}
	seen := map[string]bool{}

	add := func(gap string) {
		if !seen[gap] {
			seen[gap] = true
			gaps = append(gaps, gap)
		}
	}

	if !localPatientIdentityComplete(analysis) {
		add("patient_identity")
	}

	claimed := localClaimedFiles(analysis)
	identitySources := localIdentitySources(analysis)

	for _, file := range files {
		if !claimed[file.Metadata.Filename] {
			add("unresolved_file")
			continue
		}

		if !identitySources[file.Metadata.Filename] {
			add("document_identity")
		}
	}

	return gaps
}

func mergeFallbackAnalysis(local, fallback map[string]any) {
	var fallbackIdentity []any

	if entries, ok := fallback["verificacao_identidade"].([]any); ok {
		fallbackIdentity = entries
		delete(fallback, "verificacao_identidade")
	}

	mergeMissingValues(local, fallback)

	if len(fallbackIdentity) > 0 {
		current, _ := local["verificacao_identidade"].([]any)
		local["verificacao_identidade"] = append(current, fallbackIdentity...)
	}
}

func extractionPromptForLocalGaps(analysis map[string]any, gaps []string) string {
	resolved := localResolvedExamKeys(analysis)
	keys := make([]string, 0, len(resolved))
	for key := range resolved {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if len(keys) == 0 {
		return extractionPrompt
	}

	return extractionPrompt + fmt.Sprintf(`

MODO FALLBACK:
A extração determinística local já resolveu completamente estes exames:
%s

NÃO extraia nem retorne esses exames em "exams".
Use este fallback exclusivamente para informações ainda ausentes.
Gaps atualmente conhecidos: %s.
Não altere nem reinterprete dados já resolvidos localmente.`,
		strings.Join(keys, ", "),
		strings.Join(gaps, ", "),
	)
}

func stripLocallyResolvedExams(analysis map[string]any, resolved map[string]bool) {
	exams, _ := analysis["exams"].(map[string]any)
	for key := range resolved {
		delete(exams, key)
	}
}
