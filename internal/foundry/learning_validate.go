// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var learningNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func chunkLearningEvidence(events []learningEvent, limit int) ([]learningChunk, []map[string]any, error) {
	chunks := make([]learningChunk, 0)
	current := make([]map[string]any, 0)
	prompt := func(index int, fragments []map[string]any) string {
		return learningChunkSystem + "\nEVIDENCIA_JSON\n" + mustJSON(map[string]any{"chunk_index": index, "fragments": fragments})
	}
	flush := func() {
		if len(current) == 0 {
			return
		}
		copyFragments := append([]map[string]any(nil), current...)
		chunks = append(chunks, learningChunk{Prompt: prompt(len(chunks), copyFragments), Fragments: copyFragments})
		current = make([]map[string]any, 0)
	}
	for _, event := range events {
		metadata := make(map[string]any, len(event.Value))
		for key, value := range event.Value {
			if key != "text" {
				metadata[key] = value
			}
		}
		parts := []struct {
			name, key string
			value     []rune
		}{{"metadata", "data", []rune(mustJSON(metadata))}, {"text", "text", []rune(event.Text)}}
		for _, part := range parts {
			position := 0
			emitted := false
			for position < len(part.value) || !emitted {
				makeFragment := func(end int) map[string]any {
					return map[string]any{"event_index": event.Index, "session_event_index": event.Index, "part": part.name, "start": position, "end": end, "total_chars": len(part.value), part.key: string(part.value[position:end])}
				}
				low, high := position, len(part.value)
				for low < high {
					middle := (low + high + 1) / 2
					candidate := append(append([]map[string]any(nil), current...), makeFragment(middle))
					if utf8.RuneCountInString(prompt(len(chunks), candidate)) <= limit {
						low = middle
					} else {
						high = middle - 1
					}
				}
				candidate := append(append([]map[string]any(nil), current...), makeFragment(low))
				if low == position && position < len(part.value) || utf8.RuneCountInString(prompt(len(chunks), candidate)) > limit {
					if len(current) > 0 {
						flush()
						continue
					}
					return nil, nil, errors.New("max_input_chars no permite ni un fragmento de evidencia con su procedencia.")
				}
				current = append(current, makeFragment(low))
				position = low
				emitted = true
				if position < len(part.value) {
					flush()
				}
			}
		}
	}
	flush()
	return chunks, learningEventReceipts(events), nil
}

func learningEventReceipts(events []learningEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		metadata := make(map[string]any, len(event.Value))
		for key, value := range event.Value {
			if key != "text" {
				metadata[key] = value
			}
		}
		metadataText := mustJSON(metadata)
		out = append(out, map[string]any{"event_index": event.Index, "session_event_index": event.Index, "text_chars": utf8.RuneCountInString(event.Text), "metadata_chars": utf8.RuneCountInString(metadataText), "text_hash": hashShort([]byte(event.Text), 64), "metadata_hash": hashShort([]byte(metadataText), 64)})
	}
	return out
}

func learningFragmentReceipts(fragments []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(fragments))
	for _, f := range fragments {
		data, _ := f["text"].(string)
		if data == "" {
			data, _ = f["data"].(string)
		}
		out = append(out, map[string]any{"event_index": f["event_index"], "session_event_index": f["session_event_index"], "part": f["part"], "start": f["start"], "end": f["end"], "total_chars": f["total_chars"], "fragment_hash": hashShort([]byte(data), 64)})
	}
	return out
}

func validateLearningObservations(value any, events []learningEvent, fragments []map[string]any) ([]learningObservation, error) {
	root, ok := value.(map[string]any)
	if !ok || len(root) != 1 {
		return nil, errors.New("Respuesta de observaciones inválida.")
	}
	raw, ok := root["observations"].([]any)
	if !ok || len(raw) > 32 {
		return nil, errors.New("Respuesta de observaciones inválida.")
	}
	byIndex := map[int]learningEvent{}
	for _, event := range events {
		byIndex[event.Index] = event
	}
	out := make([]learningObservation, 0, len(raw))
	for _, item := range raw {
		object, ok := item.(map[string]any)
		if !ok || len(object) != 2 {
			return nil, errors.New("Observación inválida.")
		}
		text, ok := object["observation"].(string)
		if !ok || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 4000 {
			return nil, errors.New("Observación sin evidencia o demasiado extensa.")
		}
		rawRefs, ok := object["evidence"].([]any)
		if !ok || len(rawRefs) == 0 || len(rawRefs) > 8 {
			return nil, errors.New("Observación sin evidencia o demasiado extensa.")
		}
		obs := learningObservation{Text: text, Evidence: make([]TaskCitation, 0, len(rawRefs))}
		for _, rawRef := range rawRefs {
			ref, ok := rawRef.(map[string]any)
			if !ok || len(ref) != 2 {
				return nil, errors.New("Referencia de observación inválida.")
			}
			index, ok := learningEventIndex(ref["event_index"])
			quote, qok := ref["quote"].(string)
			event, exists := byIndex[index]
			if !ok || !qok || !exists || strings.TrimSpace(quote) == "" || utf8.RuneCountInString(quote) > 2000 || !strings.Contains(event.Text, quote) {
				return nil, errors.New("La cita de observación no corresponde al evento original.")
			}
			inFragment := false
			for _, fragment := range fragments {
				if fragment["part"] != "text" {
					continue
				}
				idx, valid := learningEventIndex(fragment["event_index"])
				if valid && idx == index {
					fragmentText, _ := fragment["text"].(string)
					if strings.Contains(fragmentText, quote) {
						inFragment = true
						break
					}
				}
			}
			if !inFragment {
				return nil, errors.New("La cita no estaba en el fragmento verificado.")
			}
			obs.Evidence = append(obs.Evidence, TaskCitation{EventIndex: index, Quote: quote})
		}
		out = append(out, obs)
	}
	return out, nil
}

