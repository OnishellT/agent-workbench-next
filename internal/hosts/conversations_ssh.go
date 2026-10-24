package hosts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const conversationMaxBytes = 64 << 20
const conversationScanLimit = 4096

var (
	errConversationDenied       = errors.New("conversation access disabled")
	errConversationChanged      = errors.New("conversation authority changed")
	errConversationPath         = errors.New("invalid conversation selection")
	errConversationLarge        = errors.New("conversation exceeds 64 MiB")
	errConversationCatalogLarge = errors.New("conversation directory exceeds 100000 entries; narrow the registered source root")
	errConversationCursorLarge  = errors.New("conversation continuation exceeds 8192 bytes; narrow the registered source root")
	errConversationRemote       = errors.New("remote conversation unavailable")
)

type conversationFile struct {
	SourceID       string `json:"source_id"`
	SourceRevision string `json:"source_revision"`
	Adapter        string `json:"adapter"`
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	Mtime          int64  `json:"mtime"`
}

type conversationCatalog struct {
	HostID     string             `json:"host_id"`
	Files      []conversationFile `json:"files"`
	NextCursor *string            `json:"next_cursor"`
	Limited    bool               `json:"limited"`
}

type conversationCursor struct {
	Identity string `json:"identity"`
	Source   string `json:"source"`
	After    string `json:"after"`
}

type conversationSSH struct {
	manager *SSHManager
	host    Host
	ctx     context.Context
	cancel  context.CancelFunc
	// Retained for trusted metadata reads (native env discovery); the socket
	// lifetime stays owned by cancel/close, never by this handle.
	client *ssh.Client
	fs     *sftp.Client
	stop   func() bool
	slot   bool
	// timedOut is set by the per-operation watchdog (guard); it turns the
	// resulting cancellation into context.DeadlineExceeded for callers.
	timedOut atomic.Bool
	keepStop func()
}

// conversationOpTimeout bounds each remote operation (opening the read, one
// catalog page, one file) instead of the whole run: a long history pass is
// many short operations, each with its own fresh deadline. Idle time between
// operations is not charged.
var conversationOpTimeout = 30 * time.Second

// guard arms the watchdog for one operation; the returned func disarms it. If
// the operation outlives conversationOpTimeout the read is cancelled, which
// closes the SSH socket and SFTP channels (the read is unusable afterwards).
func (r *conversationSSH) guard() (disarm func()) {
	timer := time.AfterFunc(conversationOpTimeout, func() {
		r.timedOut.Store(true)
		r.cancel()
	})
	return func() { timer.Stop() }
}

// ctxErr reports why the read ended, distinguishing the watchdog deadline.
func (r *conversationSSH) ctxErr() error {
	if r.timedOut.Load() {
		return context.DeadlineExceeded
	}
	return r.ctx.Err()
}

// opErr rewrites any failure after a watchdog timeout as DeadlineExceeded.
func (r *conversationSSH) opErr(err error) error {
	if err != nil && r.timedOut.Load() {
		return context.DeadlineExceeded
	}
	return err
}

func (m *SSHManager) cancelConversationsLocked(id string) {
	for read := range m.conversationReads[id] {
		read.cancel()
	}
}

// Use the statistics SSH authority, without probing or installing a native helper.
func (m *SSHManager) openConversations(ctx context.Context, id string) (*conversationSSH, error) {
	m.mu.Lock()
	h, err := m.Store.Host(id)
	if err == nil && (m.closed || !h.Enabled || h.Fingerprint == "") {
		err = errConversationDenied
	}
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	read := &conversationSSH{manager: m, host: h, ctx: ctx, cancel: cancel}
	if m.conversationReads == nil {
		m.conversationReads = map[string]map[*conversationSSH]struct{}{}
	}
	if m.conversationReads[id] == nil {
		m.conversationReads[id] = map[*conversationSSH]struct{}{}
	}
	m.conversationReads[id][read] = struct{}{}
	m.wg.Add(1)
	root := m.root
	m.mu.Unlock()
	if root != nil {
		read.stop = context.AfterFunc(root, cancel)
	}
	disarm := read.guard()
	defer disarm()
	select {
	case m.slots <- struct{}{}:
		read.slot = true
	case <-ctx.Done():
		err := read.ctxErr()
		read.close()
		return nil, err
	}
	client, err := m.connect(ctx, &h)
	if err == nil {
		read.fs, err = sftp.NewClient(client)
	}
	if err == nil {
		read.client = client
	}
	if err != nil {
		contextErr := read.ctxErr()
		read.close()
		if contextErr != nil {
			return nil, contextErr
		}
		return nil, errConversationRemote
	}
	read.keepStop = startSSHKeepalive(ctx, client)
	if err = read.check(nil); err != nil {
		read.close()
		return nil, read.opErr(err)
	}
	return read, nil
}

