package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type confirmationReceipt struct {
	CaseID      string `json:"caseId"`
	AnalysisKey string `json:"analysisKey"`
	Action      string `json:"action"`
}

type patientCaseCandidate struct {
	CaseID   string
	Analysis map[string]any
}

func patientIdentity(analysis map[string]any) (string, bool) {
	patient, ok := analysis["patient"].(map[string]any)
	if !ok || patient == nil {
		return "", false
	}

	fullName, _ := patient["full_name"].(string)
	fullName = normalizePatientName(fullName)

	birthDate, birthOK := canonicalPatientBirthDate(analysis)

	// Não fazemos merge probabilístico.
	// Sem nome E nascimento confiáveis, é mais seguro criar outro registro
	// e deixar a divergência para revisão do que juntar dois pacientes.
	if fullName == "" || !birthOK {
		return "", false
	}

	return fullName + "|" + birthDate, true
}

func canonicalPatientBirthDate(analysis map[string]any) (string, bool) {
	// A evidência bruta do documento tem prioridade sobre datas
	// "normalized" produzidas pelo modelo.
	//
	// Exemplo real do Pentacam:
	//   nascimento_lido = 03/04/1980
	//   timestamp_lido  = 08/31/2026
	//
	// Como 30 não pode ser mês, o documento está em MM/DD/YYYY.
	// Portanto 03/04/1980 = 1980-03-04.

	rawBirthDates := make([]string, 0)
	dateOrder := ""

	if entries, ok := analysis["verificacao_identidade"].([]any); ok {
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}

			if raw, ok := entry["nascimento_lido"].(string); ok && strings.TrimSpace(raw) != "" {
				rawBirthDates = append(rawBirthDates, strings.TrimSpace(raw))
			}

			if timestamp, ok := entry["timestamp_lido"].(string); ok {
				hint := inferSlashDateOrder(timestamp)

				if hint != "" {
					if dateOrder != "" && dateOrder != hint {
						return "", false
					}
					dateOrder = hint
				}
			}
		}
	}

	if len(rawBirthDates) > 0 {
		resolved := ""

		for _, raw := range rawBirthDates {
			canonical, ok := canonicalizeDocumentDate(raw, dateOrder)
			if !ok {
				// Existe uma data bruta, mas ela é ambígua.
				// Não caímos para o "normalized" do modelo porque ele pode
				// ter invertido dia e mês.
				return "", false
			}

			if resolved != "" && resolved != canonical {
				return "", false
			}

			resolved = canonical
		}

		if resolved != "" {
			return resolved, true
		}
	}

	// Fallback para análises que não possuem verificacao_identidade.
	// Aceitamos somente ISO já válido.
	patient, ok := analysis["patient"].(map[string]any)
	if !ok {
		return "", false
	}

	if value, ok := patient["birth_date"].(string); ok {
		if canonical, ok := canonicalISODate(value); ok {
			return canonical, true
		}
	}

	if value, ok := patient["birth_date_normalized"].(string); ok {
		if canonical, ok := canonicalISODate(value); ok {
			return canonical, true
		}
	}

	if value, ok := patient["birth_date"].(map[string]any); ok {
		if normalized, ok := value["normalized"].(string); ok {
			if canonical, ok := canonicalISODate(normalized); ok {
				return canonical, true
			}
		}
	}

	return "", false
}

func inferSlashDateOrder(value string) string {
	datePart := strings.Fields(strings.TrimSpace(value))
	if len(datePart) == 0 {
		return ""
	}

	parts := strings.Split(datePart[0], "/")
	if len(parts) != 3 {
		return ""
	}

	var first, second int
	if _, err := fmt.Sscanf(parts[0], "%d", &first); err != nil {
		return ""
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &second); err != nil {
		return ""
	}

	switch {
	case first > 12 && second <= 12:
		return "DMY"
	case second > 12 && first <= 12:
		return "MDY"
	default:
		return ""
	}
}

