package hosts

import (
	"strings"
	"testing"
)

// Oracle: the two original, separately written allowlist switches.
func oldUploadTop(rel string) bool {
	top := rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		top = rel[:i]
	}
	switch top {
	case "export", "jobs", "sessions", "memory", "skill":
		return true
	}
	return false
}

func oldUploadRel(rel string) bool {
	if rel == "resources.md" {
		return true
	}
	top, _, ok := strings.Cut(rel, "/")
	if !ok {
		return false
	}
	switch top {
	case "export", "jobs", "sessions", "memory", "skill":
		return true
	}
	return false
}

func TestMemuUploadAllowlistMatchesOriginal(t *testing.T) {
	for _, rel := range []string{"export", "export/a.md", "jobs/x/y", "sessions", "memory/m.md", "skill/s/SKILL.md", "resources.md", "resources.md/x", "adoptions", "adoptions/a", "commits/c", "other.md", "", "exportx/a", "Export/a"} {
		top, _, _ := strings.Cut(rel, "/")
		if got, want := isMemuUploadTop(top), oldUploadTop(rel); got != want {
			t.Errorf("top(%q)=%v want %v", rel, got, want)
		}
		if got, want := isMemuUploadRel(rel), oldUploadRel(rel); got != want {
			t.Errorf("rel(%q)=%v want %v", rel, got, want)
		}
	}
}
