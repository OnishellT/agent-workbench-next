package hosts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const historyPageSize = 8

var (
	ErrHistoryIdentityChanged = errors.New("history host identity changed")
	ErrHistoryIncomplete      = errors.New("history transcript is empty or incomplete")
	ErrHistoryTooLarge        = errors.New("history transcript exceeds Foundry limit")
	ErrHistoryUnsupported     = errors.New("history adapter is unsupported")
	ErrHistoryCancelled       = errors.New("la autorización del historial cambió; la sincronización se canceló")
)

type HistoryImport struct {
	Identity       Identity
	SourceID       string
	SourceRevision string
	Adapter        string
	Path           string
	Mtime          int64
}

type HistoryImportResult struct {
	SessionID string
	Revision  string
	Changed   bool
}

type HistoryImporter interface {
	ImportHostConversation(context.Context, HistoryImport, []byte) (HistoryImportResult, error)
}

type historyReceipt struct {
	SourceRevision string `json:"source_revision"`
	Adapter        string `json:"adapter"`
	SessionID      string `json:"session_id"`
	Revision       string `json:"revision"`
	// File version at import time: SourceRevision identifies the source
	// config, not the file, so equality must also bind mtime and size or
	// appends are ignored forever. Old receipts decode these as zero and
	// re-import exactly once.
	Mtime int64 `json:"mtime,omitempty"`
	Size  int64 `json:"size,omitempty"`
}

// historyReceiptCurrent reports whether the stored receipt already covers this
// catalog sighting: same source config, same session, same file version.
func historyReceiptCurrent(receipt historyReceipt, seen bool, file conversationFile) bool {
	return seen && receipt.SessionID != "" &&
		receipt.SourceRevision == file.SourceRevision &&
		receipt.Mtime == file.Mtime && receipt.Size == file.Size
}

type hostHistoryRecord struct {
	Enabled  bool                      `json:"enabled"`
	Identity string                    `json:"identity,omitempty"`
	Revision int64                     `json:"revision,omitempty"`
	Reason   string                    `json:"reason,omitempty"`
	Error    string                    `json:"error,omitempty"`
	Cursor   string                    `json:"cursor,omitempty"`
	LastSync string                    `json:"last_sync,omitempty"`
	Files    map[string]historyReceipt `json:"files,omitempty"`
}

type hostHistoryStatus struct {
	HostID      string   `json:"host_id"`
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state"`
	Files       int      `json:"files"`
	LastSync    string   `json:"last_sync,omitempty"`
	Error       string   `json:"error,omitempty"`
	Unsupported []string `json:"unsupported"`
	Imported    int      `json:"imported,omitempty"`
}

type historyRun struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// hostHistoryState separates the short record critical section from the long sync.
// Consent/status never wait for a sync; a new generation invalidates the previous run.
type hostHistoryState struct {
	mu         sync.Mutex
	sync       chan struct{} // capacity 1: the per-host sync slot (see acquireSync)
	generation uint64
	active     *historyRun
}

func (s *Server) historyState(id string) *hostHistoryState {
	value, _ := s.historyStates.LoadOrStore(id, &hostHistoryState{sync: make(chan struct{}, 1)})
	return value.(*hostHistoryState)
}

func (state *hostHistoryState) invalidateLocked() {
	state.generation++
	if state.active != nil {
		state.active.cancel()
		state.active = nil
	}
}

func (s *Server) readHistoryRecord(id string) (hostHistoryRecord, error) {
	record := hostHistoryRecord{Files: map[string]historyReceipt{}}
	var raw string
	err := s.Store.DB.QueryRow("SELECT payload FROM host_history WHERE host_id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return hostHistoryRecord{}, err
	}
	if record.Files == nil {
		record.Files = map[string]historyReceipt{}
	}
	return record, nil
}

