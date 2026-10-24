// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"workbench/internal/hosts"
)

func (m *Module) ImportHostConversation(ctx context.Context, input hosts.HistoryImport, raw []byte) (hosts.HistoryImportResult, error) {
	if int64(len(raw)) > MaxSourceBytes {
		return hosts.HistoryImportResult{}, hosts.ErrHistoryTooLarge
	}
	if !historyAdapter(input.Adapter) {
		return hosts.HistoryImportResult{}, hosts.ErrHistoryUnsupported
	}
	if !validHistorySourceID(input.SourceID) || !validHistoryPath(input.Path) || !validHistoryRevision(input.SourceRevision) {
		return hosts.HistoryImportResult{}, hosts.ErrHistoryUnsupported
	}
	if len(raw) == 0 {
		return hosts.HistoryImportResult{}, hosts.ErrHistoryIncomplete
	}
	if err := m.checkHistoryIdentity(ctx, input.Identity); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	fallback := "host-history-" + hashShort([]byte(input.Identity.Digest+"\x00"+input.SourceID+"\x00"+input.Path), 32)
	transcript, err := NormalizeJSONL(bytes.NewReader(raw), NormalizeOptions{Adapter: Adapter(input.Adapter), FallbackSessionID: fallback, MaxBytes: MaxSourceBytes})
	if err != nil {
		if errors.Is(err, ErrSourceTooLarge) {
			return hosts.HistoryImportResult{}, hosts.ErrHistoryTooLarge
		}
		if errors.Is(err, ErrUnsupportedAdapter) {
			return hosts.HistoryImportResult{}, hosts.ErrHistoryUnsupported
		}
		return hosts.HistoryImportResult{}, hosts.ErrHistoryIncomplete
	}
	if len(transcript.Events) == 0 || transcript.Coverage.BlockingIssueCount != 0 {
		return hosts.HistoryImportResult{}, hosts.ErrHistoryIncomplete
	}
	if err := ctx.Err(); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	if err := m.checkHistoryIdentity(ctx, input.Identity); err != nil {
		return hosts.HistoryImportResult{}, err
	}

	adapter := Adapter(input.Adapter)
	sourceID := "host-history-" + hashShort([]byte(input.Identity.Digest+"\x00"+input.SourceID+"\x00"+input.Adapter), 32)
	root := filepath.Join(m.stateDir, "host-history", sourceID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return hosts.HistoryImportResult{}, errors.New("Foundry history source directory is not private")
	}
	root, apiErr := validateSourceRoot(root)
	if apiErr != nil {
		return hosts.HistoryImportResult{}, errors.New("Foundry history source directory is unsafe")
	}
	coverage, err := jsonText(CoverageSummary{Status: "unknown"})
	if err != nil {
		return hosts.HistoryImportResult{}, err
	}
	_, err = m.db.ExecContext(ctx, `INSERT OR IGNORE INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES(?,?,?,?,?,?)`, sourceID, root, string(adapter), 1, nowSeconds(), coverage)
	if err != nil {
		return hosts.HistoryImportResult{}, err
	}
	var storedPath, storedAdapter string
	var enabled int
	if err := m.db.QueryRowContext(ctx, "SELECT path,adapter,enabled FROM sources WHERE id=?", sourceID).Scan(&storedPath, &storedAdapter, &enabled); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	if storedPath != root || storedAdapter != string(adapter) || enabled == 0 {
		return hosts.HistoryImportResult{}, errors.New("Foundry history source is not enabled")
	}
	if err := m.checkHistoryIdentity(ctx, input.Identity); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	source := sourceRecord{ID: sourceID, Path: root, Adapter: adapter, Enabled: true}
	sessionID, err := m.sessionIDFor(source.ID, transcriptSessionID(transcript.Events, fallback))
	if err != nil {
		return hosts.HistoryImportResult{}, err
	}
	remotePath := (&url.URL{Scheme: "ssh", Host: input.Identity.HostID, Path: "/" + input.SourceID + "/" + input.Path}).String()
	mtime := time.Now()
	if input.Mtime > 0 {
		mtime = time.Unix(input.Mtime, 0)
	}
	changed, err := m.persistTranscript(ctx, source, sessionID, remotePath, transcript, mtime, input.Identity.HostID)
	if err != nil {
		return hosts.HistoryImportResult{}, err
	}
	var revision string
	if err := m.db.QueryRowContext(ctx, "SELECT revision FROM sessions WHERE id=?", sessionID).Scan(&revision); err != nil {
		return hosts.HistoryImportResult{}, err
	}
	return hosts.HistoryImportResult{SessionID: sessionID, Revision: revision, Changed: changed}, nil
}

func (m *Module) checkHistoryIdentity(ctx context.Context, expected hosts.Identity) error {
	if m.hostScope == nil || expected.HostID == "" || expected.Revision < 1 || expected.Digest == "" {
		return hosts.ErrHistoryIdentityChanged
	}
	current, err := m.hostScope.Identity(ctx, expected.HostID)
	if err != nil || current != expected {
		return hosts.ErrHistoryIdentityChanged
	}
	return nil
}

func historyAdapter(adapter string) bool {
	return adapter == "codex" || adapter == "claude" || adapter == "pi" || adapter == "omp"
}

func validHistorySourceID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char == '/' || char == '\\' || char == 0 {
			return false
		}
	}
	return true
}

func validHistoryPath(value string) bool {
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\\\x00\r\n") || path.IsAbs(value) {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		if credentialSegment(part) || configSegment(part) {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(value))
	return ext == ".jsonl" || ext == ".ndjson"
}

/** Lower-case segment without its extension, except when the extension is the whole name:
 *  `.ssh` is that name and not an empty string. */
func segmentBase(segment string) string {
	lower := strings.ToLower(segment)
	if ext := path.Ext(lower); ext != "" && ext != lower {
		return strings.TrimSuffix(lower, ext)
	}
	return lower
}

/** Exact credential names, never a substring, so a folder like `auth-demo` stays usable. */
func credentialSegment(segment string) bool {
	switch segmentBase(segment) {
	case "auth", "credential", "credentials", "secret", "secrets", "token", "tokens", ".ssh", ".git":
		return true
	}
	return false
}

/** Tool configuration the source itself never publishes; unrelated ancestors stay allowed so
 *  a legitimate layout under a `.config` directory still works. */
func configSegment(segment string) bool {
	switch segmentBase(segment) {
	case "config", "settings":
		return true
	}
	return false
}

/** The history store of OpenCode, in its own `.opencode` folder or in the XDG data layout
 *  that keeps it as `share/opencode`; a project folder merely named `opencode` is not it. */
func opencodeHistoryPath(value string) bool {
	segments := strings.Split(value, string(os.PathSeparator))
	for index, segment := range segments {
		if segmentBase(segment) == ".opencode" {
			return true
		}
		if index > 0 && segmentBase(segment) == "opencode" && segmentBase(segments[index-1]) == "share" {
			return true
		}
	}
	return false
}

func validHistoryRevision(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}
