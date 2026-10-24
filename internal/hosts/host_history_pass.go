package hosts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

// historyStage names where a history pass failed, so the manual route can map
// it to an HTTP status and the automatic loop can decide what to persist.
type historyStage int

const (
	historyStageBegin       historyStage = iota // consent / identity snapshot
	historyStageOpen                            // SSH + SFTP open
	historyStageCatalog                         // remote catalog page
	historyStageInvalidPage                     // catalog violated its contract
	historyStageInvalidFile                     // catalog entry not importable
	historyStageCheck                           // run revoked / superseded
	historyStageRead                            // remote file read
	historyStageImport                          // HistoryImporter
	historyStageConfirm                         // importer returned no session
	historyStageReceipt                         // saving the receipt
	historyStageCommit                          // saving the cursor
)

type historyFailure struct {
	stage historyStage
	err   error
}

func (f *historyFailure) Error() string { return f.err.Error() }
func (f *historyFailure) Unwrap() error { return f.err }

func failHistory(stage historyStage, err error) *historyFailure {
	return &historyFailure{stage: stage, err: err}
}

// historyBeginError is a begin-stage rejection that carries its HTTP answer.
type historyBeginError struct {
	status int
	msg    string
}

func (e *historyBeginError) Error() string { return e.msg }

// historyPassOptions selects the manual or automatic flavour of a pass.
type historyPassOptions struct {
	// auto skips files whose receipt is current, ignores uncatalogable
	// entries and keeps going past per-file failures (they stay visible in
	// the status error); the manual pass re-imports and stops at the first
	// failure so the operator sees it.
	auto bool
	// pages bounds the pages walked in one pass.
	pages int
}

type historyPassResult struct {
	changed int
}

// historyPass is one registered, consent-checked run for a host.
type historyPass struct {
	s          *Server
	id         string
	state      *hostHistoryState
	run        *historyRun
	generation uint64
	identity   Identity
	record     hostHistoryRecord
	reader     *conversationSSH
	opts       historyPassOptions
	// skipErr is the last per-file failure the pass moved past; it is
	// re-asserted after every commit, including those of later clean pages.
	skipErr error
}

