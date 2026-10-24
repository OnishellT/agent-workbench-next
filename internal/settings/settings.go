package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	fileName     = "settings.json"
	maxFileLen   = 1 << 20
	maxProjects  = 100
	maxNameRunes = 120
)

type Project struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func (p *Project) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("project must be a JSON object")
	}
	for key := range fields {
		if key != "path" && key != "name" {
			return fmt.Errorf("unknown project field %q", key)
		}
	}
	pathRaw, ok := fields["path"]
	if !ok {
		return errors.New("project path is required")
	}
	var pathValue *string
	if err := json.Unmarshal(pathRaw, &pathValue); err != nil || pathValue == nil {
		return errors.New("project path must be a string")
	}
	project := Project{Path: *pathValue}
	if nameRaw, ok := fields["name"]; ok {
		var nameValue *string
		if err := json.Unmarshal(nameRaw, &nameValue); err != nil || nameValue == nil {
			return errors.New("project name must be a string")
		}
		project.Name = *nameValue
	}
	*p = project
	return nil
}

type Values struct {
	Capture  bool      `json:"capture"`
	Projects []Project `json:"projects"`
}

type Store struct {
	stateDir string
	mu       sync.Mutex
	values   Values
}

func Defaults() Values {
	return Values{Projects: []Project{}}
}

func Open(stateDir string) (*Store, error) {
	if err := validateStateDir(stateDir); err != nil {
		return nil, err
	}
	values, err := loadFile(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		values = Defaults()
		if _, err = writeFile(stateDir, values); err != nil {
			return nil, fmt.Errorf("initialize settings: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	return &Store{stateDir: stateDir, values: clone(values)}, nil
}

func (s *Store) Snapshot() Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.values)
}

func (s *Store) Save(values Values) error {
	values, err := s.Normalize(values)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := clone(s.values)
	renamed, err := writeFile(s.stateDir, values)
	if err != nil {
		if renamed {
			if _, rollbackErr := writeFile(s.stateDir, previous); rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("restore previous settings: %w", rollbackErr))
			}
		}
		return err
	}
	s.values = values
	return nil
}

// Normalize canonicalizes projects without knowing a state directory; the Store
// method of the same name also keeps projects out of Workbench's own state.
func Normalize(values Values) (Values, error) { return normalize(values, "") }

// Normalize canonicalizes projects and rejects any that would expose the state
// directory: the directory itself, its ancestors and anything inside it.
func (s *Store) Normalize(values Values) (Values, error) { return normalize(values, s.stateDir) }

func normalize(values Values, stateDir string) (Values, error) {
	if stateDir != "" {
		if resolved, err := filepath.EvalSymlinks(stateDir); err == nil {
			stateDir = resolved
		}
	}
	values = clone(values)
	if values.Projects == nil {
		return Values{}, errors.New("projects must be an array")
	}
	if len(values.Projects) > maxProjects {
		return Values{}, fmt.Errorf("projects may contain at most %d entries", maxProjects)
	}
	for i, project := range values.Projects {
		canonical, err := canonicalProjectPath(project.Path)
		if err != nil {
			return Values{}, err
		}
		if err := rejectBroadProject(canonical, stateDir); err != nil {
			return Values{}, err
		}
		name := project.Name
		if !utf8.ValidString(name) || hasControl(name) {
			return Values{}, errors.New("project name contains invalid characters")
		}
		if name == "" {
			name = filepath.Base(canonical)
		}
		values.Projects[i] = Project{Path: canonical, Name: truncateName(name)}
	}
	if err := Validate(values); err != nil {
		return Values{}, err
	}
	return values, nil
}

func canonicalProjectPath(path string) (string, error) {
	if !filepath.IsAbs(path) || !utf8.ValidString(path) || hasControl(path) {
		return "", errors.New("project path must be an absolute directory without control characters")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", errors.New("project path must resolve to an existing directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("project path must resolve to an existing directory")
	}
	return resolved, nil
}

// rejectBroadProject refuses roots that would hand the whole machine, the user's
// whole HOME or Workbench's private state to project-scoped features.
func rejectBroadProject(path, stateDir string) error {
	if path == string(filepath.Separator) {
		return errors.New("project path must not be the filesystem root")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		if path == filepath.Clean(home) {
			return errors.New("project path must not be the home directory itself")
		}
	}
	if stateDir != "" && (path == stateDir || within(path, stateDir) || within(stateDir, path)) {
		return errors.New("project path must not be, contain or sit inside the Workbench state directory")
	}
	return nil
}

