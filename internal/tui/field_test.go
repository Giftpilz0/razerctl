package tui

import "testing"

func TestSyncKeepsUserEdit(t *testing.T) {
	f := intField("x", 0, 100, 1, "")
	f.sync(10, false)
	if f.val != 10 {
		t.Fatalf("first sync: %d", f.val)
	}
	f.val = 20 // user edit, not applied yet
	f.sync(10, false)
	if f.val != 20 {
		t.Fatalf("periodic refresh overwrote edit: %d", f.val)
	}
	f.sync(30, false) // device changed elsewhere; edit still wins
	if f.val != 20 {
		t.Fatalf("edit lost: %d", f.val)
	}
	f.sync(30, true) // after apply the device is the truth
	if f.val != 30 {
		t.Fatalf("forced sync: %d", f.val)
	}
	f.sync(40, false) // untouched field follows the device
	if f.val != 40 {
		t.Fatalf("untouched field: %d", f.val)
	}
}
