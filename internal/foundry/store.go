// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 4

const schemaSQL = `
PRAGMA busy_timeout=5000;
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS schema_info (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  version INTEGER NOT NULL
);
INSERT OR IGNORE INTO schema_info(singleton,version) VALUES(1,2);
CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  adapter TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
  created REAL NOT NULL,
  last_capture REAL,
  last_scan_at REAL,
  last_scan_status TEXT NOT NULL DEFAULT 'never',
  last_scan_files INTEGER NOT NULL DEFAULT 0,
  last_scan_changed INTEGER NOT NULL DEFAULT 0,
  last_scan_errors INTEGER NOT NULL DEFAULT 0,
  last_scan_max_files INTEGER NOT NULL DEFAULT 0,
  last_scan_current_file TEXT NOT NULL DEFAULT '',
  last_scan_limited INTEGER NOT NULL DEFAULT 0,
  coverage_json TEXT NOT NULL DEFAULT '{"status":"unknown"}',
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id),
  path TEXT NOT NULL,
  title TEXT NOT NULL,
  revision TEXT NOT NULL,
  updated REAL NOT NULL,
  captured_at REAL NOT NULL,
  activity_at REAL NOT NULL,
  completion TEXT NOT NULL DEFAULT 'unknown',
  coverage_json TEXT NOT NULL,
  host_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated DESC,id);
CREATE INDEX IF NOT EXISTS sessions_source ON sessions(source_id,updated DESC);
CREATE INDEX IF NOT EXISTS sessions_host ON sessions(host_id,updated DESC);
CREATE TABLE IF NOT EXISTS revisions (
  session_id TEXT NOT NULL REFERENCES sessions(id),
  revision TEXT NOT NULL,
  raw_sha256 TEXT NOT NULL,
  created REAL NOT NULL,
  coverage_json TEXT NOT NULL,
  PRIMARY KEY(session_id,revision)
);
CREATE INDEX IF NOT EXISTS revisions_created ON revisions(session_id,created DESC);
CREATE TABLE IF NOT EXISTS events (
  session_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  event_index INTEGER NOT NULL CHECK(event_index>=0),
  event_id TEXT NOT NULL,
  source_line INTEGER NOT NULL,
  role TEXT NOT NULL,
  kind TEXT NOT NULL,
  text TEXT NOT NULL,
  timestamp_json TEXT NOT NULL,
  channel TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL DEFAULT '',
  call_id TEXT NOT NULL DEFAULT '',
  attachments_json TEXT NOT NULL DEFAULT '[]',
  evidence_json TEXT NOT NULL,
  inert INTEGER NOT NULL CHECK(inert IN (0,1)),
  completion_suggested INTEGER NOT NULL CHECK(completion_suggested IN (0,1)),
  native_type TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(session_id,revision,event_index),
  FOREIGN KEY(session_id,revision) REFERENCES revisions(session_id,revision)
);
CREATE INDEX IF NOT EXISTS events_role ON events(session_id,revision,role,event_index);
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  first_event INTEGER NOT NULL,
  last_event INTEGER NOT NULL,
  title TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT 'pending' CHECK(label IN ('pending','correct','incorrect')),
  feedback TEXT NOT NULL DEFAULT '',
  completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN (0,1)),
  completion_generation INTEGER NOT NULL DEFAULT 0,
  completion_confirmed_at REAL,
  created REAL NOT NULL,
  updated REAL NOT NULL,
  FOREIGN KEY(session_id,revision) REFERENCES revisions(session_id,revision)
);
CREATE INDEX IF NOT EXISTS tasks_revision ON tasks(session_id,revision,first_event,last_event,created);
CREATE INDEX IF NOT EXISTS tasks_label ON tasks(label,completed,updated DESC);
CREATE TABLE IF NOT EXISTS task_citations (
  task_id TEXT NOT NULL REFERENCES tasks(id),
  citation_index INTEGER NOT NULL,
  event_index INTEGER NOT NULL,
  quote TEXT NOT NULL,
  PRIMARY KEY(task_id,citation_index)
);
CREATE TABLE IF NOT EXISTS task_label_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  generation INTEGER NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('label','feedback')),
  label TEXT NOT NULL,
  feedback TEXT NOT NULL,
  created REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS task_label_history_order ON task_label_history(task_id,id);
CREATE TABLE IF NOT EXISTS task_completion_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  generation INTEGER NOT NULL,
  completed INTEGER NOT NULL CHECK(completed IN (0,1)),
  confirmed_at REAL,
  revoked_generation INTEGER,
  created REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS task_completion_history_order ON task_completion_history(task_id,id);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  action TEXT NOT NULL,
  detail_json TEXT NOT NULL,
  created REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_entity ON audit(entity,entity_id,id);
CREATE TRIGGER IF NOT EXISTS revisions_no_update BEFORE UPDATE ON revisions BEGIN SELECT RAISE(ABORT,'immutable revisions'); END;
CREATE TRIGGER IF NOT EXISTS revisions_no_delete BEFORE DELETE ON revisions BEGIN SELECT RAISE(ABORT,'immutable revisions'); END;
CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'immutable events'); END;
CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'immutable events'); END;
CREATE TRIGGER IF NOT EXISTS task_citations_no_update BEFORE UPDATE ON task_citations BEGIN SELECT RAISE(ABORT,'immutable task citations'); END;
CREATE TRIGGER IF NOT EXISTS task_citations_no_delete BEFORE DELETE ON task_citations BEGIN SELECT RAISE(ABORT,'immutable task citations'); END;
CREATE TRIGGER IF NOT EXISTS task_label_history_no_update BEFORE UPDATE ON task_label_history BEGIN SELECT RAISE(ABORT,'immutable task label history'); END;
CREATE TRIGGER IF NOT EXISTS task_label_history_no_delete BEFORE DELETE ON task_label_history BEGIN SELECT RAISE(ABORT,'immutable task label history'); END;
CREATE TRIGGER IF NOT EXISTS task_completion_history_no_update BEFORE UPDATE ON task_completion_history BEGIN SELECT RAISE(ABORT,'immutable task completion history'); END;
CREATE TRIGGER IF NOT EXISTS task_completion_history_no_delete BEFORE DELETE ON task_completion_history BEGIN SELECT RAISE(ABORT,'immutable task completion history'); END;
CREATE TRIGGER IF NOT EXISTS audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT,'immutable audit'); END;
CREATE TRIGGER IF NOT EXISTS audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT,'immutable audit'); END;
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('scan','propose_tasks')),
  subject TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
  files INTEGER NOT NULL DEFAULT 0,
  changed INTEGER NOT NULL DEFAULT 0,
  errors INTEGER NOT NULL DEFAULT 0,
  max_files INTEGER NOT NULL DEFAULT 0,
  current_file TEXT NOT NULL DEFAULT '',
  limited INTEGER NOT NULL DEFAULT 0 CHECK(limited IN (0,1)),
  result_json TEXT,
  error TEXT NOT NULL DEFAULT '',
  created REAL NOT NULL,
  updated REAL NOT NULL,
  progress_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status,created DESC);
CREATE TABLE IF NOT EXISTS task_settings (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  auto_tasks INTEGER NOT NULL DEFAULT 0 CHECK(auto_tasks IN (0,1)),
  auto_tasks_since REAL,
  task_idle_seconds INTEGER NOT NULL DEFAULT 120 CHECK(task_idle_seconds BETWEEN 15 AND 86400),
  max_input_chars INTEGER NOT NULL DEFAULT 160000 CHECK(max_input_chars BETWEEN 1000 AND 500000),
  max_calls INTEGER NOT NULL DEFAULT 36 CHECK(max_calls BETWEEN 2 AND 200),
  experiment_seconds REAL NOT NULL DEFAULT 3600 CHECK(experiment_seconds BETWEEN 30 AND 28800),
  updated REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS task_provider_configs (
  role TEXT PRIMARY KEY CHECK(role IN ('provider','replay')),
  kind TEXT NOT NULL CHECK(kind IN ('pi','codex','omp','command')),
  command_json TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  timeout_seconds REAL NOT NULL DEFAULT 180 CHECK(timeout_seconds BETWEEN 5 AND 1800),
  real_inference INTEGER NOT NULL DEFAULT 0 CHECK(real_inference IN (0,1)),
  model_provider TEXT NOT NULL DEFAULT '',
  reasoning TEXT NOT NULL DEFAULT 'medium',
  updated REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS task_proposal_receipts (
  job_id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  algorithm TEXT NOT NULL,
  provider_hash TEXT NOT NULL,
  identity_hash TEXT NOT NULL,
  automatic INTEGER NOT NULL CHECK(automatic IN (0,1)),
  status TEXT NOT NULL CHECK(status IN ('queued','running','completed','failed','cancelled','stale','interrupted')),
  attempt TEXT NOT NULL,
  started_at REAL NOT NULL,
  lease_until REAL NOT NULL,
  finished_at REAL,
  suggestions_json TEXT NOT NULL DEFAULT '[]',
  skipped_json TEXT NOT NULL DEFAULT '[]',
  coverage_json TEXT NOT NULL DEFAULT '{}',
  inference_json TEXT NOT NULL DEFAULT '{}',
  error TEXT NOT NULL DEFAULT '',
  progress_completed INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER NOT NULL DEFAULT 0,
  created REAL NOT NULL,
  updated REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS task_proposal_receipts_session ON task_proposal_receipts(session_id,revision,created DESC);
CREATE UNIQUE INDEX IF NOT EXISTS task_proposal_auto_once ON task_proposal_receipts(session_id,revision) WHERE automatic=1;
CREATE TABLE IF NOT EXISTS task_proposal_budgets (
  identity_hash TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  revision TEXT NOT NULL,
  provider_hash TEXT NOT NULL,
  max_input_chars INTEGER NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  seconds REAL NOT NULL DEFAULT 0,
  runs_json TEXT NOT NULL DEFAULT '[]',
  updated REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS task_proposal_checkpoints (
  identity_hash TEXT NOT NULL,
  chunk_index INTEGER NOT NULL,
  prompt_hash TEXT NOT NULL,
  observations_json TEXT NOT NULL,
  fragments_json TEXT NOT NULL,
  PRIMARY KEY(identity_hash,chunk_index)
);
CREATE TABLE IF NOT EXISTS task_proposal_results (
  identity_hash TEXT PRIMARY KEY,
  tasks_json TEXT NOT NULL,
  created REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS task_proposal_decisions (
  job_id TEXT NOT NULL,
  suggestion_index INTEGER NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('accepted','skipped')),
  task_id TEXT,
  reason TEXT NOT NULL DEFAULT '',
  created REAL NOT NULL,
  PRIMARY KEY(job_id,suggestion_index)
);
CREATE TRIGGER IF NOT EXISTS task_proposal_decisions_no_update BEFORE UPDATE ON task_proposal_decisions BEGIN SELECT RAISE(ABORT,'immutable proposal decisions'); END;
CREATE TRIGGER IF NOT EXISTS task_proposal_decisions_no_delete BEFORE DELETE ON task_proposal_decisions BEGIN SELECT RAISE(ABORT,'immutable proposal decisions'); END;
`