func canonicalizeDocumentDate(value, order string) (string, bool) {
	value = strings.TrimSpace(value)

	if canonical, ok := canonicalISODate(value); ok {
		return canonical, true
	}

	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return "", false
	}

	var first, second, year int
	if _, err := fmt.Sscanf(parts[0], "%d", &first); err != nil {
		return "", false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &second); err != nil {
		return "", false
	}
	if _, err := fmt.Sscanf(parts[2], "%d", &year); err != nil {
		return "", false
	}

	day := 0
	month := 0

	switch {
	case first > 12 && second <= 12:
		day = first
		month = second

	case second > 12 && first <= 12:
		month = first
		day = second

	case order == "DMY":
		day = first
		month = second

	case order == "MDY":
		month = first
		day = second

	default:
		return "", false
	}

	candidate := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	return canonicalISODate(candidate)
}

func canonicalISODate(value string) (string, bool) {
	value = strings.TrimSpace(value)

	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", false
	}

	return parsed.Format("2006-01-02"), true
}

func normalizePatientIdentityFields(analysis map[string]any) {
	patient, ok := analysis["patient"].(map[string]any)
	if !ok || patient == nil {
		return
	}

	if birthDate, ok := canonicalPatientBirthDate(analysis); ok {
		patient["birth_date"] = birthDate

		// O modelo pode devolver aliases "normalized" incorretos.
		// Depois que a data canônica foi determinada pela evidência bruta,
		// removemos esses valores concorrentes para não persistir duas datas.
		delete(patient, "birth_date_normalized")
	}
}

func normalizePatientName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c",
	)
	value = replacer.Replace(value)

	var normalized strings.Builder
	lastWasSpace := false

	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
			lastWasSpace = false
			continue
		}

		if !lastWasSpace {
			normalized.WriteByte(' ')
			lastWasSpace = true
		}
	}

	return strings.Join(strings.Fields(normalized.String()), " ")
}

func findExistingPatientCase(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	incoming map[string]any,
) (string, map[string]any, bool, error) {
	identity, ok := patientIdentity(incoming)
	if !ok {
		return "", nil, false, nil
	}

	candidates := make([]patientCaseCandidate, 0)
	var continuation *string

	for {
		listed, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String("cases/"),
			Delimiter:         aws.String("/"),
			ContinuationToken: continuation,
		})
		if err != nil {
			return "", nil, false, err
		}

		for _, prefix := range listed.CommonPrefixes {
			casePrefix := aws.ToString(prefix.Prefix)
			caseID := strings.TrimSuffix(strings.TrimPrefix(casePrefix, "cases/"), "/")
			if caseID == "" {
				continue
			}

			analysis, err := loadStoredCaseAnalysis(ctx, client, bucket, caseID)
			if err != nil {
				// Um case quebrado não deve impedir a confirmação dos demais.
				continue
			}

			existingIdentity, identifiable := patientIdentity(analysis)
			if identifiable && existingIdentity == identity {
				candidates = append(candidates, patientCaseCandidate{
					CaseID:   caseID,
					Analysis: analysis,
				})
			}
		}

		if !aws.ToBool(listed.IsTruncated) {
			break
		}

		continuation = listed.NextContinuationToken
	}

	if len(candidates) == 0 {
		return "", nil, false, nil
	}

	// Se já existirem duplicatas legadas do mesmo paciente, usamos o case
	// lexicograficamente mais recente. Não apagamos nada automaticamente.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CaseID < candidates[j].CaseID
	})

	selected := candidates[len(candidates)-1]
	return selected.CaseID, selected.Analysis, true, nil
}

func loadStoredCaseAnalysis(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	caseID string,
) (map[string]any, error) {
	key := "cases/" + caseID + "/paciente_compilado.json"

	object, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer object.Body.Close()

	var analysis map[string]any
	if err := json.NewDecoder(io.LimitReader(object.Body, maxAnalysisSize)).Decode(&analysis); err != nil {
		return nil, err
	}

	return analysis, nil
}

