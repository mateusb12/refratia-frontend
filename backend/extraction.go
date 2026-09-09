package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const extractionPrompt = `Extraia e consolide TODOS os dados alfanuméricos legíveis dos documentos oftalmológicos enviados em um único JSON. Não faça diagnóstico, não invente valores e use null quando um dado não estiver visível.

Organize o resultado como paciente_compilado.json:
- schema_version, generated_on (AAAA-MM-DD) e language (pt-BR);
- patient: full_name, birth_date normalizada e formatos conflitantes encontrados;
- facility: nome, descrição, endereço e telefone quando existirem;
- conventions: OD, OS, AO, significado de null e separador decimal normalizado;
- source_files: um item por arquivo com path igual ao nome recebido, exam, eye, páginas/dimensões e conteúdo por página quando identificável;
- exams: use exatamente as chaves fundus_retinography, refractometry, iol_calculation, oct_retina, pentacam_corneal_tomography e specular_microscopy quando aplicáveis. Inclua SOMENTE exames realmente evidenciados pelos arquivos; não crie chaves para exames ausentes. Cada exame deve ter source com os nomes dos arquivos correspondentes. Preserve aparelho, software, data/hora, qualidade, alertas, fonte e TODOS os campos, índices, medições, eixos, tabelas e cálculos legíveis. Separe olhos em eyes.OD e eyes.OS (ou AO quando realmente conjunto). Use nomes de campos técnicos em snake_case e inclua unidades no nome quando isso remover ambiguidade; não substitua a hierarquia específica do equipamento por um modelo genérico;
- extraction_notes: method, scope, not_encoded e clinical_use_warning. Esta chave fica no nível raiz, irmã de "exams", nunca dentro de "exams";
- verificacao_identidade: uma entrada por documento, com nome/nascimento/timestamp lidos, confiança e método da comparação. Divergência de identidade deve ser explícita e nunca omitida.

Contrato mínimo de campos por exame (não invente valores; quando não estiver no arquivo, registre como ausente):
- pentacam_corneal_tomography: K1, K2, Km, astigmatismo corneano anterior, paquimetria do ponto mais fino, BAD-D, ARTmax, ISV, IVA, IHA, KI, CKI, TKC, coma Z31 zona 5 mm, ACD e Z40 zona 6 mm;
- refractometry: refração por olho, com esfera, cilindro e eixo. Preserve sinais. Se o laudo de refratometria claramente omitir cilindro/eixo, registre cylinder_d como 0 e axis_deg como null conforme a convenção clínica do protocolo; se houver dúvida de leitura, use null e não infira;
- iol_calculation: comprimento axial, K1, K2, Km, astigmatismo e eixo da biometria, ACD, espessura do cristalino, white-to-white e refração alvo;
- specular_microscopy: contagem/densidade endotelial;
- fundus_retinography: ID do paciente, data/hora e achados/observações da imagem.
- oct_retina: identificação, data/hora e achados/observações do OCT; é informativo e exclusivo do Fluxo C.
Não inclua um exame no objeto "exams" apenas porque ele é esperado pelo protocolo: inclua somente exames evidenciados pelos arquivos enviados.

Confronte identidade, datas e lateralidade entre os arquivos. Preserve avisos do equipamento e divergências do documento. Não resuma tabelas nem omita linhas repetidas por modelo de lente. Retorne somente um objeto JSON.`

