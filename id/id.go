// Package id generates UUIDv7 identifiers (RFC 9562): time-ordered, random in the low bits,
// and stored in Postgres as uuid.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// New returns a new UUIDv7 in canonical text form.
func New() string {
	var u [16]byte
	binary.BigEndian.PutUint64(u[0:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(u[6:]); err != nil {
		panic("id: crypto/rand failed: " + err.Error()) // the OS random source is broken; nothing is safe
	}
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant

	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:], u[10:])
	return string(b[:])
}

// Valid reports whether s is a UUID in canonical text form (any version).
func Valid(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
