// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"
)

// learningSchemaSQL extends the existing Foundry database with typed domain tables.
// Conversation events and task decisions remain in their existing immutable schema;
// this is not a second store or a generic JSON-record table.
const learningSchemaSQL = `
CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  spec_json TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('proposed','evaluating','evaluated','discarded','inconclusive','accepted','stale')),
  base_version TEXT,
  created REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS candidates_task_created ON candidates(task_id,created DESC,id);
CREATE TABLE IF NOT EXISTS candidate_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  candidate_id TEXT NOT NULL REFERENCES candidates(id),
  action TEXT NOT NULL,
  before_hash TEXT NOT NULL DEFAULT '',
  after_hash TEXT NOT NULL DEFAULT '',
  detail_json TEXT NOT NULL DEFAULT '{}',
  created REAL NOT NULL
);
CREATE TRIGGER IF NOT EXISTS candidate_history_no_update BEFORE UPDATE ON candidate_history BEGIN SELECT RAISE(ABORT,'immutable candidate history'); END;
CREATE TRIGGER IF NOT EXISTS candidate_history_no_delete BEFORE DELETE ON candidate_history BEGIN SELECT RAISE(ABORT,'immutable candidate history'); END;
CREATE TABLE IF NOT EXISTS cases (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  prompt TEXT NOT NULL,
  snapshot_json TEXT NOT NULL,
  verify_json TEXT NOT NULL,
  positive_control_json TEXT NOT NULL,
  negative_control_json TEXT NOT NULL,
  domain TEXT NOT NULL,
  split TEXT NOT NULL CHECK(split IN ('origin','holdout','regression')),
  created REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS cases_split_domain ON cases(split,domain,created DESC);
CREATE TRIGGER IF NOT EXISTS cases_no_update BEFORE UPDATE ON cases BEGIN SELECT RAISE(ABORT,'immutable case snapshots'); END;
CREATE TRIGGER IF NOT EXISTS cases_no_delete BEFORE DELETE ON cases BEGIN SELECT RAISE(ABORT,'immutable case snapshots'); END;
CREATE TABLE IF NOT EXISTS learning_case_requests (
  job_id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  prompt TEXT NOT NULL,
  project_path TEXT NOT NULL,
  verifier_path TEXT NOT NULL,
  git_ref TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL,
  split TEXT NOT NULL,
  verify_json TEXT NOT NULL,
  positive_control_json TEXT NOT NULL,
  negative_control_json TEXT NOT NULL,
	  exclude_json TEXT NOT NULL DEFAULT '[]',
  task_id TEXT NOT NULL DEFAULT '',
  expected_revision TEXT NOT NULL DEFAULT '',
  created REAL NOT NULL
);
CREATE TRIGGER IF NOT EXISTS learning_case_requests_no_update BEFORE UPDATE ON learning_case_requests BEGIN SELECT RAISE(ABORT,'immutable case job request'); END;
CREATE TRIGGER IF NOT EXISTS learning_case_requests_no_delete BEFORE DELETE ON learning_case_requests BEGIN SELECT RAISE(ABORT,'immutable case job request'); END;
CREATE TABLE IF NOT EXISTS task_case_selections (
  task_id TEXT NOT NULL REFERENCES tasks(id),
  generation INTEGER NOT NULL,
  selection_hash TEXT NOT NULL,
  created REAL NOT NULL,
  PRIMARY KEY(task_id,generation)
);
CREATE TABLE IF NOT EXISTS task_case_selection_items (
  task_id TEXT NOT NULL,
  generation INTEGER NOT NULL,
  case_id TEXT NOT NULL REFERENCES cases(id),
  position INTEGER NOT NULL,
  PRIMARY KEY(task_id,generation,case_id),
  UNIQUE(task_id,generation,position),
  FOREIGN KEY(task_id,generation) REFERENCES task_case_selections(task_id,generation)
);
CREATE TRIGGER IF NOT EXISTS task_case_selections_no_update BEFORE UPDATE ON task_case_selections BEGIN SELECT RAISE(ABORT,'immutable case selection history'); END;
CREATE TRIGGER IF NOT EXISTS task_case_selections_no_delete BEFORE DELETE ON task_case_selections BEGIN SELECT RAISE(ABORT,'immutable case selection history'); END;
CREATE TRIGGER IF NOT EXISTS task_case_selection_items_no_update BEFORE UPDATE ON task_case_selection_items BEGIN SELECT RAISE(ABORT,'immutable case selection history'); END;
CREATE TRIGGER IF NOT EXISTS task_case_selection_items_no_delete BEFORE DELETE ON task_case_selection_items BEGIN SELECT RAISE(ABORT,'immutable case selection history'); END;
CREATE TABLE IF NOT EXISTS task_origins (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  case_id TEXT NOT NULL REFERENCES cases(id),
  source_revision TEXT NOT NULL,
  project_hash TEXT NOT NULL,
  project_key TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  provider_config_hash TEXT NOT NULL,
  captured_before_task INTEGER NOT NULL CHECK(captured_before_task IN (0,1)),
  provenance TEXT NOT NULL CHECK(provenance IN ('system_recorded','human_attested','reconstructed_unverified')),
  note TEXT NOT NULL,
  confirmed_at REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS task_origins_latest ON task_origins(task_id,id DESC);
CREATE TRIGGER IF NOT EXISTS task_origins_no_update BEFORE UPDATE ON task_origins BEGIN SELECT RAISE(ABORT,'immutable origin attestations'); END;
CREATE TRIGGER IF NOT EXISTS task_origins_no_delete BEFORE DELETE ON task_origins BEGIN SELECT RAISE(ABORT,'immutable origin attestations'); END;
CREATE TABLE IF NOT EXISTS experiments (
  id TEXT PRIMARY KEY,
  candidate_id TEXT NOT NULL REFERENCES candidates(id),
  base_version TEXT NOT NULL,
  report_json TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('running','passed','discarded','inconclusive','cancelled','stale')),
  created REAL NOT NULL,
  updated REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS experiments_candidate_created ON experiments(candidate_id,created DESC);
CREATE TABLE IF NOT EXISTS versions (
  id TEXT PRIMARY KEY,
  parent TEXT,
  members_json TEXT NOT NULL,
  created REAL NOT NULL,
  reason TEXT NOT NULL,
  package_hash TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER IF NOT EXISTS versions_no_update BEFORE UPDATE ON versions BEGIN SELECT RAISE(ABORT,'immutable registry versions'); END;
CREATE TRIGGER IF NOT EXISTS versions_no_delete BEFORE DELETE ON versions BEGIN SELECT RAISE(ABORT,'immutable registry versions'); END;
CREATE TABLE IF NOT EXISTS learning_registry_state (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  active_version TEXT NOT NULL REFERENCES versions(id),
  generation INTEGER NOT NULL DEFAULT 0,
  updated REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS learning_budgets (
  identity_hash TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  binding_hash TEXT NOT NULL,
  provider_hash TEXT NOT NULL,
  max_input_chars INTEGER NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  seconds REAL NOT NULL DEFAULT 0,
  runs_json TEXT NOT NULL DEFAULT '[]',
  updated REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS learning_checkpoints (
  identity_hash TEXT NOT NULL,
  stage TEXT NOT NULL CHECK(stage IN ('chunk','synthesis')),
  ordinal INTEGER NOT NULL,
  prompt_hash TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  checkpoint_hash TEXT NOT NULL,
  created REAL NOT NULL,
  PRIMARY KEY(identity_hash,stage,ordinal)
);
CREATE TRIGGER IF NOT EXISTS learning_checkpoints_no_update BEFORE UPDATE ON learning_checkpoints BEGIN SELECT RAISE(ABORT,'immutable learning checkpoints'); END;
CREATE TRIGGER IF NOT EXISTS learning_checkpoints_no_delete BEFORE DELETE ON learning_checkpoints BEGIN SELECT RAISE(ABORT,'immutable learning checkpoints'); END;
CREATE TABLE IF NOT EXISTS learning_backup_receipts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  versions INTEGER NOT NULL,
  created REAL NOT NULL
);
CREATE TRIGGER IF NOT EXISTS learning_backup_receipts_no_update BEFORE UPDATE ON learning_backup_receipts BEGIN SELECT RAISE(ABORT,'immutable backup receipts'); END;
CREATE TRIGGER IF NOT EXISTS learning_backup_receipts_no_delete BEFORE DELETE ON learning_backup_receipts BEGIN SELECT RAISE(ABORT,'immutable backup receipts'); END;
CREATE TABLE IF NOT EXISTS learning_sync_receipts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_key TEXT NOT NULL,
  version TEXT NOT NULL,
  manifest_path TEXT NOT NULL,
  manifest_sha256 TEXT NOT NULL,
  block_sha256 TEXT NOT NULL DEFAULT '',
  receipt_json TEXT NOT NULL,
  created REAL NOT NULL
);
CREATE TRIGGER IF NOT EXISTS learning_sync_receipts_no_update BEFORE UPDATE ON learning_sync_receipts BEGIN SELECT RAISE(ABORT,'immutable sync receipts'); END;
CREATE TRIGGER IF NOT EXISTS learning_sync_receipts_no_delete BEFORE DELETE ON learning_sync_receipts BEGIN SELECT RAISE(ABORT,'immutable sync receipts'); END;
`