const pentacamRepairPrompt = `Analise somente as imagens dos PDFs do Pentacam enviados. Cada PDF pode ter 9 páginas; examine todas as páginas e associe cada valor ao arquivo/lateralidade correta. Esta é uma segunda leitura focada nos campos que frequentemente ficam em tabelas ou imagens pequenas.

Retorne somente este objeto JSON, mantendo null apenas quando o valor realmente não estiver legível em nenhuma página:
{"eyes":{"OD":{"anterior_cornea":{"k1_d":null,"k2_d":null,"km_d":null,"astigmatism_d":null,"axis_deg":null,"kmax_d":null},"pachymetry":{"thinnest_um":null},"belin_ambrosio":{"d":null,"art_max":null},"topometric_indices_8mm":{"isv":null,"iva":null,"iha":null,"ki":null,"cki":null,"tkc":"—"},"corneal_rings":{"zernike":{"5mm":{"z31_coma":null},"6mm":{"z31_coma":null}}},"anterior_segment":{"internal_anterior_chamber_depth_mm":null},"cataract_preop":{"total_corneal_z40_6mm_um":null}},"OS":{"anterior_cornea":{"k1_d":null,"k2_d":null,"km_d":null,"astigmatism_d":null,"axis_deg":null,"kmax_d":null},"pachymetry":{"thinnest_um":null},"belin_ambrosio":{"d":null,"art_max":null},"topometric_indices_8mm":{"isv":null,"iva":null,"iha":null,"ki":null,"cki":null,"tkc":"—"},"corneal_rings":{"zernike":{"5mm":{"z31_coma":null},"6mm":{"z31_coma":null}}},"anterior_segment":{"internal_anterior_chamber_depth_mm":null},"cataract_preop":{"total_corneal_z40_6mm_um":null}}}}

Use a lateralidade impressa no documento. Não misture OD e OS. Em "Ectasia Reforçada Belin / Ambrósio", leia D no rodapé direito e ARTmax no quadro "Índice de Progressão"; não confunda ARTmax com Mín, Méd ou Máx. Em "Topométrico / Estadiamento KC", leia ISV, IVA, IHA, KI, CKI e TKC. Em "Paquimétrico", leia a espessura do ponto mais fino. Em "Anéis Corneanos", leia Z31 coma nas zonas 5 mm e 6 mm. Em "Cataract Pre-OP", leia Z40 na zona 6 mm. Se TKC estiver visivelmente impresso como traço, retorne "—" para registrar que o campo foi localizado, mas não há classificação; use null somente se o campo não puder ser localizado.`

const iolRepairPrompt = `Analise somente as imagens do PDF de biometria/cálculo de LIO enviado. Examine todas as páginas, principalmente a página que contém os blocos OD e OS no topo. Extraia os valores impressos separadamente para cada olho. Não use valores do Pentacam nem faça cálculos.

Retorne somente este objeto JSON, usando null apenas quando o campo não estiver legível:
{"eyes":{"OD":{"axial_length_mm":null,"keratometry":{"k1_d":null,"k1_axis_deg":null,"k2_d":null,"k2_axis_deg":null,"mean_k_d":null,"astigmatism_d":null,"astigmatism_axis_deg":null},"anterior_chamber_depth_mm":null,"aqueous_depth_mm":null,"lens_thickness_mm":null,"white_to_white_mm":null,"target_refraction_d":null},"OS":{"axial_length_mm":null,"keratometry":{"k1_d":null,"k1_axis_deg":null,"k2_d":null,"k2_axis_deg":null,"mean_k_d":null,"astigmatism_d":null,"astigmatism_axis_deg":null},"anterior_chamber_depth_mm":null,"aqueous_depth_mm":null,"lens_thickness_mm":null,"white_to_white_mm":null,"target_refraction_d":null}}}

No documento EyeSuite, leia AL como comprimento axial, K1/K2/K como ceratometria, -AST como astigmatismo, ACD, LT, WTW e Target Refraction. Preserve sinais negativos, casas decimais e eixos. A refração alvo pode estar impressa uma vez para cada olho ou uma vez para o exame; nesse caso replique o mesmo valor nos dois olhos.`

type uploadedFile struct {
	Metadata intakeFile
	Data     []byte
}