func mergePatientCase(existing, incoming map[string]any) map[string]any {
	if existing == nil {
		return cloneMap(incoming)
	}

	merged := cloneMap(existing)

	// Envelope / paciente / metadados:
	// valores novos não nulos prevalecem, mas campos ausentes no upload novo
	// continuam preservados.
	for key, incomingValue := range incoming {
		if key == "exams" || key == "source_files" {
			continue
		}

		if incomingValue == nil {
			continue
		}

		incomingMap, incomingIsMap := incomingValue.(map[string]any)
		existingMap, existingIsMap := merged[key].(map[string]any)

		if incomingIsMap && existingIsMap {
			mergeMapPreferIncoming(existingMap, incomingMap)
			continue
		}

		merged[key] = cloneValue(incomingValue)
	}

	// source_files representa a evidência clínica ATUAL.
	// Se chegaram novos arquivos do mesmo exame+olho, eles substituem
	// as referências antigas daquele mesmo grupo.
	merged["source_files"] = mergeSourceFiles(
		existing["source_files"],
		incoming["source_files"],
	)

	existingExams, _ := merged["exams"].(map[string]any)
	if existingExams == nil {
		existingExams = map[string]any{}
	}

	incomingExams, _ := incoming["exams"].(map[string]any)
	exactGroups := exactReuploadGroups(
		existing["source_files"],
		incoming["source_files"],
	)

	for examKey, incomingExam := range incomingExams {
		effectiveExam, shouldMerge := incomingExamForMerge(
			examKey,
			incomingExam,
			exactGroups,
		)
		if !shouldMerge {
			continue
		}

		existingExams[examKey] = mergeExamPayload(
			existingExams[examKey],
			effectiveExam,
		)
	}

	merged["exams"] = existingExams

	// Cases legados podem carregar aliases contraditórios produzidos
	// pelo modelo. Reaplicamos a identidade canônica ao resultado final.
	normalizePatientIdentityFields(merged)

	// exam.source deve apontar somente para a evidência clínica atual,
	// em ordem determinística OD -> OS -> AO.
	rebuildExamSources(merged)

	return merged
}

func mergeExamPayload(existingRaw, incomingRaw any) any {
	incoming, incomingOK := incomingRaw.(map[string]any)
	if !incomingOK || incoming == nil {
		return cloneValue(incomingRaw)
	}

	existing, existingOK := existingRaw.(map[string]any)
	if !existingOK || existing == nil {
		return cloneMap(incoming)
	}

	incomingEyes, hasIncomingEyes := incoming["eyes"].(map[string]any)

	// Exames sem granularidade por olho são tratados como uma unidade.
	if !hasIncomingEyes {
		return cloneMap(incoming)
	}

	// Exames com eyes:
	// - olhos ausentes no upload novo permanecem;
	// - mesmo exame + mesmo olho é substituído integralmente.
	result := cloneMap(existing)

	for key, value := range incoming {
		if key == "eyes" || key == "source" {
			continue
		}
		result[key] = cloneValue(value)
	}

	// Fallback. rebuildExamSources substituirá isto quando houver
	// source_files devidamente classificados para este exame.
	if source, exists := incoming["source"]; exists {
		result["source"] = cloneValue(source)
	}

	targetEyes, _ := result["eyes"].(map[string]any)
	if targetEyes == nil {
		targetEyes = map[string]any{}
	}

	for eye, payload := range incomingEyes {
		targetEyes[eye] = cloneValue(payload)
	}

	result["eyes"] = targetEyes
	return result
}

func mergeMapPreferIncoming(target, incoming map[string]any) {
	for key, incomingValue := range incoming {
		if incomingValue == nil {
			continue
		}

		incomingMap, incomingIsMap := incomingValue.(map[string]any)
		targetMap, targetIsMap := target[key].(map[string]any)

		if incomingIsMap && targetIsMap {
			mergeMapPreferIncoming(targetMap, incomingMap)
			continue
		}

		target[key] = cloneValue(incomingValue)
	}
}