func (r *conversationSSH) close() {
	if r.keepStop != nil {
		r.keepStop()
	}
	r.cancel() // connect's cancellation hook closes the SSH socket and all SFTP channels.
	if r.stop != nil {
		r.stop()
	}
	if r.fs != nil {
		r.fs.Close()
	}
	if r.slot {
		<-r.manager.slots
	}
	r.manager.mu.Lock()
	delete(r.manager.conversationReads[r.host.ID], r)
	if len(r.manager.conversationReads[r.host.ID]) == 0 {
		delete(r.manager.conversationReads, r.host.ID)
	}
	r.manager.mu.Unlock()
	r.manager.wg.Done()
}

func (r *conversationSSH) check(source *Source) error {
	if err := r.ctxErr(); err != nil {
		return err
	}
	h, err := r.manager.Store.Host(r.host.ID)
	if err != nil || !h.Enabled || h.Revision != r.host.Revision || h.Address != r.host.Address || h.Port != r.host.Port || h.User != r.host.User || h.Fingerprint != r.host.Fingerprint {
		return errConversationChanged
	}
	if source != nil {
		s, err := r.manager.Store.source(source.ID)
		if err != nil || !s.Enabled || s.HostID != r.host.ID || s.Path != source.Path || s.Kind != source.Kind {
			return errConversationChanged
		}
	}
	return nil
}

func conversationAdapter(kind string) bool {
	return kind == "codex" || kind == "claude" || kind == "pi" || kind == "omp"
}

func conversationLexical(value string) bool {
	if value == "" || len(value) > 4096 || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	return true
}

func conversationRelative(value string) bool {
	if !conversationLexical(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
		name := strings.SplitN(strings.ToLower(part), ".", 2)[0]
		switch name {
		case "auth", "credentials", "credential", "config", "settings", "secrets", "secret", "tokens", "token", "api-keys", "api_keys":
			return false
		}
	}
	return true
}

func conversationNativeFile(value string) bool {
	return conversationRelative(value) && strings.HasSuffix(value, ".jsonl")
}

// SFTP v3 has no no-follow open: check every component before and after reading.
func conversationLstat(fs *sftp.Client, filename string) (os.FileInfo, error) {
	if !path.IsAbs(filename) || path.Clean(filename) != filename || strings.ContainsAny(filename, "\\\x00\r\n") {
		return nil, errConversationPath
	}
	current := "/"
	var info os.FileInfo
	for _, part := range strings.Split(strings.TrimPrefix(filename, "/"), "/") {
		current = path.Join(current, part)
		var err error
		info, err = fs.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || (current != filename && !info.IsDir()) {
			return nil, errConversationPath
		}
	}
	return info, nil
}

func (r *conversationSSH) sources() ([]Source, error) {
	sources, err := r.enabledSources()
	if err != nil || len(sources) != 0 {
		return sources, err
	}
	// Auto-capture discovers the trusted host's native defaults without a
	// helper install or a manual sync; configured paths and disabled rows are
	// preserved by ensureNativeHostSources below.
	if err := r.discoverNativeSources(); err != nil {
		return nil, err
	}
	return r.enabledSources()
}