func (s *Server) saveHistoryRecord(id string, record hostHistoryRecord) error {
	if record.Files == nil {
		record.Files = map[string]historyReceipt{}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.Store.DB.Exec("INSERT INTO host_history(host_id,payload) VALUES(?,?) ON CONFLICT(host_id) DO UPDATE SET payload=excluded.payload", id, string(raw))
	return err
}

func historyIdentity(host Host) (Identity, error) {
	if !sshFingerprintPattern.MatchString(host.Fingerprint) {
		return Identity{}, ErrUntrusted
	}
	return host.Identity()
}

func (s *Server) historyStatusLocked(id string, state *hostHistoryState) (hostHistoryStatus, error) {
	host, err := s.Store.Host(id)
	if err != nil {
		return hostHistoryStatus{}, err
	}
	record, err := s.readHistoryRecord(id)
	if err != nil {
		return hostHistoryStatus{}, err
	}
	identity, identityErr := historyIdentity(host)
	if record.Enabled {
		reason := ""
		switch {
		case errors.Is(identityErr, ErrIdentityChanged):
			reason = "identity_changed"
		case identityErr != nil:
			reason = "untrusted"
		case identity.Digest != record.Identity:
			reason = "identity_changed"
		}
		if reason != "" {
			// A revision/identity change cancels the current generation before the
			// record is disabled, so no in-flight import can publish afterwards.
			state.invalidateLocked()
			record.Enabled, record.Reason, record.Error, record.Cursor = false, reason, "", ""
			if err := s.saveHistoryRecord(id, record); err != nil {
				return hostHistoryStatus{}, err
			}
		}
	}
	statusState := "disabled"
	switch {
	case record.Reason == "identity_changed":
		statusState = "identity_changed"
	case identityErr != nil:
		statusState = "untrusted"
	case record.Enabled && !host.Enabled:
		statusState = "host_paused"
	case record.Enabled && record.Error != "":
		statusState = "error"
	case record.Enabled:
		statusState = "ready"
	}
	return hostHistoryStatus{HostID: id, Enabled: record.Enabled, State: statusState, Files: len(record.Files), LastSync: record.LastSync, Error: record.Error, Unsupported: []string{"opencode"}}, nil
}

func (s *Server) hostHistoryRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	state := s.historyState(id)
	state.mu.Lock()
	status, err := s.historyStatusLocked(id, state)
	state.mu.Unlock()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, errors.New("host SSH no registrado"))
			return
		}
		fail(w, http.StatusInternalServerError, errors.New("no se pudo consultar el historial"))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) hostHistoryConsentRoute(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := decode(w, r, &body); err != nil || len(body) != 1 {
		fail(w, http.StatusBadRequest, errors.New("se requiere únicamente enabled booleano"))
		return
	}
	var enabled bool
	if raw, ok := body["enabled"]; !ok || json.Unmarshal(raw, &enabled) != nil {
		fail(w, http.StatusBadRequest, errors.New("se requiere únicamente enabled booleano"))
		return
	}
	id := r.PathValue("id")
	state := s.historyState(id)
	state.mu.Lock()
	defer state.mu.Unlock()
	host, err := s.Store.Host(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, errors.New("host SSH no registrado"))
		} else {
			fail(w, http.StatusInternalServerError, errors.New("no se pudo actualizar el consentimiento"))
		}
		return
	}
	record, err := s.readHistoryRecord(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo actualizar el consentimiento"))
		return
	}
	if enabled {
		if !host.Enabled {
			fail(w, http.StatusConflict, errors.New("reanuda primero el host SSH"))
			return
		}
		identity, err := historyIdentity(host)
		if err != nil {
			fail(w, http.StatusConflict, errors.New("confirma primero la identidad SSH del host"))
			return
		}
		if record.Identity != identity.Digest {
			record.Files = map[string]historyReceipt{}
		}
		record.Identity, record.Revision = identity.Digest, identity.Revision
		record.Enabled, record.Reason, record.Error, record.Cursor = true, "", "", ""
	} else {
		record.Enabled, record.Reason, record.Error, record.Cursor = false, "disabled", "", ""
	}
	// Every consent transition starts a new generation: revoke cancels immediately and a
	// quick regrant can never let the previous sync publish.
	state.invalidateLocked()
	if err := s.saveHistoryRecord(id, record); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo actualizar el consentimiento"))
		return
	}
	status, err := s.historyStatusLocked(id, state)
	if err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo consultar el historial"))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func historyLineage(identity, source, filename string) string {
	encoded, _ := json.Marshal([]string{identity, source, filename})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func historyEntryValid(file conversationFile) bool {
	return file.SourceID != "" && len(file.SourceID) <= 256 && conversationAdapter(file.Adapter) && conversationRevisionValid(file.SourceRevision) && file.Size >= 0 && file.Size <= conversationMaxBytes && conversationNativeFile(file.Path)
}

func historySyncBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2049))
	if err != nil {
		return err
	}
	if len(body) > 2048 {
		return errors.New("sync request too large")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || bytes.Equal(body, []byte("{}")) {
		return nil
	}
	return errors.New("la sincronización no acepta opciones")
}

