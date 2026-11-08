package secrets

import (
	"bytes"
	"errors"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(key(1))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("JBSWY3DPEHPK3PXP"), "totp:user-1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("plaintext visible in sealed value")
	}
	got, err := box.Open(sealed, "totp:user-1")
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	box, _ := New(key(1))
	other, _ := New(key(2))
	sealed, _ := box.Seal([]byte("secret"), "totp:user-1")

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1

	for name, tc := range map[string]struct {
		box    *Box
		sealed []byte
		ad     string
	}{
		"other row":        {box, sealed, "totp:user-2"},
		"other master key": {other, sealed, "totp:user-1"},
		"tampered":         {box, tampered, "totp:user-1"},
		"truncated":        {box, sealed[:10], "totp:user-1"},
		"empty":            {box, nil, "totp:user-1"},
	} {
		if _, err := tc.box.Open(tc.sealed, tc.ad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: err = %v, want ErrDecrypt", name, err)
		}
	}
}

func TestSealIsRandomized(t *testing.T) {
	box, _ := New(key(1))
	a, _ := box.Seal([]byte("same"), "x")
	b, _ := box.Seal([]byte("same"), "x")
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same value are identical")
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected an error")
	}
}