func observationsToMaps(observations []learningObservation) []map[string]any {
	out := make([]map[string]any, 0, len(observations))
	for _, obs := range observations {
		refs := make([]map[string]any, 0, len(obs.Evidence))
		for _, ref := range obs.Evidence {
			refs = append(refs, map[string]any{"event_index": ref.EventIndex, "quote": ref.Quote})
		}
		out = append(out, map[string]any{"observation": obs.Text, "evidence": refs})
	}
	return out
}
func observationReferenceSet(observations []learningObservation) map[string]bool {
	out := map[string]bool{}
	for _, obs := range observations {
		for _, ref := range obs.Evidence {
			out[strconv.Itoa(ref.EventIndex)+"\x00"+ref.Quote] = true
		}
	}
	return out
}
func learningEventIndex(value any) (int, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil && n >= 0 && n <= int64(^uint(0)>>1)
	case int:
		return v, v >= 0
	case int64:
		return int(v), v >= 0
	case float64:
		if v >= 0 && v == float64(int64(v)) {
			return int(v), true
		}
	}
	return 0, false
}

func validateLearningSpec(value any, events []learningEvent, existing map[string]map[string]any) (map[string]any, error) {
	spec, ok := value.(map[string]any)
	if !ok || len(spec) != 10 {
		return nil, errors.New("La propuesta no cumple el contrato de campos.")
	}
	for _, key := range []string{"name", "description", "kind", "scope", "domain", "positive", "preventive", "evidence", "replaces", "rationale"} {
		if _, exists := spec[key]; !exists {
			return nil, errors.New("La propuesta no cumple el contrato de campos.")
		}
	}
	name, ok := spec["name"].(string)
	if !ok || !learningNamePattern.MatchString(name) {
		return nil, errors.New("Nombre de skill inválido.")
	}
	for _, key := range []string{"description", "domain", "rationale"} {
		text, ok := spec[key].(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, errors.New("Falta " + key)
		}
		clean, apiErr := cleanUserText(text, 100000)
		if apiErr != nil {
			return nil, errors.New("El contenido de " + key + " excede los límites de redacción.")
		}
		spec[key] = clean
	}
	kind, _ := spec["kind"].(string)
	scope, _ := spec["scope"].(string)
	if (kind != "skill" && kind != "rule" && kind != "tool") || (scope != "project" && scope != "domain" && scope != "general") {
		return nil, errors.New("Tipo o alcance inválido.")
	}
	byIndex := map[int]learningEvent{}
	for _, event := range events {
		byIndex[event.Index] = event
	}
	refs, ok := spec["evidence"].([]any)
	if !ok || len(refs) == 0 {
		return nil, errors.New("La propuesta no incluye evidencia.")
	}
	for _, raw := range refs {
		ref, ok := raw.(map[string]any)
		if !ok || len(ref) != 2 {
			return nil, errors.New("Referencia de evidencia inválida.")
		}
		index, valid := learningEventIndex(ref["event_index"])
		quote, qok := ref["quote"].(string)
		event, exists := byIndex[index]
		if !valid || !qok || !exists || strings.TrimSpace(quote) == "" || !strings.Contains(event.Text, quote) {
			return nil, errors.New("La cita no corresponde al evento original.")
		}
	}
	replaces, ok := spec["replaces"].([]any)
	if !ok {
		return nil, errors.New("Sustitución de capacidad desconocida.")
	}
	seenReplace := map[string]bool{}
	for _, raw := range replaces {
		id, ok := raw.(string)
		if !ok || existing[id] == nil {
			return nil, errors.New("Sustitución de capacidad desconocida.")
		}
		if seenReplace[id] {
			return nil, errors.New("Sustitución duplicada.")
		}
		seenReplace[id] = true
	}
	for _, key := range []string{"positive", "preventive"} {
		variant, ok := spec[key].(map[string]any)
		if !ok || len(variant) != 2 {
			return nil, errors.New("Variante inválida.")
		}
		instructions, ok := variant["instructions"].(string)
		if !ok || strings.TrimSpace(instructions) == "" || utf8.RuneCountInString(instructions) > 15000 {
			return nil, errors.New("Instrucciones vacías o demasiado extensas.")
		}
		clean, apiErr := cleanUserText(instructions, 15000)
		if apiErr != nil {
			return nil, errors.New("Las instrucciones exceden los límites de redacción.")
		}
		variant["instructions"] = clean
		files, ok := variant["files"].([]any)
		if !ok || len(files) > 12 {
			return nil, errors.New("Lista de archivos inválida o demasiado extensa.")
		}
		seen := map[string]bool{}
		total := 0
		for _, rawFile := range files {
			file, ok := rawFile.(map[string]any)
			if !ok || len(file) != 2 {
				return nil, errors.New("Contrato de archivo inválido.")
			}
			path, pok := file["path"].(string)
			content, cok := file["content"].(string)
			if !pok || !cok || !safeLearningPath(path) {
				return nil, errors.New("La ruta del paquete debe ser relativa, sin .. ni directorios ocultos.")
			}
			if seen[path] {
				return nil, errors.New("Archivo duplicado.")
			}
			seen[path] = true
			for _, r := range path {
				if r < 32 {
					return nil, errors.New("La ruta del archivo contiene controles.")
				}
			}
			cleanContent, apiErr := cleanUserText(content, 100000)
			if apiErr != nil {
				return nil, errors.New("El archivo supera el límite de redacción.")
			}
			file["content"] = cleanContent
			total += utf8.RuneCountInString(cleanContent)
		}
		if total > 100000 {
			return nil, errors.New("El paquete supera 100 KB.")
		}
		if kind == "skill" && !seen["SKILL.md"] {
			if len(files) >= 12 {
				return nil, errors.New("El límite de archivos no deja espacio para SKILL.md.")
			}
			generated := map[string]any{"path": "SKILL.md", "content": "---\nname: " + name + "\ndescription: " + strconv.Quote(spec["description"].(string)) + "\n---\n\n" + clean + "\n"}
			variant["files"] = append([]any{generated}, files...)
			seen["SKILL.md"] = true
		}
		if kind == "skill" {
			var front string
			for _, rawFile := range variant["files"].([]any) {
				file := rawFile.(map[string]any)
				if file["path"] == "SKILL.md" {
					front = file["content"].(string)
					break
				}
			}
			if err := validateSkillFrontmatter(front, name); err != nil {
				return nil, err
			}
		}
		if kind == "tool" {
			valid := false
			for _, rawFile := range files {
				file := rawFile.(map[string]any)
				if validLearningScript(file["path"].(string), file["content"].(string)) {
					valid = true
					break
				}
			}
			if !valid {
				return nil, errors.New("Una tool requiere un script real con código, no una comprobación vacía.")
			}
		}
	}
	return spec, nil
}