type openAIResponse struct {
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func readIntakeFiles(headers []*multipart.FileHeader) ([]uploadedFile, error) {
	files := make([]uploadedFile, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		files = append(files, uploadedFile{
			Metadata: intakeFile{Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Size: header.Size, SHA256: hex.EncodeToString(digest[:])},
			Data:     data,
		})
	}
	return files, nil
}

func intakeMetadata(files []uploadedFile) []intakeFile {
	result := make([]intakeFile, len(files))
	for index, file := range files {
		result[index] = file.Metadata
	}
	return result
}

func extractPatient(ctx context.Context, files []uploadedFile) (map[string]any, error) {
	analysis := extractPatientLocal(ctx, files)
	gaps := collectLocalGaps(analysis, files)

	var prepared []preparedFile

	if len(gaps) > 0 {
		fallbackFiles := localFallbackFiles(analysis, files)

		var err error
		prepared, err = prepareExtractionFiles(ctx, fallbackFiles)
		if err != nil {
			return nil, err
		}

		output, err := requestOpenAIPreparedJSON(
			ctx,
			prepared,
			extractionPromptForLocalGaps(analysis, gaps),
			40000,
		)
		if err != nil {
			return nil, err
		}

		fallback, err := decodeAnalysis(output)
		if err != nil {
			return nil, err
		}

		resolved := localResolvedExamKeys(analysis)
		stripLocallyResolvedExams(fallback, resolved)
		mergeFallbackAnalysis(analysis, fallback)
	}

	// Pentacam usa somente o fallback local-first acima.
	// Não executar uma segunda chamada OpenAI para o mesmo exame.

	if repairFiles := iolFilesNeedingRepair(analysis, files); len(repairFiles) > 0 {
		if prepared == nil {
			var err error
			prepared, err = prepareExtractionFiles(ctx, files)
			if err != nil {
				return nil, err
			}
		}

		if repairOutput, repairErr := requestOpenAIPreparedJSON(
			ctx,
			prepareRepairFiles(repairFiles, prepared),
			iolRepairPrompt,
			5000,
		); repairErr == nil {
			var repair map[string]any
			if json.Unmarshal([]byte(repairOutput), &repair) == nil {
				mergeIOLRepair(analysis, repair)
			}
		}
	}

	enrichSourceFiles(analysis, files)
	return analysis, nil
}

func prepareRepairFiles(files []uploadedFile, prepared []preparedFile) []preparedFile {
	wanted := map[string]bool{}
	for _, file := range files {
		wanted[file.Metadata.Filename] = true
	}
	result := make([]preparedFile, 0)
	for _, file := range prepared {
		if wanted[file.File.Metadata.Filename] {
			result = append(result, file)
		}
	}
	return result
}

func requestOpenAIJSON(ctx context.Context, files []uploadedFile, prompt string, maxOutputTokens int) (string, error) {
	prepared := make([]preparedFile, 0, len(files))
	for _, file := range files {
		prepared = append(prepared, preparedFile{File: file})
	}
	return requestOpenAIPreparedJSON(ctx, prepared, prompt, maxOutputTokens)
}

func requestOpenAIPreparedJSON(ctx context.Context, files []preparedFile, prompt string, maxOutputTokens int) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("extração indisponível: OPENAI_API_KEY não configurada")
	}

	content := make([]map[string]any, 0, len(files)*2+1)
	content = append(content, map[string]any{"type": "input_text", "text": prompt})
	for _, prepared := range files {
		file := prepared.File
		content = append(content, map[string]any{"type": "input_text", "text": preparedPromptLabel(prepared)})
		if prepared.TextLayer != "" {
			content = append(content, map[string]any{"type": "input_text", "text": "Camada textual extraída deste documento:\n" + prepared.TextLayer})
			continue
		}
		dataURL := "data:" + file.Metadata.ContentType + ";base64," + base64.StdEncoding.EncodeToString(file.Data)
		if strings.HasPrefix(file.Metadata.ContentType, "image/") {
			content = append(content, map[string]any{"type": "input_image", "image_url": dataURL, "detail": "high"})
		} else {
			input := map[string]any{"type": "input_file", "filename": file.Metadata.Filename, "file_data": dataURL}
			if file.Metadata.ContentType == "application/pdf" {
				input["detail"] = "high"
			}
			content = append(content, input)
		}
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-5.4-mini"
	}
	body, err := json.Marshal(map[string]any{
		"model":             model,
		"store":             false,
		"max_output_tokens": maxOutputTokens,
		"input": []map[string]any{{
			"role":    "user",
			"content": content,
		}},
		"text": map[string]any{"format": map[string]any{"type": "json_object"}},
	})
	if err != nil {
		return "", fmt.Errorf("não foi possível preparar a extração: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("não foi possível preparar a extração: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return "", fmt.Errorf("falha ao processar os documentos: %w", err)
	}
	defer response.Body.Close()

	var result openAIResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 10<<20)).Decode(&result); err != nil {
		return "", errors.New("resposta inválida do serviço de extração")
	}
	if response.StatusCode >= 300 {
		if result.Error != nil && result.Error.Message != "" {
			return "", fmt.Errorf("falha na extração: %s", result.Error.Message)
		}
		return "", fmt.Errorf("falha na extração (HTTP %d)", response.StatusCode)
	}
	if result.Status != "completed" {
		return "", errors.New("a extração não foi concluída")
	}

	var output string
	for _, item := range result.Output {
		for _, part := range item.Content {
			if part.Refusal != "" {
				return "", fmt.Errorf("extração recusada: %s", part.Refusal)
			}
			if part.Type == "output_text" {
				output += part.Text
			}
		}
	}
	return output, nil
}

