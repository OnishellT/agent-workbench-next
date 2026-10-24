// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"workbench/internal/hosts"
)

type countingHostScope struct {
	historyHostScope
	gets map[string]int
}

func (s *countingHostScope) Get(ctx context.Context, id string) (hosts.Host, error) {
	s.gets[id]++
	return s.historyHostScope.Get(ctx, id)
}

// Listing tasks and conversations hides rows of hosts outside the current scope,
// pages over the visible rows only, and asks the host registry once per host.
func TestListsPageOverHostVisibleRowsAndResolveEachHostOnce(t *testing.T) {
	hostA, _ := historyTestHost("host-a")
	scope := &countingHostScope{historyHostScope: historyHostScope{rows: map[string]hosts.Host{hostA.ID: hostA}}, gets: map[string]int{}}
	module, server := newFoundryTestServer(t, filepath.Join(t.TempDir(), "state"), scope)
	client := server.Client()
	if _, err := module.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES('src','/x','canonical',1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	// order of insertion == order of "updated" (newest last); the API lists newest first.
	hostsOf := []string{"", "host-a", "host-gone", "host-a", "", "host-gone", "host-a"}
	var visibleNewestFirst []string
	for i, host := range hostsOf {
		id := fmt.Sprintf("s%d", i)
		updated := float64(100 + i)
		if _, err := module.db.Exec(`INSERT INTO sessions(id,source_id,path,title,revision,updated,captured_at,activity_at,completion,coverage_json,host_id) VALUES(?,'src',?,?, 'r1',?,?,?,'unknown','{}',?)`, id, "/x/"+id, id, updated, updated, updated, host); err != nil {
			t.Fatal(err)
		}
		if _, err := module.db.Exec(`INSERT INTO revisions(session_id,revision,raw_sha256,created,coverage_json) VALUES(?,'r1','h',1,'{}')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := module.db.Exec(`INSERT INTO tasks(id,session_id,revision,first_event,last_event,title,created,updated) VALUES(?,?,'r1',0,0,?,?,?)`, "t"+id, id, "task "+id, updated, updated); err != nil {
			t.Fatal(err)
		}
		if host != "host-gone" {
			visibleNewestFirst = append([]string{id}, visibleNewestFirst...)
		}
	}
	for _, c := range []struct{ path, key, idField string }{{"/api/tasks", "tasks", "title"}, {"/api/conversations", "sessions", "id"}} {
		scope.gets = map[string]int{}
		var got []string
		cursor := ""
		pages := 0
		for {
			_, body := callFoundryJSON(t, client, server.URL, http.MethodGet, c.path+"?limit=2&cursor="+cursor, nil)
			for _, item := range foundryList(t, body[c.key]) {
				value := foundryObject(t, item)[c.idField].(string)
				got = append(got, value)
			}
			if total := body["total_"+c.key]; total != float64(len(visibleNewestFirst)) {
				t.Fatalf("%s total=%v want %d", c.path, total, len(visibleNewestFirst))
			}
			pages++
			next, _ := body["next_cursor"].(string)
			if next == "" {
				break
			}
			cursor = next
		}
		want := visibleNewestFirst
		if c.key == "tasks" {
			want = nil
			for _, id := range visibleNewestFirst {
				want = append(want, "task "+id)
			}
		}
		if !reflect.DeepEqual(got, want) || pages != 3 {
			t.Fatalf("%s paged %v (%d pages) want %v (3 pages)", c.path, got, pages, want)
		}
		scope.gets = map[string]int{}
		callFoundryJSON(t, client, server.URL, http.MethodGet, c.path+"?limit=100", nil)
		if scope.gets["host-a"] != 1 || scope.gets["host-gone"] != 1 {
			t.Fatalf("%s resolved hosts per row instead of once per request: %v", c.path, scope.gets)
		}
	}
}
