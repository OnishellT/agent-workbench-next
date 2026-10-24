package foundry

import (
	"context"
	"testing"
)

func TestListLocalScanSourcesSkipsHostHistoryAndDisabled(t *testing.T) {
	m, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	for _, row := range []struct {
		id      string
		enabled int
	}{{"local-on", 1}, {"local-off", 0}, {"host-history-abc", 1}} {
		if _, err := m.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created,coverage_json) VALUES(?,?,'canonical',?,1,'{}')`, row.id, "/x/"+row.id, row.enabled); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := m.listLocalScanSources(t.Context())
	if err != nil || len(sources) != 1 || sources[0].ID != "local-on" {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}