// acquireSync waits for the per-host sync slot, giving up when ctx ends.
func (state *hostHistoryState) acquireSync(ctx context.Context) bool {
	select {
	case state.sync <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (state *hostHistoryState) tryAcquireSync() bool {
	select {
	case state.sync <- struct{}{}:
		return true
	default:
		return false
	}
}

func (state *hostHistoryState) releaseSync() { <-state.sync }

// runHistoryPass is the single implementation behind manual sync and the
// automatic loop. The caller holds the host's sync slot. It registers a run
// (cancelling any previous one), opens one conversation read and walks up to
// opts.pages pages, honouring consent, identity and cancellation before every
// publish. A nil failure means the walk finished or reached its page bound.
func (s *Server) runHistoryPass(ctx context.Context, id string, state *hostHistoryState, opts historyPassOptions) (historyPassResult, *historyFailure) {
	var result historyPassResult
	runCtx, cancel := context.WithCancel(ctx)
	run := &historyRun{ctx: runCtx, cancel: cancel}
	defer cancel()
	pass := &historyPass{s: s, id: id, state: state, run: run, opts: opts}
	if f := pass.begin(); f != nil {
		return result, f
	}
	defer func() {
		cancel()
		state.mu.Lock()
		if state.active == run {
			state.active = nil
		}
		state.mu.Unlock()
	}()
	if err := runCtx.Err(); err != nil {
		return result, failHistory(historyStageOpen, err)
	}
	reader, err := s.SSH.openConversations(runCtx, id)
	if err != nil {
		return result, failHistory(historyStageOpen, err)
	}
	defer reader.close()
	pass.reader = reader
	cursor := pass.record.Cursor
	for range max(opts.pages, 1) {
		next, changed, f := pass.page(cursor)
		result.changed += changed
		if f != nil {
			return result, f
		}
		if next == "" || next == cursor {
			return result, nil
		}
		cursor = next
		if runCtx.Err() != nil {
			return result, nil
		}
	}
	return result, nil
}

// begin snapshots consent and identity under state.mu and registers the run.
func (p *historyPass) begin() *historyFailure {
	s, id, state := p.s, p.id, p.state
	state.mu.Lock()
	defer state.mu.Unlock()
	status, err := s.historyStatusLocked(id, state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return failHistory(historyStageBegin, &historyBeginError{http.StatusNotFound, "host SSH no registrado"})
		}
		return failHistory(historyStageBegin, &historyBeginError{http.StatusInternalServerError, "no se pudo consultar el historial"})
	}
	if !status.Enabled {
		if status.State == "disabled" {
			return failHistory(historyStageBegin, &historyBeginError{http.StatusForbidden, "el historial remoto no está autorizado"})
		}
		return failHistory(historyStageBegin, &historyBeginError{http.StatusConflict, "la identidad SSH no permite leer el historial"})
	}
	host, err := s.Store.Host(id)
	if err != nil {
		return failHistory(historyStageBegin, &historyBeginError{http.StatusNotFound, "host SSH no registrado"})
	}
	identity, err := historyIdentity(host)
	if err != nil || !host.Enabled {
		return failHistory(historyStageBegin, &historyBeginError{http.StatusConflict, "la identidad SSH no permite leer el historial"})
	}
	record, err := s.readHistoryRecord(id)
	if err != nil {
		return failHistory(historyStageBegin, &historyBeginError{http.StatusInternalServerError, "no se pudo consultar el historial"})
	}
	if !record.Enabled || record.Identity != identity.Digest {
		return failHistory(historyStageBegin, ErrHistoryCancelled)
	}
	if state.active != nil {
		state.active.cancel()
		state.active = nil
	}
	p.generation = state.generation
	state.active = p.run
	p.identity, p.record = identity, record
	return nil
}

func (p *historyPass) check() error {
	return p.s.historyRunCheck(p.id, p.state, p.run, p.generation)
}

func (p *historyPass) saveError(err error) {
	p.s.historySaveError(p.id, p.state, p.run, p.generation, err)
}

// catalogPage fetches one catalog page, restarting from the top when a stale
// cursor no longer matches the registered sources, and validates its contract.
func (p *historyPass) catalogPage(cursor string) (conversationCatalog, *historyFailure) {
	page, err := p.reader.catalog(cursor, historyPageSize)
	if err != nil && cursor != "" && errors.Is(err, errConversationPath) {
		cursor = ""
		page, err = p.reader.catalog("", historyPageSize)
	}
	if err != nil {
		return page, failHistory(historyStageCatalog, err)
	}
	if err := p.check(); err != nil {
		return page, failHistory(historyStageCheck, err)
	}
	if page.HostID != p.id || len(page.Files) > historyPageSize || page.NextCursor != nil && (*page.NextCursor == cursor || len(*page.NextCursor) > 8192) {
		return page, failHistory(historyStageInvalidPage, errors.New("catálogo de historial remoto inválido"))
	}
	return page, nil
}

// page imports one catalog page and commits its cursor. It returns the next
// cursor (empty at the end of the catalog) and how many files changed.
func (p *historyPass) page(cursor string) (next string, changed int, failure *historyFailure) {
	defer func() {
		if failure != nil && p.opts.auto {
			p.recordAutoFailure(failure)
		}
	}()
	if err := p.check(); err != nil {
		return "", 0, failHistory(historyStageCheck, err)
	}
	page, f := p.catalogPage(cursor)
	if f != nil {
		return "", 0, f
	}
	for _, file := range page.Files {
		if err := p.check(); err != nil {
			return "", changed, failHistory(historyStageCheck, err)
		}
		fileChanged, skipped, f := p.importFile(file)
		if f != nil {
			return "", changed, f
		}
		if skipped != nil {
			p.skipErr = skipped
			continue
		}
		if fileChanged {
			changed++
		}
	}
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	if err := p.s.historyCommit(p.id, p.state, p.run, p.generation, next); err != nil {
		return "", changed, failHistory(historyStageCommit, err)
	}
	if p.skipErr != nil {
		// The commit clears the visible error; re-save the skipped file
		// failure so the status keeps showing it until the file imports.
		p.saveError(p.skipErr)
	}
	return next, changed, nil
}

// importFile reads and imports one catalog entry. skipped is non-nil only in
// the automatic flavour, for per-file failures that must not stop the pass.
func (p *historyPass) importFile(file conversationFile) (changed bool, skipped error, failure *historyFailure) {
	lineage := historyLineage(p.identity.Digest, file.SourceID, file.Path)
	if p.opts.auto {
		receipt, seen := p.record.Files[lineage]
		if historyReceiptCurrent(receipt, seen, file) || !historyEntryValid(file) {
			return false, nil, nil
		}
	} else if !historyEntryValid(file) {
		return false, nil, failHistory(historyStageInvalidFile, errors.New("el catálogo remoto contiene una fuente no compatible"))
	}
	raw, err := p.reader.file(file.SourceID, file.Path, file.SourceRevision)
	if err != nil {
		clear(raw)
		return p.fileFailure(historyStageRead, err)
	}
	if err := p.check(); err != nil {
		clear(raw)
		return false, nil, failHistory(historyStageCheck, err)
	}
	result, err := p.s.HistoryImporter.ImportHostConversation(p.run.ctx, HistoryImport{Identity: p.identity, SourceID: file.SourceID, SourceRevision: file.SourceRevision, Adapter: file.Adapter, Path: file.Path, Mtime: file.Mtime}, raw)
	clear(raw)
	if err != nil {
		return p.fileFailure(historyStageImport, err)
	}
	if result.SessionID == "" || result.Revision == "" {
		return false, nil, failHistory(historyStageConfirm, errors.New("Foundry no confirmó la conversación"))
	}
	receipt := historyReceipt{SourceRevision: file.SourceRevision, Adapter: file.Adapter, SessionID: result.SessionID, Revision: result.Revision, Mtime: file.Mtime, Size: file.Size}
	if err := p.s.historySaveReceipt(p.id, p.state, p.run, p.generation, lineage, receipt); err != nil {
		return false, nil, failHistory(historyStageReceipt, err)
	}
	p.record.Files[lineage] = receipt
	return result.Changed, nil, nil
}

// fileFailure classifies a read/import error: the automatic flavour keeps going
// past per-file skip errors (saving them), the manual one stops.
func (p *historyPass) fileFailure(stage historyStage, err error) (bool, error, *historyFailure) {
	if p.opts.auto && historySkipFileError(err) {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrHistoryCancelled) {
			p.saveError(err)
		}
		return false, err, nil
	}
	return false, nil, failHistory(stage, err)
}