func openStore(stateDir string) (*sql.DB, error) {
	if stateDir == "" {
		return nil, errors.New("Foundry state directory is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	stateInfo, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if stateInfo.Mode()&os.ModeSymlink != 0 || !stateInfo.IsDir() {
		return nil, errors.New("Foundry state directory must be a real directory")
	}
	dir := filepath.Join(abs, "foundry")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, errors.New("Foundry storage directory must not be a symlink")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "foundry.sqlite")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("Foundry database must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Pragmas travel in the DSN so they apply to every connection the pool opens, not
	// only to the one that ran schemaSQL.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=journal_mode(WAL)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single connection: serialises all access. Never run a second query on m.db while
	// a *sql.Rows or *sql.Tx is open in the same goroutine (it would wait forever);
	// drain/close rows first and use the tx for statements inside a transaction.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(err error) (*sql.DB, error) {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return fail(err)
	}
	if err := db.Ping(); err != nil {
		return fail(err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_info WHERE singleton=1`).Scan(&version); err != nil {
		return fail(err)
	}
	if version == 1 {
		if err := migrateV1ToV2(db); err != nil {
			return fail(err)
		}
		version = 2
	}
	if version == 2 {
		if err := migrateV2ToV3(db); err != nil {
			return fail(err)
		}
		version = 3
	}
	if version == 3 {
		if err := migrateV3ToV4(db); err != nil {
			return fail(err)
		}
		version = 4
	}
	if version != schemaVersion {
		return fail(fmt.Errorf("unsupported Foundry schema version %d (want %d)", version, schemaVersion))
	}
	if _, err := db.Exec(learningSchemaSQL); err != nil {
		return fail(err)
	}
	if err := initializeLearningStore(db, dir); err != nil {
		return fail(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fail(err)
	}
	return db, nil
}

func migrateV1ToV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		"DROP INDEX IF EXISTS jobs_status",
		"ALTER TABLE jobs RENAME TO jobs_v1",
		`CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('scan','propose_tasks')),
  subject TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
  files INTEGER NOT NULL DEFAULT 0,
  changed INTEGER NOT NULL DEFAULT 0,
  errors INTEGER NOT NULL DEFAULT 0,
  max_files INTEGER NOT NULL DEFAULT 0,
  current_file TEXT NOT NULL DEFAULT '',
  limited INTEGER NOT NULL DEFAULT 0 CHECK(limited IN (0,1)),
  result_json TEXT,
  error TEXT NOT NULL DEFAULT '',
  created REAL NOT NULL,
  updated REAL NOT NULL,
  progress_json TEXT NOT NULL DEFAULT '{}'
)
`,
		"INSERT INTO jobs(id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated,progress_json) SELECT id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated,'{}' FROM jobs_v1",
		"DROP TABLE jobs_v1",
		"CREATE INDEX jobs_status ON jobs(status,created DESC)",
		"UPDATE schema_info SET version=2 WHERE singleton=1",
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateV3ToV4 widens the stored engine kinds: the proposal analysis can run on
// this installation's own OMP through the shared harness. The table is rebuilt
// because SQLite cannot widen a CHECK in place.
func migrateV3ToV4(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		"ALTER TABLE task_provider_configs RENAME TO task_provider_configs_v3",
		`CREATE TABLE task_provider_configs (
  role TEXT PRIMARY KEY CHECK(role IN ('provider','replay')),
  kind TEXT NOT NULL CHECK(kind IN ('pi','codex','omp','command')),
  command_json TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  timeout_seconds REAL NOT NULL DEFAULT 180 CHECK(timeout_seconds BETWEEN 5 AND 1800),
  real_inference INTEGER NOT NULL DEFAULT 0 CHECK(real_inference IN (0,1)),
  model_provider TEXT NOT NULL DEFAULT '',
  reasoning TEXT NOT NULL DEFAULT 'medium',
  updated REAL NOT NULL
)`,
		"INSERT INTO task_provider_configs(role,kind,command_json,model,timeout_seconds,real_inference,model_provider,reasoning,updated) SELECT role,kind,command_json,model,timeout_seconds,real_inference,model_provider,reasoning,updated FROM task_provider_configs_v3",
		"DROP TABLE task_provider_configs_v3",
		"UPDATE schema_info SET version=4 WHERE singleton=1",
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func jsonText(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func appendAudit(ctx context.Context, tx *sql.Tx, entity, entityID, action string, detail any) error {
	payload, err := jsonText(detail)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO audit(entity,entity_id,action,detail_json,created) VALUES(?,?,?,?,?)`,
		entity, entityID, action, payload, time.Now().UnixNano()/1e9,
	)
	return err
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// queryAll runs a query and scans every row with scan, closing the rows before it
// returns (the store has a single connection, so callers must never keep rows open
// while issuing another query).
func queryAll[T any](ctx context.Context, q rowQuerier, scan func(*sql.Rows) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
