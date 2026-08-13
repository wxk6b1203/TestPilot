package model

import "testing"

func TestNextIDUnique(t *testing.T) {
	const n = 1000
	seen := make(map[int64]struct{}, n)
	for i := 0; i < n; i++ {
		id := NextID()
		if id <= 0 {
			t.Fatalf("NextID() = %d, want positive", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NextID() duplicated at iteration %d: %d", i, id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("unique ids = %d, want %d", len(seen), n)
	}
}
