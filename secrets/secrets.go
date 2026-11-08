// Package secrets encrypts values at rest (TOTP seeds, integration tokens) with envelope
// encryption: each value gets its own random data key, and the data key is encrypted with the
// app's master key (APP_MASTER_KEY). Both use AES-256-GCM.
//
// The associated data binds a sealed value to where it is stored (for example "totp:<user id>"),
// so a value copied to another row does not decrypt.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const version byte = 1

// ErrDecrypt means the value was tampered with, stored for other associated data, or sealed
// with another master key.
var ErrDecrypt = errors.New("secrets: cannot decrypt")

// Box seals and opens values under one master key.
type Box struct{ master cipher.AEAD }

// New returns a Box for a 32-byte master key.
func New(masterKey []byte) (*Box, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("secrets: master key must be 32 bytes, got %d", len(masterKey))
	}
	a, err := newGCM(masterKey)
	if err != nil {
		return nil, err
	}
	return &Box{master: a}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext. The result is self-contained: version, wrapped data key and ciphertext.
func (b *Box) Seal(plaintext []byte, associatedData string) ([]byte, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	wrapped, err := seal(b.master, dek, []byte(associatedData))
	if err != nil {
		return nil, err
	}
	data, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	body, err := seal(data, plaintext, []byte(associatedData))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 2+len(wrapped)+len(body))
	out = append(out, version, byte(len(wrapped)))
	out = append(out, wrapped...)
	return append(out, body...), nil
}

// Open decrypts a value from Seal with the same associated data.
func (b *Box) Open(sealed []byte, associatedData string) ([]byte, error) {
	if len(sealed) < 2 || sealed[0] != version {
		return nil, ErrDecrypt
	}
	n := int(sealed[1])
	if len(sealed) < 2+n {
		return nil, ErrDecrypt
	}
	dek, err := open(b.master, sealed[2:2+n], []byte(associatedData))
	if err != nil {
		return nil, ErrDecrypt
	}
	data, err := newGCM(dek)
	if err != nil {
		return nil, ErrDecrypt
	}
	plain, err := open(data, sealed[2+n:], []byte(associatedData))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

func seal(a cipher.AEAD, plaintext, ad []byte) ([]byte, error) {
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plaintext, ad), nil
}

func open(a cipher.AEAD, data, ad []byte) ([]byte, error) {
	ns := a.NonceSize()
	if len(data) < ns {
		return nil, ErrDecrypt
	}
	return a.Open(nil, data[:ns], data[ns:], ad)
}
