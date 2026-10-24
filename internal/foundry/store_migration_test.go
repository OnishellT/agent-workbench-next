// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreMigratesV1JobsWithoutLosingRows(t *testing.T) {
	stateDir := t.TempDir()
	foundryDir := filepath.Join(stateDir, "foundry")
	if err := os.Mkdir(foundryDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(foundryDir, "foundry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE schema_info(singleton INTEGER PRIMARY KEY CHECK(singleton=1),version INTEGER NOT NULL);
	INSERT INTO schema_info(singleton,version) VALUES(1,1);
	CREATE TABLE jobs (
	  id TEXT PRIMARY KEY,
	  kind TEXT NOT NULL CHECK(kind='scan'),
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
	  updated REAL NOT NULL
	);
	INSERT INTO jobs(id,kind,subject,status,files,changed,errors,max_files,current_file,limited,result_json,error,created,updated)
	VALUES('scan-old','scan','source-old','succeeded',3,2,0,500,'',0,'{"files":3}','',10,11);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openStore(stateDir)
	if err != nil {
		t.Fatalf("migrate v1 store: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow(`SELECT version FROM schema_info WHERE singleton=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version=%d, want %d", version, schemaVersion)
	}
	var kind, status, progress string
	var files, changed int
	if err := migrated.QueryRow(`SELECT kind,status,files,changed,progress_json FROM jobs WHERE id='scan-old'`).Scan(&kind, &status, &files, &changed, &progress); err != nil {
		t.Fatal(err)
	}
	if kind != "scan" || status != "succeeded" || files != 3 || changed != 2 || progress != "{}" {
		t.Fatalf("migrated job changed: kind=%s status=%s files=%d changed=%d progress=%s", kind, status, files, changed, progress)
	}
	if _, err := migrated.Exec(`INSERT INTO jobs(id,kind,subject,status,created,updated) VALUES('proposal-new','propose_tasks','session','queued',12,12)`); err != nil {
		t.Fatalf("schema v2 rejected proposal job kind: %v", err)
	}
	if _, err := migrated.Exec(`INSERT INTO task_proposal_decisions(job_id,suggestion_index,action,task_id,reason,created) VALUES('proposal-new',0,'skipped',NULL,'reviewed',13)`); err != nil {
		t.Fatal(err)
	}
	if _, err := migrated.Exec(`UPDATE task_proposal_decisions SET reason='changed' WHERE job_id='proposal-new' AND suggestion_index=0`); err == nil {
		t.Fatal("proposal decision update bypassed append-only trigger")
	}
	if _, err := migrated.Exec(`DELETE FROM task_proposal_decisions WHERE job_id='proposal-new' AND suggestion_index=0`); err == nil {
		t.Fatal("proposal decision delete bypassed append-only trigger")
	}
}

// A store written before the OMP engine existed keeps its provider rows and
// accepts the wider engine list.
func TestOpenStoreMigratesV3ProviderKindsWithoutLosingRows(t *testing.T) {
	stateDir := t.TempDir()
	foundryDir := filepath.Join(stateDir, "foundry")
	if err := os.Mkdir(foundryDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(foundryDir, "foundry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE schema_info(singleton INTEGER PRIMARY KEY CHECK(singleton=1),version INTEGER NOT NULL);
	INSERT INTO schema_info(singleton,version) VALUES(1,3);
	CREATE TABLE task_provider_configs (
	  role TEXT PRIMARY KEY CHECK(role IN ('provider','replay')),
	  kind TEXT NOT NULL CHECK(kind IN ('pi','codex','command')),
	  command_json TEXT NOT NULL,
	  model TEXT NOT NULL DEFAULT '',
	  timeout_seconds REAL NOT NULL DEFAULT 180 CHECK(timeout_seconds BETWEEN 5 AND 1800),
	  real_inference INTEGER NOT NULL DEFAULT 0 CHECK(real_inference IN (0,1)),
	  model_provider TEXT NOT NULL DEFAULT '',
	  reasoning TEXT NOT NULL DEFAULT 'medium',
	  updated REAL NOT NULL
	);
	INSERT INTO task_provider_configs(role,kind,command_json,model,timeout_seconds,real_inference,model_provider,reasoning,updated)
	VALUES('provider','pi','["pi"]','native-model',180,1,'','high',10);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openStore(stateDir)
	if err != nil {
		t.Fatalf("migrate v3 store: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.QueryRow(`SELECT version FROM schema_info WHERE singleton=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version=%d, want %d", version, schemaVersion)
	}
	var kind, model, reasoning string
	var real int
	if err := migrated.QueryRow(`SELECT kind,model,real_inference,reasoning FROM task_provider_configs WHERE role='provider'`).Scan(&kind, &model, &real, &reasoning); err != nil {
		t.Fatal(err)
	}
	if kind != "pi" || model != "native-model" || real != 1 || reasoning != "high" {
		t.Fatalf("migrated provider row changed: kind=%s model=%s real=%d reasoning=%s", kind, model, real, reasoning)
	}
	if _, err := migrated.Exec(`UPDATE task_provider_configs SET kind='omp',command_json='["omp"]' WHERE role='provider'`); err != nil {
		t.Fatalf("schema v4 rejected the OMP engine: %v", err)
	}
	if _, err := migrated.Exec(`UPDATE task_provider_configs SET kind='claude' WHERE role='provider'`); err == nil {
		t.Fatal("an unknown engine bypassed the kind constraint")
	}
}

// The pragmas must hold on every pooled connection, including ones opened after the
// first (the pool recycles connections), so they ride in the DSN.
func TestOpenStorePragmasApplyToEveryConnection(t *testing.T) {
	db, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(time.Nanosecond) // force a fresh connection per query
	for i := 0; i < 3; i++ {
		var fk, busy int
		var sync int
		if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if fk != 1 || busy != 5000 || sync != 1 {
			t.Fatalf("connection %d: foreign_keys=%d busy_timeout=%d synchronous=%d", i, fk, busy, sync)
		}
	}
}