func mergeSourceFiles(existingRaw, incomingRaw any) []any {
	existing := sourceFileSlice(existingRaw)
	incoming := sourceFileSlice(incomingRaw)

	// Um upload pode conter vários arquivos do mesmo exame+olho.
	// A substituição acontece por grupo inteiro, não arquivo por arquivo.
	replacementGroups := map[string]bool{}
	incomingHashes := map[string]map[string]bool{}

	for _, source := range incoming {
		group := sourceGroupKey(source)
		if group == "" {
			continue
		}

		replacementGroups[group] = true

		sha, _ := source["sha256"].(string)
		if sha != "" {
			if incomingHashes[group] == nil {
				incomingHashes[group] = map[string]bool{}
			}
			incomingHashes[group][sha] = true
		}
	}

	result := make([]any, 0, len(existing)+len(incoming))
	seen := map[string]bool{}

	appendSource := func(source map[string]any) {
		sha, _ := source["sha256"].(string)
		path, _ := source["path"].(string)

		dedupKey := ""
		if strings.TrimSpace(sha) != "" {
			dedupKey = "sha256:" + sha
		} else if strings.TrimSpace(path) != "" {
			dedupKey = "path:" + path
		}

		if dedupKey != "" && seen[dedupKey] {
			return
		}

		if dedupKey != "" {
			seen[dedupKey] = true
		}

		result = append(result, cloneValue(source))
	}

	for _, source := range existing {
		group := sourceGroupKey(source)

		if replacementGroups[group] {
			// Reenvio byte-a-byte do mesmo documento:
			// mantém a referência já persistida e descarta a cópia redundante.
			sha, _ := source["sha256"].(string)
			if sha != "" && incomingHashes[group][sha] {
				appendSource(source)
			}
			continue
		}

		appendSource(source)
	}

	for _, source := range incoming {
		appendSource(source)
	}

	return result
}

func exactReuploadGroups(existingRaw, incomingRaw any) map[string]bool {
	existing := sourceFileSlice(existingRaw)
	incoming := sourceFileSlice(incomingRaw)

	hashes := map[string]map[string]bool{}

	for _, source := range existing {
		group := sourceGroupKey(source)
		sha, _ := source["sha256"].(string)

		if group == "" || sha == "" {
			continue
		}

		if hashes[group] == nil {
			hashes[group] = map[string]bool{}
		}

		hashes[group][sha] = true
	}

	result := map[string]bool{}
	seenIncoming := map[string]bool{}

	for _, source := range incoming {
		group := sourceGroupKey(source)
		if group == "" {
			continue
		}

		if !seenIncoming[group] {
			result[group] = true
			seenIncoming[group] = true
		}

		sha, _ := source["sha256"].(string)
		if sha == "" || !hashes[group][sha] {
			result[group] = false
		}
	}

	return result
}

func incomingExamForMerge(
	examKey string,
	raw any,
	exactGroups map[string]bool,
) (any, bool) {
	exam, ok := raw.(map[string]any)
	if !ok || exam == nil {
		return raw, true
	}

	eyes, hasEyes := exam["eyes"].(map[string]any)
	if !hasEyes {
		if exactGroups[examKey+"|"] {
			return nil, false
		}
		return raw, true
	}

	filtered := cloneMap(exam)
	filteredEyes := map[string]any{}

	for eye, payload := range eyes {
		if exactGroups[examKey+"|"+eye] {
			continue
		}

		filteredEyes[eye] = cloneValue(payload)
	}

	if len(filteredEyes) == 0 {
		return nil, false
	}

	filtered["eyes"] = filteredEyes
	return filtered, true
}

func sourceFileSlice(raw any) []map[string]any {
	items, _ := raw.([]any)
	result := make([]map[string]any, 0, len(items))

	for _, rawSource := range items {
		source, ok := rawSource.(map[string]any)
		if ok && source != nil {
			result = append(result, source)
		}
	}

	return result
}

func sourceExamName(source map[string]any) string {
	switch exam := source["exam"].(type) {
	case string:
		return strings.TrimSpace(exam)

	case []any:
		// O extrator real pode devolver:
		//   "exam": ["pentacam_corneal_tomography"]
		//
		// Só colapsamos automaticamente quando existe exatamente
		// um tipo de exame. Arquivos multi-exame ficam conservadores.
		if len(exam) != 1 {
			return ""
		}

		value, _ := exam[0].(string)
		return strings.TrimSpace(value)

	case []string:
		if len(exam) != 1 {
			return ""
		}

		return strings.TrimSpace(exam[0])
	}

	return ""
}