// recordAutoFailure persists the failure the status surface should show. The
// automatic loop has no HTTP caller, so this is its only report channel.
// Cancellations, revocations and run-check failures save nothing.
func (p *historyPass) recordAutoFailure(f *historyFailure) {
	switch f.stage {
	case historyStageCatalog, historyStageInvalidPage, historyStageRead, historyStageImport, historyStageConfirm:
		if errors.Is(f.err, context.Canceled) || errors.Is(f.err, ErrHistoryCancelled) {
			return
		}
		p.saveError(f.err)
	}
}

// writeHistoryFailure maps a pass failure to the manual sync HTTP answer.
func writeHistoryFailure(w http.ResponseWriter, f *historyFailure) {
	var begin *historyBeginError
	canceled := errors.Is(f.err, context.Canceled)
	switch f.stage {
	case historyStageBegin:
		if errors.As(f.err, &begin) {
			fail(w, begin.status, errors.New(begin.msg))
			return
		}
		historyCancelFailure(w)
	case historyStageOpen, historyStageCatalog, historyStageRead:
		if canceled {
			historyCancelFailure(w)
			return
		}
		conversationFailure(w, f.err)
	case historyStageInvalidPage:
		fail(w, http.StatusBadGateway, f.err)
	case historyStageInvalidFile:
		fail(w, http.StatusUnprocessableEntity, f.err)
	case historyStageImport:
		if canceled || errors.Is(f.err, ErrHistoryCancelled) {
			historyCancelFailure(w)
			return
		}
		historyImportFailure(w, f.err)
	case historyStageConfirm:
		fail(w, http.StatusInternalServerError, f.err)
	default: // check, receipt, commit
		historyRunFailure(w, f.err)
	}
}
