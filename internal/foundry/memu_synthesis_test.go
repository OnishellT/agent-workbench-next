package foundry

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMemuSynthesisRequiresIndependentEvidenceAndIsAddedOnce(t *testing.T) {
	user := []memuExportCitation{{Role: "user", Exported: true}}
	for _, test := range []struct {
		name     string
		sessions []memuExportSession
		want     int
	}{
		{"same project", []memuExportSession{{Project: "/a", Citations: user}, {Project: "/a", Citations: user}}, 1},
		{"metadata alone", []memuExportSession{{Project: "/a", Citations: user}, {Project: "/b"}}, 1},
		{"two evidenced projects", []memuExportSession{{Project: "/a", Citations: user}, {Project: "/b", Citations: user}}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			data, _ := json.Marshal(memuExportManifest{Sessions: test.sessions})
			if err := writePrivateFile(filepath.Join(base, "export", "manifest.json"), data); err != nil {
				t.Fatal(err)
			}
			jobs, err := appendMemuSynthesisJob(base, []memuJobDTO{{Kind: "skill", Status: "succeeded"}})
			if err != nil || len(jobs) != test.want || jobs[0].Status != "succeeded" {
				t.Fatalf("jobs = %+v, %v", jobs, err)
			}
			if len(jobs) == 2 {
				if jobs[1].Kind != "synthesis" || jobs[1].Status != "queued" {
					t.Fatalf("synthesis = %+v", jobs[1])
				}
				if _, err := safeMemuJobPath(base, jobs[1].Path); err != nil {
					t.Fatal(err)
				}
				again, err := appendMemuSynthesisJob(base, jobs)
				if err != nil || len(again) != 2 {
					t.Fatalf("duplicated synthesis: %+v, %v", again, err)
				}
				again[1].Content = "previous synthesis policy"
				again[1].Status = "succeeded"
				updated, err := appendMemuSynthesisJob(base, again)
				if err != nil || len(updated) != 2 || updated[1].Status != "queued" || updated[0].Status != "succeeded" {
					t.Fatalf("upgrade must refresh only synthesis: %+v, %v", updated, err)
				}
			}
		})
	}
}
