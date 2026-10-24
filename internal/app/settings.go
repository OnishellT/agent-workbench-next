package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"workbench/internal/settings"
)

type settingsPatch struct {
	projects *[]settings.Project
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	mediaTypes := r.Header.Values("Content-Type")
	if len(mediaTypes) != 1 {
		writeError(w, http.StatusUnsupportedMediaType, "application_json_required")
		return
	}
	mediaType, _, err := mime.ParseMediaType(mediaTypes[0])
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "application_json_required")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSettingsBody+1))
	if err != nil || len(data) > maxSettingsBody {
		writeError(w, http.StatusBadRequest, "invalid_settings")
		return
	}
	patch, err := parseSettingsPatch(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_settings")
		return
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	previous := s.settings.Snapshot()
	next := previous
	if patch.projects != nil {
		next.Projects = append([]settings.Project{}, (*patch.projects)...)
	}
	normalized, err := s.settings.Normalize(next)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_settings")
		return
	}
	next = normalized

	settingsChanged := !equalSettings(previous, next)
	if settingsChanged {
		if err := s.settings.Save(next); err != nil {
			writeError(w, http.StatusInternalServerError, "settings_save_failed")
			return
		}
	}
	s.writeBootstrap(w)
}

func parseSettingsPatch(data []byte) (settingsPatch, error) {
	fields, err := decodeObject(data)
	if err != nil {
		return settingsPatch{}, err
	}
	var patch settingsPatch
	for key, raw := range fields {
		switch key {
		case "projects":
			var values []settings.Project
			if err := decodeStrictJSON(raw, &values); err != nil || values == nil {
				return settingsPatch{}, errors.New("projects must be an array of path/name objects")
			}
			patch.projects = &values
		default:
			return settingsPatch{}, fmt.Errorf("unknown settings key %q", key)
		}
	}
	return patch, nil
}

func decodeObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("settings must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("settings contains an invalid key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate settings key %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("settings must contain one JSON object")
	}
	return fields, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func equalSettings(a, b settings.Values) bool {
	if a.Capture != b.Capture || len(a.Projects) != len(b.Projects) {
		return false
	}
	for i := range a.Projects {
		if a.Projects[i] != b.Projects[i] {
			return false
		}
	}
	return true
}