func sourceHasExam(source map[string]any, expected string) bool {
	switch exam := source["exam"].(type) {
	case string:
		return strings.TrimSpace(exam) == expected

	case []any:
		for _, raw := range exam {
			value, _ := raw.(string)
			if strings.TrimSpace(value) == expected {
				return true
			}
		}

	case []string:
		for _, value := range exam {
			if strings.TrimSpace(value) == expected {
				return true
			}
		}
	}

	return false
}

func sourceGroupKey(source map[string]any) string {
	exam := sourceExamName(source)
	eye, _ := source["eye"].(string)

	eye = strings.TrimSpace(eye)

	if exam == "" {
		return ""
	}

	return exam + "|" + eye
}

func rebuildExamSources(analysis map[string]any) {
	exams, _ := analysis["exams"].(map[string]any)
	if exams == nil {
		return
	}

	sources := sourceFileSlice(analysis["source_files"])

	for examKey, rawExam := range exams {
		exam, ok := rawExam.(map[string]any)
		if !ok || exam == nil {
			continue
		}

		current := make([]any, 0)
		seen := map[string]bool{}

		appendEye := func(expectedEye string) {
			for _, source := range sources {
				sourceEye, _ := source["eye"].(string)

				if !sourceHasExam(source, examKey) || sourceEye != expectedEye {
					continue
				}

				path, _ := source["path"].(string)
				if path == "" || seen[path] {
					continue
				}

				seen[path] = true
				current = append(current, path)
			}
		}

		// Essa ordem é importante porque o frontend hoje usa, por exemplo,
		// pentacam.source[0] para OD e pentacam.source[1] para OS.
		for _, eye := range []string{"OD", "OS", "AO", ""} {
			appendEye(eye)
		}

		// Preserva qualquer lateralidade futura/desconhecida sem misturá-la
		// com as posições conhecidas acima.
		for _, source := range sources {
			sourceEye, _ := source["eye"].(string)

			if !sourceHasExam(source, examKey) ||
				sourceEye == "OD" ||
				sourceEye == "OS" ||
				sourceEye == "AO" ||
				sourceEye == "" {
				continue
			}

			path, _ := source["path"].(string)
			if path == "" || seen[path] {
				continue
			}

			seen[path] = true
			current = append(current, path)
		}

		if len(current) > 0 {
			exam["source"] = current
		}
	}
}

func mergeStringLists(existingRaw, incomingRaw any) []any {
	result := make([]any, 0)
	seen := map[string]bool{}

	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}

	for _, raw := range []any{existingRaw, incomingRaw} {
		switch values := raw.(type) {
		case []any:
			for _, item := range values {
				if value, ok := item.(string); ok {
					appendValue(value)
				}
			}
		case []string:
			for _, value := range values {
				appendValue(value)
			}
		}
	}

	return result
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}

	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil || cloned == nil {
		return map[string]any{}
	}

	return cloned
}

func cloneValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}

	var cloned any
	if json.Unmarshal(raw, &cloned) != nil {
		return value
	}

	return cloned
}

func referencedSourcePaths(analysis map[string]any) map[string]bool {
	result := map[string]bool{}

	files, _ := analysis["source_files"].([]any)
	for _, raw := range files {
		source, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		path, _ := source["path"].(string)
		if path != "" {
			result[path] = true
		}
	}

	return result
}

func confirmationKey(intakeID string) string {
	return "intake-confirmations/" + intakeID + ".json"
}

func loadConfirmationReceipt(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	intakeID string,
) (confirmationReceipt, bool) {
	object, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(confirmationKey(intakeID)),
	})
	if err != nil {
		return confirmationReceipt{}, false
	}
	defer object.Body.Close()

	var receipt confirmationReceipt
	if json.NewDecoder(io.LimitReader(object.Body, 64<<10)).Decode(&receipt) != nil {
		return confirmationReceipt{}, false
	}

	if receipt.CaseID == "" || receipt.AnalysisKey == "" {
		return confirmationReceipt{}, false
	}

	return receipt, true
}