// discoverNativeSources reads the remote identity metadata and native
// environment over the trusted SSH connection (the same reads a manual sync
// performs) and registers only the missing defaults. It never walks files and
// never writes usage counters or host config: the store gains missing default
// rows, nothing else.
func (r *conversationSSH) discoverNativeSources() error {
	if err := r.ctxErr(); err != nil {
		return err
	}
	if r.client == nil {
		return errConversationRemote
	}
	identity, err := remoteCommand(r.ctx, r.client, remoteIdentityCommand)
	if err != nil {
		return errConversationRemote
	}
	metadata := strings.Split(strings.TrimSpace(string(identity)), "\n")
	if len(metadata) != 2 || len(identity) > 8192 {
		return errConversationRemote
	}
	home, hostname := strings.TrimSpace(metadata[0]), strings.TrimSpace(metadata[1])
	if !hostNamePattern.MatchString(hostname) || !path.IsAbs(home) || len(home) > 4096 {
		return errConversationRemote
	}
	environment, err := remoteCommand(r.ctx, r.client, remoteEnvironmentCommand)
	if err != nil {
		return errConversationRemote
	}
	pathEnv, err := parseRemoteEnvironment(environment)
	if err != nil {
		return errConversationRemote
	}
	if err := r.ctxErr(); err != nil {
		return err
	}
	if err := r.manager.ensureNativeHostSources(r.host.ID, sshSourceDefaults(home, pathEnv)); err != nil {
		return err
	}
	return r.check(nil)
}

