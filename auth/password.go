// Package auth holds the building blocks for sign-in: password hashing, random tokens,
// TOTP codes and rate limiting. The accounts module puts them together.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's recommended minimum profile (19 MiB, t=2, p=1). Every hash stores
// its parameters, so they can be raised later; VerifyPassword then reports needsRehash.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// Password length limits. The upper bound caps the work an attacker can force per request.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 256
)

// ErrMalformedHash is returned for a stored hash that is not an argon2id PHC string.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// CheckPassword returns a user-facing reason why password is not acceptable, or "".
// email may be empty.
func CheckPassword(password, email string) string {
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLength:
		return fmt.Sprintf("must be at least %d characters", MinPasswordLength)
	case n > MaxPasswordLength:
		return fmt.Sprintf("must be at most %d characters", MaxPasswordLength)
	case commonPasswords[strings.ToLower(password)]:
		return "is too common; choose something less predictable"
	}
	if local, _, ok := strings.Cut(strings.ToLower(email), "@"); ok && len(local) >= 4 &&
		strings.Contains(strings.ToLower(password), local) {
		return "must not contain your email address"
	}
	return ""
}

// HashPassword returns an argon2id hash in PHC string format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches hash, in constant time, and whether the hash
// uses older parameters and should be replaced with a fresh HashPassword result.
func VerifyPassword(hash, password string) (ok, needsRehash bool, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, ErrMalformedHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, false, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false, ErrMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, false, ErrMalformedHash
	}
	if utf8.RuneCountInString(password) > MaxPasswordLength {
		return false, false, nil
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = memory != argonMemory || time != argonTime || threads != argonThreads || len(want) != argonKeyLen
	return ok, needsRehash, nil
}

// dummyHash is verified when an account does not exist, so that sign-in takes the same time
// for unknown and known emails.
var dummyHash, _ = HashPassword("this password is never used for anything")

// SpendHashTime runs one password verification against a fixed hash and discards the result.
func SpendHashTime(password string) { _, _, _ = VerifyPassword(dummyHash, password) }

// A short list of the most common passwords of at least MinPasswordLength characters.
var commonPasswords = map[string]bool{
	"123456789012": true, "1234567890123": true, "12345678901234": true, "qwertyuiopas": true,
	"password1234": true, "password12345": true, "passwordpassword": true, "qwerty123456": true,
	"iloveyou1234": true, "111111111111": true, "000000000000": true, "abcdefghijkl": true,
	"administrator": true, "letmein12345": true, "welcome12345": true, "changeme1234": true,
	"qwertyuiop123": true, "1q2w3e4r5t6y": true, "aaaaaaaaaaaa": true, "trustno1trustno1": true,
}