func storeConfirmationReceipt(
	ctx context.Context,
	client *s3.Client,
	bucket string,
	intakeID string,
	receipt confirmationReceipt,
) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(confirmationKey(intakeID)),
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	})

	return err
}

type patientChangeRow struct {
	Exam   string `json:"exam"`
	Eye    string `json:"eye,omitempty"`
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
	Kind   string `json:"kind"`
}

type patientChangePreview struct {
	Added     int                `json:"added"`
	Changed   int                `json:"changed"`
	Removed   int                `json:"removed"`
	Unchanged int                `json:"unchanged"`
	Rows      []patientChangeRow `json:"rows"`
}

func buildPatientChangePreview(existing, incoming map[string]any) patientChangePreview {
	preview := patientChangePreview{Rows: []patientChangeRow{}}
	existingExams, _ := existing["exams"].(map[string]any)
	incomingExams, _ := incoming["exams"].(map[string]any)
	exactGroups := exactReuploadGroups(
		existing["source_files"],
		incoming["source_files"],
	)

	for examKey, incomingRaw := range incomingExams {
		effectiveRaw, include := incomingExamForMerge(
			examKey,
			incomingRaw,
			exactGroups,
		)
		if !include {
			continue
		}

		incomingExam, ok := effectiveRaw.(map[string]any)
		if !ok {
			continue
		}

		existingExam, _ := existingExams[examKey].(map[string]any)
		incomingEyes, hasEyes := incomingExam["eyes"].(map[string]any)

		if !hasEyes {
			appendChangeRows(&preview, examKey, "", existingExam, incomingExam)
			continue
		}

		existingEyes, _ := existingExam["eyes"].(map[string]any)
		for eye, incomingEye := range incomingEyes {
			appendChangeRows(&preview, examKey, eye, existingEyes[eye], incomingEye)
		}
	}

	sort.Slice(preview.Rows, func(i, j int) bool {
		a, b := preview.Rows[i], preview.Rows[j]
		if a.Exam != b.Exam {
			return a.Exam < b.Exam
		}
		if a.Eye != b.Eye {
			return eyeRank(a.Eye) < eyeRank(b.Eye)
		}
		return a.Field < b.Field
	})

	return preview
}

func appendChangeRows(preview *patientChangePreview, exam, eye string, before, after any) {
	beforeFields := map[string]any{}
	afterFields := map[string]any{}
	flattenPreviewFields("", before, beforeFields)
	flattenPreviewFields("", after, afterFields)

	keys := map[string]bool{}
	for key := range beforeFields {
		keys[key] = true
	}
	for key := range afterFields {
		keys[key] = true
	}

	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)

	for _, field := range names {
		oldValue, hadOld := beforeFields[field]
		newValue, hasNew := afterFields[field]

		kind := "unchanged"
		switch {
		case !hadOld:
			kind = "added"
			preview.Added++
		case !hasNew:
			kind = "removed"
			preview.Removed++
		case !reflect.DeepEqual(oldValue, newValue):
			kind = "changed"
			preview.Changed++
		default:
			preview.Unchanged++
		}

		preview.Rows = append(preview.Rows, patientChangeRow{
			Exam: exam, Eye: eye, Field: field,
			Before: oldValue, After: newValue, Kind: kind,
		})
	}
}

func flattenPreviewFields(prefix string, value any, result map[string]any) {
	if prefix == "" && value == nil {
		return
	}

	object, ok := value.(map[string]any)
	if !ok {
		if prefix == "" {
			prefix = "value"
		}
		result[prefix] = cloneValue(value)
		return
	}

	for key, child := range object {
		if key == "source" {
			continue
		}

		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		flattenPreviewFields(path, child, result)
	}
}

func eyeRank(eye string) int {
	switch eye {
	case "OD":
		return 0
	case "OS":
		return 1
	case "AO":
		return 2
	default:
		return 3
	}
}