func (r *conversationSSH) enabledSources() ([]Source, error) {
	all, err := r.manager.Store.Sources()
	if err != nil {
		return nil, err
	}
	sources := []Source{}
	for _, s := range all {
		if s.HostID == r.host.ID && s.Enabled && conversationAdapter(s.Kind) {
			sources = append(sources, s)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	return sources, nil
}

func conversationIdentity(h Host, sources []Source) string {
	identity := []any{h.ID, h.Revision, h.Address, h.Port, h.User, h.Fingerprint}
	for _, s := range sources {
		identity = append(identity, []string{s.ID, s.HostID, s.Kind, s.Path})
	}
	data, _ := json.Marshal(identity)
	hash := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func conversationSourceRevision(h Host, s Source) string {
	data, _ := json.Marshal([]any{h.ID, h.Revision, h.Address, h.Port, h.User, h.Auth, h.KeyPath, h.Fingerprint, h.Enabled, s.ID, s.HostID, s.Kind, s.Path, s.Enabled})
	hash := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func conversationRevisionValid(revision string) bool {
	if len(revision) != 43 {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(revision)
	return err == nil && len(data) == sha256.Size && base64.RawURLEncoding.EncodeToString(data) == revision
}

func (r *conversationSSH) catalog(cursor string, limit int) (out conversationCatalog, err error) {
	defer r.guard()()
	defer func() { err = r.opErr(err) }()
	return r.catalogPage(cursor, limit)
}

// parseCatalogCursor validates a continuation token against the current
// sources; an empty cursor starts at the first source.
func parseCatalogCursor(cursor string, identity string, sources []Source) (conversationCursor, error) {
	position := conversationCursor{Identity: identity}
	if cursor == "" {
		return position, nil
	}
	if len(cursor) > 8192 {
		return position, errConversationPath
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(data, &position) != nil || position.Identity != identity || (position.After != "" && !conversationLexical(strings.TrimSuffix(position.After, "/"))) {
		return position, errConversationPath
	}
	for _, source := range sources {
		if source.ID == position.Source {
			return position, nil
		}
	}
	return position, errConversationPath
}

// catalogWalk is the state of one catalog page: a depth-first, lexically
// ordered walk that stops once limit files (or the scan limit) are reached and
// remembers where to resume.
type catalogWalk struct {
	r        *conversationSSH
	limit    int
	out      *conversationCatalog
	position conversationCursor
	last     conversationCursor
	visited  int
	stopped  bool
	source   Source // source being walked
	after    string // resume key inside source
}

func (r *conversationSSH) catalogPage(cursor string, limit int) (conversationCatalog, error) {
	out := conversationCatalog{HostID: r.host.ID, Files: []conversationFile{}}
	sources, err := r.sources()
	if err != nil {
		return out, err
	}
	position, err := parseCatalogCursor(cursor, conversationIdentity(r.host, sources), sources)
	if err != nil {
		return out, err
	}
	walk := &catalogWalk{r: r, limit: limit, out: &out, position: position, last: position}
	for _, source := range sources {
		if source.ID < position.Source {
			continue
		}
		if err := walk.walkSource(source); err != nil {
			return out, err
		}
		if walk.stopped {
			break
		}
	}
	for _, source := range sources {
		if err := r.check(&source); err != nil {
			return out, err
		}
	}
	if err := r.check(nil); err != nil {
		return out, err
	}
	if walk.stopped {
		data, _ := json.Marshal(walk.last)
		next := base64.RawURLEncoding.EncodeToString(data)
		if len(next) > 8192 {
			return out, errConversationCursorLarge
		}
		out.NextCursor, out.Limited = &next, true
	}
	return out, nil
}

// walkSource walks one source root (a directory, or a single file).
func (w *catalogWalk) walkSource(source Source) error {
	if err := w.r.check(&source); err != nil {
		return err
	}
	w.source, w.after = source, ""
	if source.ID == w.position.Source {
		w.after = w.position.After
	}
	info, err := conversationLstat(w.r.fs, source.Path)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, errConversationPath) {
			return nil
		}
		return errConversationRemote
	}
	relative := ""
	if !info.IsDir() {
		relative = path.Base(source.Path)
	}
	return w.visit(relative, info)
}

func (w *catalogWalk) visit(relative string, info os.FileInfo) error {
	r, source := w.r, w.source
	if err := r.check(&source); err != nil {
		return err
	}
	if relative != "" && !conversationLexical(relative) {
		return nil
	}
	key := relative
	if info.IsDir() && key != "" {
		key += "/"
	}
	if key != "" && key <= w.after && (!info.IsDir() || !strings.HasPrefix(w.after, key)) {
		return nil
	}
	if key != "" && key > w.after {
		if len(w.out.Files) >= w.limit || w.visited >= conversationScanLimit {
			w.stopped = true
			return nil
		}
		w.visited++
		w.last = conversationCursor{Identity: w.position.Identity, Source: source.ID, After: key}
	}
	if relative != "" && !conversationRelative(relative) {
		return nil
	}
	if info.IsDir() {
		return w.visitDirectory(relative)
	}
	if conversationNativeFile(relative) && info.Mode().IsRegular() && key > w.after {
		w.out.Files = append(w.out.Files, conversationFile{SourceID: source.ID, SourceRevision: conversationSourceRevision(r.host, source), Adapter: source.Kind, Path: relative, Size: info.Size(), Mtime: info.ModTime().Unix()})
	}
	return nil
}

func (w *catalogWalk) visitDirectory(relative string) error {
	r, source := w.r, w.source
	directory := source.Path
	if relative != "" {
		directory = path.Join(directory, relative)
	}
	if _, err := conversationLstat(r.fs, directory); err != nil {
		return errConversationRemote
	}
	entries, err := r.fs.ReadDirContext(r.ctx, directory)
	if err != nil {
		return errConversationRemote
	}
	if err := r.check(&source); err != nil {
		return err
	}
	if current, err := conversationLstat(r.fs, directory); err != nil || !current.IsDir() {
		return errConversationChanged
	}
	// shortcut: SFTP's directory API buffers entries; reject huge directories, use streaming when available.
	if len(entries) > 100000 {
		return errConversationCatalogLarge
	}
	sortCatalogEntries(entries)
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || (!entry.IsDir() && !entry.Mode().IsRegular()) {
			continue
		}
		if err := w.visit(path.Join(relative, name), entry); err != nil {
			return err
		}
		if w.stopped {
			break
		}
	}
	return nil
}

// sortCatalogEntries orders entries so directories sort as "name/", which
// keeps resume keys consistent with the depth-first walk.
func sortCatalogEntries(entries []os.FileInfo) {
	key := func(info os.FileInfo) string {
		if info.IsDir() {
			return info.Name() + "/"
		}
		return info.Name()
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
}

func (r *conversationSSH) file(sourceID, relative, revision string) (data []byte, err error) {
	defer r.guard()()
	defer func() { err = r.opErr(err) }()
	return r.readFile(sourceID, relative, revision)
}

func (r *conversationSSH) readFile(sourceID, relative, revision string) ([]byte, error) {
	if !conversationNativeFile(relative) || !conversationRevisionValid(revision) {
		return nil, errConversationPath
	}
	source, err := r.manager.Store.source(sourceID)
	if err != nil || source.HostID != r.host.ID || !source.Enabled || !conversationAdapter(source.Kind) {
		return nil, sql.ErrNoRows
	}
	if err := r.check(&source); err != nil {
		return nil, err
	}
	if revision != conversationSourceRevision(r.host, source) {
		return nil, errConversationChanged
	}
	root, err := conversationLstat(r.fs, source.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sql.ErrNoRows
		}
		return nil, errConversationPath
	}
	filename := path.Join(source.Path, relative)
	if !root.IsDir() {
		if relative != path.Base(source.Path) {
			return nil, errConversationPath
		}
		filename = source.Path
	}
	before, err := conversationLstat(r.fs, filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sql.ErrNoRows
		}
		return nil, errConversationPath
	}
	if !before.Mode().IsRegular() {
		return nil, errConversationPath
	}
	if before.Size() > conversationMaxBytes {
		return nil, errConversationLarge
	}
	if err := r.check(&source); err != nil {
		return nil, err
	}
	file, err := r.fs.Open(filename)
	if err != nil {
		return nil, errConversationRemote
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		return nil, errConversationChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, conversationMaxBytes+1))
	if err != nil {
		return nil, errConversationRemote
	}
	if len(data) > conversationMaxBytes {
		return nil, errConversationLarge
	}
	after, err := conversationLstat(r.fs, filename)
	if err != nil || !after.Mode().IsRegular() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || int64(len(data)) != before.Size() {
		return nil, errConversationChanged
	}
	if err := r.check(&source); err != nil {
		return nil, err
	}
	return data, nil
}

func conversationFailure(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, errConversationDenied):
		status = http.StatusForbidden
	case errors.Is(err, errConversationChanged), errors.Is(err, context.Canceled):
		status = http.StatusConflict
	case errors.Is(err, errConversationPath):
		status = http.StatusBadRequest
	case errors.Is(err, errConversationLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, errConversationCatalogLarge):
		fail(w, http.StatusUnprocessableEntity, errConversationCatalogLarge)
		return
	case errors.Is(err, errConversationCursorLarge):
		fail(w, http.StatusUnprocessableEntity, errConversationCursorLarge)
		return
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	// Never return remote paths, authentication errors, or file output.
	fail(w, status, errors.New(http.StatusText(status)))
}

