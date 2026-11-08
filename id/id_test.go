package id

import (
	"testing"
	"time"
)

func TestNewIsValidVersion7(t *testing.T) {
	s := New()
	if !Valid(s) {
		t.Fatalf("New() = %q, not a valid UUID", s)
	}
	if s[14] != '7' {
		t.Fatalf("version nibble = %c, want 7", s[14])
	}
	if v := s[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("variant nibble = %c, want 8, 9, a or b", v)
	}
}

func TestNewSortsByTime(t *testing.T) {
	a := New()
	time.Sleep(2 * time.Millisecond)
	b := New()
	if !(a < b) {
		t.Fatalf("IDs not time-ordered: %s >= %s", a, b)
	}
}

func TestNewIsUnique(t *testing.T) {
	seen := make(map[string]bool, 10000)
	for range 10000 {
		s := New()
		if seen[s] {
			t.Fatalf("duplicate ID %s", s)
		}
		seen[s] = true
	}
}

func TestValid(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"0190f6a2-7b3c-7d4e-8f00-112233445566", true},
		{"0190F6A2-7B3C-7D4E-8F00-112233445566", true},
		{"0190f6a2-7b3c-7d4e-8f00-11223344556", false},
		{"0190f6a2x7b3c-7d4e-8f00-112233445566", false},
		{"0190f6a2-7b3c-7d4e-8f00-11223344556g", false},
		{"", false},
		{"../../etc/passwd", false},
	} {
		if got := Valid(tc.in); got != tc.want {
			t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
