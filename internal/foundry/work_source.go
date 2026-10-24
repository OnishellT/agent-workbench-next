// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

// workSourceID is the identity of this installation's own Work source. The legacy runner
// remembered the id it had generated so a moved state root relinked it; a fixed id is that
// memory without another table.
const workSourceID = "work-sessions"

// EnsureWorkSource registers this installation's own Work transcript directory as a
// canonical source: new work produced inside the app stays capturable and the operator
// never has to know an internal path. It registers that directory alone — no discovery,
// no scan, no HOME — and keeps the source's id, receipts, sessions and pause state when
// the state root moves.
func (m *Module) EnsureWorkSource(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	root, apiErr := validateSourceRoot(dir)
	if apiErr != nil {
		return errors.New("work source directory is unusable: " + apiErr.Code)
	}
	// A source already holding this path is the operator's own registration, or this one
	// from a previous start: capture works either way, so it is left untouched.
	var taken int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources WHERE path=?`, root).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return nil
	}
	var current string
	err := m.db.QueryRowContext(ctx, `SELECT path FROM sources WHERE id=?`, workSourceID).Scan(&current)
	if err == nil {
		_, err = m.db.ExecContext(ctx, `UPDATE sources SET path=? WHERE id=?`, root, workSourceID)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	coverage, err := jsonText(CoverageSummary{Status: "unknown"})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES(?,?,?,?,?,?)`, workSourceID, root, string(AdapterCanonical), 1, nowSeconds(), coverage)
	return err
}
