// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// The job-registry helpers replaced eight hand-copied "query ids, then cancel
// the registered workers" blocks; this pins the behaviour they must keep.
func TestJobRegistryHelpers(t *testing.T) {
	module, _ := newFoundryTestServer(t, t.TempDir(), nil)
	for _, row := range [][3]string{{"j1", "evaluate", "cand-1"}, {"j2", "evaluate", "cand-1"}, {"j3", "evaluate", "cand-2"}} {
		if _, err := module.db.Exec(`INSERT INTO jobs(id,kind,subject,status,created,updated) VALUES(?,?,?,'cancelled',1,1)`, row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	got := jobIDs(context.Background(), module.db, `SELECT id FROM jobs WHERE subject=? ORDER BY id`, "cand-1")
	if want := []string{"j1", "j2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("jobIDs=%v want %v", got, want)
	}
	if got := jobIDs(context.Background(), module.db, `SELECT nope FROM nowhere`); len(got) != 0 {
		t.Fatalf("a failing query must yield no ids, got %v", got)
	}

	cancelled := map[string]bool{}
	module.mu.Lock()
	for _, id := range []string{"j1", "j2", "j3"} {
		id := id
		module.jobs[id] = func() { cancelled[id] = true }
	}
	module.mu.Unlock()

	module.cancelLearningWorkersForCandidate("cand-1")
	var names []string
	for id := range cancelled {
		names = append(names, id)
	}
	sort.Strings(names)
	if want := []string{"j1", "j2"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("cancelled %v want %v (j3 belongs to another candidate)", names, want)
	}

	module.cancelJobs("unknown-id") // unregistered ids are ignored
	module.forgetJob("j1")
	module.mu.Lock()
	_, stillThere := module.jobs["j1"]
	_, other := module.jobs["j3"]
	module.mu.Unlock()
	if stillThere || !other {
		t.Fatalf("forgetJob must drop only its id: j1 present=%v j3 present=%v", stillThere, other)
	}
}
