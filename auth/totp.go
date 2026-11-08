package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 TOTP uses HMAC-SHA1, which authenticator apps expect
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP settings compatible with common authenticator apps.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step before and after, for clock drift
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret in base32, as shown to the user.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return b32.EncodeToString(b)
}

// TOTPURL returns the otpauth:// URL that authenticator apps read from a QR code.
func TOTPURL(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPCode returns the code for the time step containing t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("auth: bad TOTP secret: %w", err)
	}
	return hotp(key, uint64(t.Unix()/totpPeriod)), nil
}

// VerifyTOTP checks code against the steps around now. A step at or before lastStep is rejected,
// so each code works only once. It returns the matched step, which the caller stores as the new
// lastStep.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if err != nil || len(code) != totpDigits {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for s := current - totpSkew; s <= current+totpSkew; s++ {
		if s <= lastStep {
			continue
		}
		if Equal(hotp(key, uint64(s)), code) {
			return s, true
		}
	}
	return 0, false
}

func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// NewRecoveryCodes returns n single-use recovery codes like "k4m2-9xq7-pd3a".
func NewRecoveryCodes(n int) []string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			panic("auth: crypto/rand failed: " + err.Error())
		}
		var sb strings.Builder
		for j, c := range b {
			if j > 0 && j%4 == 0 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(c)%len(alphabet)])
		}
		codes[i] = sb.String()
	}
	return codes
}

// NormalizeRecoveryCode lowercases a typed recovery code and restores its dashes.
func NormalizeRecoveryCode(s string) string {
	s = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(s))
	if len(s) != 12 {
		return s
	}
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}
