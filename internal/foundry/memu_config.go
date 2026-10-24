// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type memuExternalConfig struct {
	Path   string
	Values map[string]string
}

func (c memuExternalConfig) value(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.Values[name]); value != "" {
		return value
	}
	return fallback
}

func memuBinary() (string, bool, error) {
	value := strings.TrimSpace(os.Getenv("WORKBENCH_MEMU_BIN"))
	if value == "" {
		return "", false, errors.New("WORKBENCH_MEMU_BIN no está definido")
	}
	if !filepath.IsAbs(value) {
		return "", false, errors.New("WORKBENCH_MEMU_BIN debe ser una ruta absoluta")
	}
	info, err := os.Lstat(value)
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", false, errors.New("WORKBENCH_MEMU_BIN no es un ejecutable regular")
	}
	return value, true, nil
}

func requireMemuBinary() (string, *APIError) {
	binary, installed, err := memuBinary()
	if installed {
		return binary, nil
	}
	message := "memU no está instalado o WORKBENCH_MEMU_BIN no apunta a su ejecutable."
	if err != nil {
		message += " " + safeLearningError(err.Error(), 400)
	}
	return "", &APIError{Status: http.StatusConflict, Code: "memu_unavailable", Message: message}
}

func memuVersion(binary string) string {
	if binary == "" {
		return ""
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(binary), ".."))
	patterns := []string{
		filepath.Join(root, "lib", "python*", "site-packages", "memu_cli-*.dist-info", "METADATA"),
		filepath.Join(root, "lib64", "python*", "site-packages", "memu_cli-*.dist-info", "METADATA"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			data, err := os.ReadFile(match)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "Version: ") {
					return strings.TrimSpace(strings.TrimPrefix(line, "Version: "))
				}
			}
		}
	}
	return ""
}

func loadMemuConfiguration() (memuExternalConfig, error) {
	path := strings.TrimSpace(os.Getenv("MEMU_CONFIG_ENV"))
	if path == "" {
		home := strings.TrimSpace(os.Getenv("HOME"))
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return memuExternalConfig{}, err
			}
		}
		path = filepath.Join(home, ".memu", "config.env")
	}
	if strings.HasPrefix(path, "~/") {
		home := strings.TrimSpace(os.Getenv("HOME"))
		if home == "" {
			return memuExternalConfig{}, errors.New("HOME no está definido")
		}
		path = filepath.Join(home, path[2:])
	} else if path == "~" {
		path = strings.TrimSpace(os.Getenv("HOME"))
	}
	path = filepath.Clean(path)
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	config := memuExternalConfig{Path: path, Values: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			config.Values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return config, nil
}

// memuChildEnvironment is the allowlisted environment of the memU CLI: the basics it
// needs to run plus its own MEMU_* settings (config file first, process env wins, as in
// memuExternalConfig.value). Nothing else of the dashboard's environment leaks to it.
func memuChildEnvironment() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/bin:/bin"
	}
	env := []string{"PATH=" + path}
	for _, key := range []string{"HOME", "USER", "TMPDIR", "TZ", "LANG", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, entry := range os.Environ() {
		if key, _, ok := strings.Cut(entry, "="); ok && strings.HasPrefix(key, "LC_") {
			env = append(env, entry)
		}
	}
	values := map[string]string{}
	if config, err := loadMemuConfiguration(); err == nil {
		for key, value := range config.Values {
			if strings.HasPrefix(key, "MEMU_") {
				values[key] = value
			}
		}
	}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.HasPrefix(key, "MEMU_") && value != "" {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return memuCLIEnvironment(env)
}

func memuCLIEnvironment(env []string) []string {
	if strings.TrimSpace(os.Getenv("MEMU_CONFIG_ENV")) == "" {
		return env
	}
	config, err := loadMemuConfiguration()
	if err != nil {
		return env
	}
	return replaceCommandEnv(env, "MEMU_CONFIG_ENV", config.Path)
}

func requireMemuConfiguration() *APIError {
	config, err := loadMemuConfiguration()
	if err != nil {
		return &APIError{Status: http.StatusConflict, Code: "memu_config_missing", Message: "No se pudo leer la configuración de memU en " + config.Path + ": " + safeLearningError(err.Error(), 400)}
	}
	mode := strings.ToLower(config.value("MEMU_MEMORY_MODE", "local"))
	switch mode {
	case "cloud":
		if config.value("MEMU_CLOUD_API_KEY", "") == "" {
			return &APIError{Status: http.StatusConflict, Code: "memu_config_missing", Message: "Falta MEMU_CLOUD_API_KEY en " + config.Path + " o en el entorno."}
		}
	case "local":
		if config.value("MEMU_DB", "") == "" {
			return &APIError{Status: http.StatusConflict, Code: "memu_config_missing", Message: "Falta MEMU_DB en " + config.Path + " o en el entorno."}
		}
	default:
		return &APIError{Status: http.StatusConflict, Code: "memu_config_missing", Message: "MEMU_MEMORY_MODE debe ser local o cloud en " + config.Path + "."}
	}
	return nil
}

func replaceCommandEnv(env []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(env)+1)
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !found {
				result = append(result, prefix+value)
				found = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !found {
		result = append(result, prefix+value)
	}
	return result
}