func migrateV2ToV3(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		"DROP INDEX IF EXISTS jobs_status",
		"ALTER TABLE jobs RENAME TO jobs_v2",
		`CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('scan','propose_tasks','derive','case','evaluate')),
  subject TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted','blocked')),
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
)`,
		"INSERT INTO jobs(id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated,progress_json) SELECT id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated,progress_json FROM jobs_v2",
		"DROP TABLE jobs_v2",
		"CREATE INDEX jobs_status ON jobs(status,created DESC)",
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	columns := []struct{ name, definition string }{
		{"auto_derive", "INTEGER NOT NULL DEFAULT 1 CHECK(auto_derive IN (0,1))"},
		{"auto_evaluate", "INTEGER NOT NULL DEFAULT 0 CHECK(auto_evaluate IN (0,1))"},
		{"poll_seconds", "INTEGER NOT NULL DEFAULT 15 CHECK(poll_seconds BETWEEN 2 AND 3600)"},
		{"repetitions", "INTEGER NOT NULL DEFAULT 3 CHECK(repetitions BETWEEN 1 AND 10)"},
		{"min_quality_gain", "REAL NOT NULL DEFAULT 0.05 CHECK(min_quality_gain BETWEEN 0.001 AND 1)"},
		{"max_time_ratio", "REAL NOT NULL DEFAULT 1.25 CHECK(max_time_ratio BETWEEN 1 AND 10)"},
		{"min_efficiency_gain", "REAL NOT NULL DEFAULT 0.15 CHECK(min_efficiency_gain BETWEEN 0.01 AND 0.9)"},
		{"max_source_bytes", "INTEGER NOT NULL DEFAULT 64000000 CHECK(max_source_bytes BETWEEN 1000 AND 512000000)"},
		{"max_files_per_scan", "INTEGER NOT NULL DEFAULT 200 CHECK(max_files_per_scan BETWEEN 1 AND 1000)"},
		{"max_scan_seconds", "INTEGER NOT NULL DEFAULT 10 CHECK(max_scan_seconds BETWEEN 1 AND 120)"},
	}
	for _, column := range columns {
		if err := addSQLiteColumn(tx, "task_settings", column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE schema_info SET version=3 WHERE singleton=1`); err != nil {
		return err
	}
	return tx.Commit()
}

func addSQLiteColumn(tx *sql.Tx, table, name, definition string) error {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var index, notNull, primary int
		var column, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&index, &column, &kind, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + definition)
	return err
}

func initializeLearningStore(db *sql.DB, root string) error {
	for _, name := range []string{"snapshots", "runs", "registry", "exports", "backups"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			return err
		}
	}
	now := float64(time.Now().UnixNano()) / 1e9
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addSQLiteColumn(tx, "learning_case_requests", "exclude_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO versions(id,parent,members_json,created,reason,package_hash) VALUES('initial',NULL,'[]',?,'Arnés inicial','')`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO learning_registry_state(singleton,active_version,generation,updated) VALUES(1,'initial',0,?)`, now); err != nil {
		return err
	}
	return tx.Commit()
}
