package rbac

import (
	"slices"
	"testing"
)

func TestPermissionValid(t *testing.T) {
	for key, want := range map[string]bool{
		"notes.read": true, "notes.write_all": true,
		"notes": false, ".read": false, "notes.": false, "Notes.read": false, "notes.*": false, "notes .read": false,
	} {
		if got := (Permission{Key: key}).Valid(); got != want {
			t.Errorf("%q: %v", key, got)
		}
	}
}

func TestIntersect(t *testing.T) {
	got := Intersect([]string{"a.r", "b.r", "c.r"}, []string{"c.r", "a.r"})
	if !slices.Equal(got, []string{"a.r", "c.r"}) {
		t.Fatal(got)
	}
	if Intersect([]string{"a.r"}, nil) != nil {
		t.Fatal("expected nil")
	}
}