func (s *Server) hostHistorySyncRoute(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil || s.HistoryImporter == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("servicio de historial no disponible"))
		return
	}
	if err := historySyncBody(r); err != nil {
		fail(w, http.StatusBadRequest, errors.New("la sincronización no acepta opciones"))
		return
	}
	id := r.PathValue("id")
	state := s.historyState(id)
	// Serialize syncs per host, but leave state.mu free for consent/status: revoke must
	// cancel the running generation instead of waiting for catalog/download/import.
	// A request queued behind another sync gives up when its client goes away.
	if !state.acquireSync(r.Context()) {
		fail(w, http.StatusRequestTimeout, errors.New("sincronización cancelada mientras esperaba otra en curso"))
		return
	}
	defer state.releaseSync()
	result, failure := s.runHistoryPass(r.Context(), id, state, historyPassOptions{pages: 1})
	if failure != nil {
		writeHistoryFailure(w, failure)
		return
	}
	state.mu.Lock()
	status, err := s.historyStatusLocked(id, state)
	state.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo consultar el historial"))
		return
	}
	status.Imported = result.changed
	writeJSON(w, http.StatusOK, status)
}

func historyRecordForRunLocked(s *Server, id string, state *hostHistoryState, run *historyRun, generation uint64) (Host, hostHistoryRecord, Identity, error) {
	if state.generation != generation || state.active != run || run.ctx.Err() != nil {
		return Host{}, hostHistoryRecord{}, Identity{}, ErrHistoryCancelled
	}
	host, err := s.Store.Host(id)
	if err != nil {
		return Host{}, hostHistoryRecord{}, Identity{}, err
	}
	if !host.Enabled {
		return Host{}, hostHistoryRecord{}, Identity{}, ErrHistoryCancelled
	}
	identity, err := historyIdentity(host)
	if err != nil {
		return Host{}, hostHistoryRecord{}, Identity{}, ErrHistoryCancelled
	}
	record, err := s.readHistoryRecord(id)
	if err != nil {
		return Host{}, hostHistoryRecord{}, Identity{}, err
	}
	if !record.Enabled || record.Identity != identity.Digest {
		return Host{}, hostHistoryRecord{}, Identity{}, ErrHistoryCancelled
	}
	return host, record, identity, nil
}

func (s *Server) historyRunCheck(id string, state *hostHistoryState, run *historyRun, generation uint64) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, _, _, err := historyRecordForRunLocked(s, id, state, run, generation)
	return err
}

func (s *Server) historySaveReceipt(id string, state *hostHistoryState, run *historyRun, generation uint64, lineage string, receipt historyReceipt) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, record, _, err := historyRecordForRunLocked(s, id, state, run, generation)
	if err != nil {
		return err
	}
	record.Files[lineage] = receipt
	return s.saveHistoryRecord(id, record)
}

func (s *Server) historyCommit(id string, state *hostHistoryState, run *historyRun, generation uint64, cursor string) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, record, _, err := historyRecordForRunLocked(s, id, state, run, generation)
	if err != nil {
		return err
	}
	record.Cursor = cursor
	record.LastSync = time.Now().UTC().Format(time.RFC3339)
	record.Error = ""
	return s.saveHistoryRecord(id, record)
}

// historySkipFileError reports per-file failures the capture passes over: an
// empty or incomplete transcript, an unsupported adapter, an over-limit file,
// or a file that changed mid-read. The error stays visible, no receipt is
// invented, and the file is retried when it changes or on the next round.
// Cancellation, revocation, identity and transport failures still stop the run.
func historySkipFileError(err error) bool {
	return errors.Is(err, ErrHistoryIncomplete) ||
		errors.Is(err, ErrHistoryUnsupported) ||
		errors.Is(err, ErrHistoryTooLarge) ||
		errors.Is(err, errConversationLarge) ||
		errors.Is(err, errConversationChanged)
}

func historyRunFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrHistoryCancelled) {
		historyCancelFailure(w)
		return
	}
	fail(w, http.StatusInternalServerError, errors.New("no se pudo guardar el recibo del historial"))
}

func historyCancelFailure(w http.ResponseWriter) {
	fail(w, http.StatusConflict, ErrHistoryCancelled)
}

func historyImportFailure(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "Foundry no pudo conservar la conversación"
	switch {
	case errors.Is(err, ErrHistoryIdentityChanged):
		status, message = http.StatusConflict, "la identidad SSH cambió durante la sincronización"
	case errors.Is(err, ErrHistoryTooLarge):
		status, message = http.StatusRequestEntityTooLarge, "la conversación supera el límite de Foundry"
	case errors.Is(err, ErrHistoryIncomplete):
		status, message = http.StatusUnprocessableEntity, "la conversación está vacía, incompleta o ilegible; se conserva la revisión anterior"
	case errors.Is(err, ErrHistoryUnsupported):
		status, message = http.StatusUnprocessableEntity, "el adaptador remoto no es compatible"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusConflict, "la sincronización se interrumpió; se conserva la revisión anterior"
	}
	fail(w, status, errors.New(message))
}

