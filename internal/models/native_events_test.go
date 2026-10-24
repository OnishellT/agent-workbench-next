package models

import (
	"strconv"
	"testing"
)

// The event ring keeps the newest nativeLogLimit lines in order, and a snapshot
// taken earlier is never rewritten by later events (the in-place shift must not
// alias what diagnostics already copied).
func TestNativeEventRingKeepsNewestInOrder(t *testing.T) {
	p := &nativeProcess{}
	for i := range nativeLogLimit + 10 {
		p.record("e", strconv.Itoa(i))
	}
	snapshot := append([]nativeEvent{}, p.events...)
	for i := nativeLogLimit + 10; i < nativeLogLimit*3; i++ {
		p.record("e", strconv.Itoa(i))
	}
	if len(p.events) != nativeLogLimit {
		t.Fatalf("ring holds %d, want %d", len(p.events), nativeLogLimit)
	}
	for i, ev := range p.events {
		if want := strconv.Itoa(nativeLogLimit*3 - nativeLogLimit + i); ev.Detail != want {
			t.Fatalf("event %d = %q, want %q", i, ev.Detail, want)
		}
	}
	if snapshot[0].Detail != "10" || snapshot[len(snapshot)-1].Detail != strconv.Itoa(nativeLogLimit+9) {
		t.Fatalf("earlier snapshot mutated: %q..%q", snapshot[0].Detail, snapshot[len(snapshot)-1].Detail)
	}
}