func (s *Server) conversationRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/hosts/{id}/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		limit := 32
		if value := r.URL.Query().Get("limit"); value != "" {
			var err error
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 {
				conversationFailure(w, errConversationPath)
				return
			}
		}
		if s.SSH == nil {
			fail(w, 503, errors.New("SSH unavailable"))
			return
		}
		read, err := s.SSH.openConversations(r.Context(), r.PathValue("id"))
		if err != nil {
			conversationFailure(w, err)
			return
		}
		defer read.close()
		catalog, err := read.catalog(r.URL.Query().Get("cursor"), limit)
		if err != nil {
			conversationFailure(w, err)
			return
		}
		writeJSON(w, 200, catalog)
	})
	m.HandleFunc("GET /api/hosts/{id}/conversations/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		revision := r.URL.Query().Get("source_revision")
		if !conversationRevisionValid(revision) {
			conversationFailure(w, errConversationPath)
			return
		}
		if s.SSH == nil {
			fail(w, 503, errors.New("SSH unavailable"))
			return
		}
		read, err := s.SSH.openConversations(r.Context(), r.PathValue("id"))
		if err != nil {
			conversationFailure(w, err)
			return
		}
		defer read.close()
		data, err := read.file(r.URL.Query().Get("source_id"), r.URL.Query().Get("path"), revision)
		if err != nil {
			conversationFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	})
}