func pentacamFilesNeedingRepair(analysis map[string]any, files []uploadedFile) []uploadedFile {
	exam := pentacamExam(analysis)
	if exam == nil || !pentacamNeedsRepair(exam) {
		return nil
	}

	wanted := map[string]bool{}
	if sources, ok := exam["source"].([]any); ok {
		for _, source := range sources {
			wanted[filepath.Base(fmt.Sprint(source))] = true
		}
	}
	result := make([]uploadedFile, 0, len(wanted))
	for _, file := range files {
		if wanted[file.Metadata.Filename] {
			result = append(result, file)
		}
	}
	return result
}

func iolFilesNeedingRepair(analysis map[string]any, files []uploadedFile) []uploadedFile {
	exams, _ := analysis["exams"].(map[string]any)
	exam, _ := exams["iol_calculation"].(map[string]any)
	if exam == nil || !iolNeedsRepair(exam) {
		return nil
	}
	wanted := map[string]bool{}
	if sources, ok := exam["source"].([]any); ok {
		for _, source := range sources {
			wanted[filepath.Base(fmt.Sprint(source))] = true
		}
	}
	result := make([]uploadedFile, 0, len(wanted))
	for _, file := range files {
		if wanted[file.Metadata.Filename] {
			result = append(result, file)
		}
	}
	return result
}

func iolNeedsRepair(exam map[string]any) bool {
	eyes, _ := exam["eyes"].(map[string]any)
	for _, eyeName := range []string{"OD", "OS"} {
		eye, _ := eyes[eyeName].(map[string]any)
		if !hasNumberAtAnyPath(eye, []string{"axial_length_mm"}) ||
			!hasNumberAtAnyPath(eye, []string{"keratometry", "k1_d"}) ||
			!hasNumberAtAnyPath(eye, []string{"keratometry", "k2_d"}) ||
			!hasNumberAtAnyPath(eye, []string{"keratometry", "mean_k_d"}) ||
			!hasNumberAtAnyPath(eye, []string{"keratometry", "astigmatism_d"}) ||
			!hasNumberAtAnyPath(eye, []string{"keratometry", "astigmatism_axis_deg"}) ||
			(!hasNumberAtAnyPath(eye, []string{"anterior_chamber_depth_mm"}) && !hasNumberAtAnyPath(eye, []string{"aqueous_depth_mm"})) ||
			!hasNumberAtAnyPath(eye, []string{"lens_thickness_mm"}) ||
			!hasNumberAtAnyPath(eye, []string{"white_to_white_mm"}) ||
			!hasNumberAtAnyPath(eye, []string{"target_refraction_d"}) {
			return true
		}
	}
	return false
}

func mergeIOLRepair(analysis, repair map[string]any) {
	exams, _ := analysis["exams"].(map[string]any)
	target, _ := exams["iol_calculation"].(map[string]any)
	targetEyes, _ := target["eyes"].(map[string]any)
	repairEyes, _ := repair["eyes"].(map[string]any)
	if targetEyes == nil || repairEyes == nil {
		return
	}
	for _, eyeName := range []string{"OD", "OS"} {
		targetEye, _ := targetEyes[eyeName].(map[string]any)
		repairEye, _ := repairEyes[eyeName].(map[string]any)
		if targetEye != nil && repairEye != nil {
			mergeMissingValues(targetEye, repairEye)
		}
	}
}

func pentacamExam(analysis map[string]any) map[string]any {
	exams, _ := analysis["exams"].(map[string]any)
	exam, _ := exams["pentacam_corneal_tomography"].(map[string]any)
	return exam
}