const (
	// historyAutoDefaultInterval paces server-side capture when the caller
	// passes a non-positive interval. One page per host per tick keeps every
	// run bounded; more than historyPageSize files simply take more ticks.
	historyAutoDefaultInterval = 2 * time.Minute
	// historyAutoMaxHosts bounds concurrent hosts per tick.
	historyAutoMaxHosts = 2
	// historyAutoMaxPages bounds pages per host per tick.
	historyAutoMaxPages = 8
)

type historyAutoLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartHistoryAutoSync begins server-side capture for every consented host: one
// paginated pass per host per tick, without any open browser. It reuses the
// HistoryImporter service and the same consent/identity/cancellation rules as
// the manual sync: revoking a source, changing identity, or closing the app
// cancels the in-flight page before it can publish. Idempotent: a running loop
// is kept and the call is a no-op. Stop with StopHistoryAutoSync or by
// cancelling ctx.
func (s *Server) StartHistoryAutoSync(ctx context.Context, interval time.Duration) {
	if s == nil || ctx == nil {
		return
	}
	if interval <= 0 {
		interval = historyAutoDefaultInterval
	}
	s.historyAutoMu.Lock()
	if s.historyAuto != nil {
		s.historyAutoMu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	loop := &historyAutoLoop{cancel: cancel, done: make(chan struct{})}
	s.historyAuto = loop
	s.historyAutoMu.Unlock()
	go s.historyAutoRun(loopCtx, loop, interval)
}

// StopHistoryAutoSync halts the automatic capture loop; an in-flight page
// finishes its current file and still observes revocation before publishing.
// No-op when no loop runs.
func (s *Server) StopHistoryAutoSync() {
	s.historyAutoMu.Lock()
	loop := s.historyAuto
	s.historyAuto = nil
	s.historyAutoMu.Unlock()
	if loop == nil {
		return
	}
	loop.cancel()
	<-loop.done
}

// StartHistoryAutoSync wires the server-side capture loop for main: one call
// after SetHistoryImporter, paired with StopHistoryAutoSync before Close.
func (m *Module) StartHistoryAutoSync(ctx context.Context, interval time.Duration) {
	if m == nil || m.server == nil {
		return
	}
	m.server.StartHistoryAutoSync(ctx, interval)
}

// StopHistoryAutoSync halts the capture loop started by StartHistoryAutoSync.
func (m *Module) StopHistoryAutoSync() {
	if m == nil || m.server == nil {
		return
	}
	m.server.StopHistoryAutoSync()
}

func (s *Server) historyAutoRun(ctx context.Context, loop *historyAutoLoop, interval time.Duration) {
	defer close(loop.done)
	// Only clear the slot when the exiting loop is still the current one: a
	// newer Start after a Stop must never be reset by the old loop.
	defer func() {
		s.historyAutoMu.Lock()
		if s.historyAuto == loop {
			s.historyAuto = nil
		}
		s.historyAutoMu.Unlock()
	}()
	s.historyAutoTick(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.historyAutoTick(ctx)
		}
	}
}

func (s *Server) historyAutoTick(ctx context.Context) {
	if s == nil || s.SSH == nil || s.HistoryImporter == nil || s.Store == nil {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	hosts, err := s.Store.Hosts()
	if err != nil {
		return
	}
	sem := make(chan struct{}, historyAutoMaxHosts)
	var wg sync.WaitGroup
tick:
	for _, host := range hosts {
		select {
		case <-ctx.Done():
			break tick
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			s.historyAutoSyncHost(ctx, id)
		}(host.ID)
	}
	wg.Wait()
}

// historyAutoSyncHost captures up to historyAutoMaxPages for one host, skipping
// it when consent, identity, or availability do not allow capture. Files
// already imported for the current identity are not re-imported; revocation or
// an identity change mid-run cancels before any further receipt is published.
func (s *Server) historyAutoSyncHost(ctx context.Context, id string) {
	state := s.historyState(id)
	if !state.tryAcquireSync() {
		return
	}
	defer state.releaseSync()
	if ctx.Err() != nil {
		return
	}
	_, _ = s.runHistoryPass(ctx, id, state, historyPassOptions{auto: true, pages: historyAutoMaxPages})
}

// historySaveError persists the last capture failure for the status surface; a
// later successful commit clears it. Revoked or superseded runs save nothing.
func (s *Server) historySaveError(id string, state *hostHistoryState, run *historyRun, generation uint64, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	_, record, _, runErr := historyRecordForRunLocked(s, id, state, run, generation)
	if runErr != nil {
		return
	}
	record.Error = truncate(err.Error(), 300)
	_ = s.saveHistoryRecord(id, record)
}