// within reports whether child is strictly inside parent.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func truncateName(name string) string {
	runes := []rune(name)
	if len(runes) > maxNameRunes {
		return string(runes[:maxNameRunes])
	}
	return name
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func Validate(values Values) error {
	if values.Projects == nil {
		return errors.New("projects must be an array")
	}
	if len(values.Projects) > maxProjects {
		return fmt.Errorf("projects may contain at most %d entries", maxProjects)
	}
	seen := make(map[string]struct{}, len(values.Projects))
	for _, project := range values.Projects {
		if err := validateProject(project); err != nil {
			return err
		}
		if _, exists := seen[project.Path]; exists {
			return errors.New("duplicate project path")
		}
		seen[project.Path] = struct{}{}
	}
	return nil
}

func validateProject(project Project) error {
	if project.Path == "" || !filepath.IsAbs(project.Path) || filepath.Clean(project.Path) != project.Path || !utf8.ValidString(project.Path) || hasControl(project.Path) {
		return errors.New("project path must be an absolute canonical directory")
	}
	if project.Name == "" || !utf8.ValidString(project.Name) || utf8.RuneCountInString(project.Name) > maxNameRunes || hasControl(project.Name) {
		return errors.New("project name must contain between 1 and 120 characters without controls")
	}
	info, err := os.Lstat(project.Path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("project path must be an existing directory, not a symlink")
	}
	resolved, err := filepath.EvalSymlinks(project.Path)
	if err != nil || resolved != project.Path {
		return errors.New("project path must be canonical and contain no symlink")
	}
	return nil
}

func clone(values Values) Values {
	values.Projects = append([]Project{}, values.Projects...)
	return values
}

func validateStateDir(stateDir string) error {
	if stateDir == "" || !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return errors.New("state directory must be absolute and canonical")
	}
	info, err := os.Lstat(stateDir)
	if err != nil {
		return fmt.Errorf("state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("state directory must be a real directory, not a symlink")
	}
	if info.Mode().Perm() != 0700 {
		return errors.New("state directory must have private 0700 permissions")
	}
	resolved, err := filepath.EvalSymlinks(stateDir)
	if err != nil || resolved != stateDir {
		return errors.New("state directory must not traverse a symlink")
	}
	return nil
}

func loadFile(stateDir string) (Values, error) {
	path := filepath.Join(stateDir, fileName)
	info, err := os.Lstat(path)
	if err != nil {
		return Values{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Values{}, errors.New("settings file must be a regular file, not a symlink")
	}
	if info.Mode().Perm() != 0600 {
		return Values{}, errors.New("settings file must have private 0600 permissions")
	}
	if info.Size() > maxFileLen {
		return Values{}, errors.New("settings file is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Values{}, err
	}
	if len(data) > maxFileLen {
		return Values{}, errors.New("settings file is too large")
	}
	values, err := decodeValues(data)
	if err != nil {
		return Values{}, err
	}
	return values, nil
}

func decodeValues(data []byte) (Values, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Values{}, fmt.Errorf("invalid settings JSON: %w", err)
	}
	var wire struct {
		Capture  *bool      `json:"capture"`
		Projects *[]Project `json:"projects"`
		// Retired Orbit settings: accepted and discarded so an existing
		// settings file keeps its projects instead of failing to load.
		OrbitLocalWorker *bool   `json:"orbit_local_worker"`
		OrbitImage       *string `json:"orbit_image"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Values{}, fmt.Errorf("invalid settings JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Values{}, errors.New("settings must contain one JSON object")
	}
	if wire.Capture == nil || wire.Projects == nil {
		return Values{}, errors.New("settings JSON is missing a required field or contains null")
	}
	values := Values{
		Capture:  *wire.Capture,
		Projects: *wire.Projects,
	}
	if err := Validate(values); err != nil {
		return Values{}, fmt.Errorf("invalid settings state: %w", err)
	}
	return values, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("settings must contain one JSON value")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("settings object contains an invalid key")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate settings key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim('}') {
			return errors.New("invalid settings object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim(']') {
			return errors.New("invalid settings array")
		}
	default:
		return errors.New("invalid settings JSON delimiter")
	}
	return nil
}

func writeFile(stateDir string, values Values) (bool, error) {
	if err := validateStateDir(stateDir); err != nil {
		return false, err
	}
	path := filepath.Join(stateDir, fileName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return false, errors.New("settings file is not a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(stateDir, ".settings-*.tmp")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = io.Copy(tmp, bytes.NewReader(data))
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	if info, lstatErr := os.Lstat(path); lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return false, errors.New("settings file is not a private regular file")
		}
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return false, lstatErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	dir, err := os.Open(stateDir)
	if err != nil {
		return true, err
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if err == nil {
		err = closeErr
	}
	return true, err
}