func pentacamNeedsRepair(exam map[string]any) bool {
	eyes, _ := exam["eyes"].(map[string]any)
	for _, eyeName := range []string{"OD", "OS"} {
		eye, _ := eyes[eyeName].(map[string]any)
		if !hasNumberAtAnyPath(eye,
			[]string{"pachymetry", "thinnest_um"},
			[]string{"pachymetry", "point_and_finest_um"},
			[]string{"general", "thinnest_pachy_um"},
			[]string{"general", "pachymetry_thinnest_um"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"anterior_cornea", "kmax_d"},
			[]string{"pachymetry", "k_max_anterior_diopters"},
			[]string{"general", "k_max_anterior_diopters"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"belin_ambrosio", "d"},
			[]string{"ectasia_reforcada_belin_ambrosio", "d"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"belin_ambrosio", "art_max"},
			[]string{"belin_ambrosio", "indice_de_progressao", "art_max"},
			[]string{"ectasia_reforcada_belin_ambrosio", "art_max"},
			[]string{"ectasia_reforcada_belin_ambrosio", "indice_de_progressao", "art_max"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"topometric_indices_8mm", "isv"},
			[]string{"indices_zona_8mm", "isv"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"topometric_indices_8mm", "iva"},
			[]string{"indices_zona_8mm", "iva"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"topometric_indices_8mm", "iha"},
			[]string{"indices_zona_8mm", "iha"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"topometric_indices_8mm", "ki"},
			[]string{"indices_zona_8mm", "ki"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"topometric_indices_8mm", "cki"},
			[]string{"indices_zona_8mm", "cki"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"corneal_rings", "zernike", "5mm", "z31_coma"},
			[]string{"anéis_corneanos", "total_corneal_wfa_components_of_zernike", "diam_zone_5_mm", "z31_coma_um"},
		) || !hasNumberAtAnyPath(eye,
			[]string{"cataract_preop", "total_corneal_z40_6mm_um"},
			[]string{"cataract_pre_op", "total_corneal_z40_6mm_um"},
		) {
			return true
		}
	}
	return false
}

func hasNumberAtAnyPath(root map[string]any, paths ...[]string) bool {
	for _, path := range paths {
		var value any = root
		for _, key := range path {
			object, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = object[key]
		}
		if _, ok := value.(float64); ok {
			return true
		}
	}
	return false
}

func mergePentacamRepair(analysis, repair map[string]any) {
	exam := pentacamExam(analysis)
	if exam == nil {
		return
	}
	targetEyes, _ := exam["eyes"].(map[string]any)
	repairEyes, _ := repair["eyes"].(map[string]any)
	if targetEyes == nil || repairEyes == nil {
		return
	}
	for _, eyeName := range []string{"OD", "OS"} {
		targetEye, _ := targetEyes[eyeName].(map[string]any)
		repairEye, _ := repairEyes[eyeName].(map[string]any)
		if targetEye != nil && repairEye != nil {
			mergeMissingValues(targetEye, repairEye)
		}
	}
}

func mergeMissingValues(target, repair map[string]any) {
	for key, value := range repair {
		if value == nil {
			continue
		}
		if repairObject, ok := value.(map[string]any); ok {
			targetObject, _ := target[key].(map[string]any)
			if targetObject == nil {
				targetObject = map[string]any{}
				target[key] = targetObject
			}
			mergeMissingValues(targetObject, repairObject)
			continue
		}
		if existing, ok := target[key]; !ok || existing == nil {
			target[key] = value
		}
	}
}

func decodeAnalysis(raw string) (map[string]any, error) {
	if raw == "" {
		return nil, errors.New("o serviço de extração não retornou um JSON válido")
	}
	var analysis map[string]any
	if err := json.Unmarshal([]byte(raw), &analysis); err != nil {
		return nil, fmt.Errorf(
			"o serviço de extração não retornou um JSON válido: %w",
			err,
		)
	}
	if analysis == nil {
		return nil, errors.New(
			"o serviço de extração não retornou um JSON válido: objeto JSON vazio",
		)
	}
	normalizeExtractionMetadata(analysis)
	normalizePatientIdentityFields(analysis)
	dropMalformedOptionalExams(analysis)
	normalized, err := json.Marshal(analysis)
	if err != nil {
		return nil, errors.New("o serviço de extração não retornou um JSON válido")
	}
	if err := validatePatientJSON(string(normalized)); err != nil {
		return nil, err
	}
	return analysis, nil
}