func safeLearningPath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func validateSkillFrontmatter(content, name string) error {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return errors.New("SKILL.md necesita frontmatter delimitado por ---. ")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return errors.New("El frontmatter de SKILL.md no está cerrado.")
	}
	fields := map[string]string{}
	pattern := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):\s*(.*)$`)
	for i := 1; i < end; {
		line := lines[i]
		i++
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimLeft(line, " "), "#") {
			continue
		}
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			return errors.New("Frontmatter de SKILL.md inválido.")
		}
		key, value := match[1], match[2]
		if key != "name" && key != "description" {
			continue
		}
		if _, ok := fields[key]; ok {
			return errors.New("Campo duplicado en SKILL.md.")
		}
		if value == "|" || value == ">" || value == "|-" || value == ">-" || value == "|+" || value == ">+" {
			parts := []string{}
			for i < end && (strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t")) {
				parts = append(parts, strings.TrimSpace(lines[i]))
				i++
			}
			value = strings.TrimSpace(strings.Join(parts, " "))
		} else if strings.HasPrefix(value, "\"") {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return errors.New("Usa texto entre comillas JSON válido en SKILL.md.")
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return errors.New("Comillas sin cerrar en SKILL.md.")
			}
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else if strings.Contains(value, ": ") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") || strings.HasPrefix(value, "&") || strings.HasPrefix(value, "*") || strings.HasPrefix(value, "!") || strings.EqualFold(value, "null") || value == "~" || strings.EqualFold(value, "true") || strings.EqualFold(value, "false") {
			return errors.New("name y description deben ser texto escalar.")
		} else if at := strings.Index(value, " #"); at >= 0 {
			value = strings.TrimSpace(value[:at])
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("Falta texto de " + key + " en SKILL.md.")
		}
		fields[key] = value
	}
	if fields["name"] != name || strings.TrimSpace(fields["description"]) == "" {
		return errors.New("SKILL.md necesita name coincidente y description no vacía.")
	}
	return nil
}

func validLearningScript(path, content string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".py" {
		python, err := exec.LookPath("python3")
		if err != nil {
			return false
		}
		cmd := exec.Command(python, "-I", "-S", "-c", "import ast,sys; ast.parse(sys.stdin.read())")
		cmd.Env = []string{"PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1"}
		cmd.Stdin = strings.NewReader(content)
		return cmd.Run() == nil
	}
	switch ext {
	case ".sh", ".bash", ".js", ".mjs", ".cjs", ".rb", ".pl", ".ps1":
	default:
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		return true
	}
	return false
}