// Models sometimes place the envelope metadata inside exams despite the
// prompt. Move it back before validating the official exam keys.
func normalizeExtractionMetadata(analysis map[string]any) {
	exams, ok := analysis["exams"].(map[string]any)
	if !ok {
		return
	}

	for _, key := range []string{
		"extraction_notes",
		"verificacao_identidade",
	} {
		value, exists := exams[key]
		if !exists {
			continue
		}

		if _, alreadyAtRoot := analysis[key]; !alreadyAtRoot {
			analysis[key] = value
		}

		delete(exams, key)
	}
}

// A resposta de um exame isolado pode conter um payload vazio para outros
// exames. Exame ausente é válido; exame com payload inválido deve ser ignorado
// para não bloquear a análise do documento que realmente foi enviado.
func dropMalformedOptionalExams(analysis map[string]any) {
	exams, ok := analysis["exams"].(map[string]any)
	if !ok {
		return
	}
	for key, rawExam := range exams {
		if !officialExamKeys[key] {
			continue
		}
		exam, ok := rawExam.(map[string]any)
		if !ok {
			recordMalformedExam(analysis, key, "payload não é um objeto")
			delete(exams, key)
			continue
		}
		if _, ok := exam["source"].([]any); !ok {
			recordMalformedExam(analysis, key, "source ausente ou inválido")
			delete(exams, key)
		}
	}
}

func recordMalformedExam(analysis map[string]any, examKey, reason string) {
	notes, _ := analysis["extraction_notes"].(map[string]any)
	if notes == nil {
		notes = map[string]any{}
		analysis["extraction_notes"] = notes
	}
	items, _ := notes["invalid_exams"].([]any)
	notes["invalid_exams"] = append(items, map[string]any{"exam": examKey, "reason": reason})
}

func enrichSourceFiles(analysis map[string]any, files []uploadedFile) {
	extracted, _ := analysis["source_files"].([]any)
	sources := make([]any, 0, len(files))
	for index, file := range files {
		source := findSource(extracted, file.Metadata.Filename)
		source["index"] = index
		source["path"] = file.Metadata.Filename
		source["type"] = file.Metadata.ContentType
		source["size_bytes"] = file.Metadata.Size
		source["sha256"] = file.Metadata.SHA256
		sources = append(sources, source)
	}
	analysis["source_files"] = sources
	if _, ok := analysis["schema_version"]; !ok {
		analysis["schema_version"] = "1.0"
	}
	if _, ok := analysis["generated_on"]; !ok {
		analysis["generated_on"] = time.Now().Format("2006-01-02")
	}
	if _, ok := analysis["language"]; !ok {
		analysis["language"] = "pt-BR"
	}
}

func findSource(sources []any, filename string) map[string]any {
	for _, item := range sources {
		source, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := source["path"].(string)
		name, _ := source["filename"].(string)
		if filepath.Base(path) == filename || name == filename {
			return source
		}
	}
	return map[string]any{}
}

func validateStoredAnalysis(analysis map[string]any, files []intakeFile) error {
	sources, ok := analysis["source_files"].([]any)
	if !ok || len(sources) != len(files) {
		return errors.New("os arquivos não correspondem ao JSON analisado")
	}
	for index, file := range files {
		source, ok := sources[index].(map[string]any)
		if !ok || source["sha256"] != file.SHA256 || filepath.Base(fmt.Sprint(source["path"])) != file.Filename || !strings.HasPrefix(file.Key, "drafts/") {
			return errors.New("os arquivos não correspondem ao JSON analisado")
		}
	}
	return nil
}

func setStoredPaths(analysis map[string]any, files []intakeFile) {
	sources, _ := analysis["source_files"].([]any)
	for index, file := range files {
		if index >= len(sources) {
			return
		}
		if source, ok := sources[index].(map[string]any); ok {
			source["path"] = file.Key
		}
	}
}
