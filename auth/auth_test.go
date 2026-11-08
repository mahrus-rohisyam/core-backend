package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mahrus-rohisyam/core-backend/db/dbtest"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	ok, rehash, err := VerifyPassword(h, "correct horse battery")
	if err != nil || !ok || rehash {
		t.Fatalf("Verify(correct) = %v, %v, %v", ok, rehash, err)
	}
	if ok, _, _ := VerifyPassword(h, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyFlagsOldParameters(t *testing.T) {
	// A hash made with weaker parameters (t=1) must still verify and ask for a rehash.
	const old = "$argon2id$v=19$m=19456,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$"
	if _, _, err := VerifyPassword(old+"AAAA", "x"); err != nil {
		t.Fatalf("old-parameter hash rejected as malformed: %v", err)
	}
	_, rehash, _ := VerifyPassword(old+"AAAA", "x")
	if !rehash {
		t.Fatal("old parameters not flagged for rehash")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$2a$10$bcrypt", "$argon2id$v=19$m=x$a$b", "$argon2i$v=19$m=1,t=1,p=1$YQ$YQ"} {
		if _, _, err := VerifyPassword(h, "x"); err == nil {
			t.Errorf("VerifyPassword(%q) accepted a malformed hash", h)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for _, tc := range []struct{ pw, email, want string }{
		{"short", "", "at least 12"},
		{strings.Repeat("a", 257), "", "at most 256"},
		{"password1234", "", "too common"},
		{"my-alice.smith-pass", "alice.smith@example.com", "email"},
		{"a long and unusual phrase", "bob@example.com", ""},
	} {
		got := CheckPassword(tc.pw, tc.email)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("CheckPassword(%q) = %q, want %q", tc.pw, got, tc.want)
		}
	}
}

func TestTokens(t *testing.T) {
	tok, hash := NewToken("cbt_")
	if !strings.HasPrefix(tok, "cbt_") || len(tok) < 40 {
		t.Fatalf("token %q too short or missing prefix", tok)
	}
	if string(HashToken(tok)) != string(hash) {
		t.Fatal("HashToken does not match NewToken's hash")
	}
	other, _ := NewToken("cbt_")
	if tok == other {
		t.Fatal("tokens repeat")
	}
}

func TestTOTPMatchesRFC6238(t *testing.T) {
	// RFC 6238 appendix B test secret "12345678901234567890" (SHA-1), 8 digits truncated to 6.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"},
	} {
		got, err := TOTPCode(secret, time.Unix(tc.unix, 0))
		if err != nil || got != tc.want {
			t.Errorf("TOTPCode(t=%d) = %s, %v; want %s", tc.unix, got, err, tc.want)
		}
	}
}

func TestVerifyTOTPRejectsReuseAndDrift(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := TOTPCode(secret, now)

	step, ok := VerifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("current code rejected")
	}
	if _, ok := VerifyTOTP(secret, code, now, step); ok {
		t.Fatal("code accepted twice")
	}
	prev, _ := TOTPCode(secret, now.Add(-30*time.Second))
	if _, ok := VerifyTOTP(secret, prev, now, 0); !ok {
		t.Fatal("code from the previous step rejected")
	}
	old, _ := TOTPCode(secret, now.Add(-2*time.Minute))
	if _, ok := VerifyTOTP(secret, old, now, 0); ok {
		t.Fatal("code from two minutes ago accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 14 || c[4] != '-' || c[9] != '-' || seen[c] {
			t.Fatalf("bad or repeated code %q", c)
		}
		seen[c] = true
		if NormalizeRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) != c {
			t.Fatalf("normalize does not restore %q", c)
		}
	}
}

func TestLimiter(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE rate_limits (key text, window_start timestamptz, count int NOT NULL,
		expires_at timestamptz NOT NULL, PRIMARY KEY (key, window_start))`); err != nil {
		t.Fatal(err)
	}
	l := NewLimiter(pool)
	fixed := time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC)
	l.now = func() time.Time { return fixed }

	for i := 1; i <= 3; i++ {
		if ok, err := l.Allow(ctx, "sign-in:a", 3, time.Minute); err != nil || !ok {
			t.Fatalf("attempt %d blocked (err %v)", i, err)
		}
	}
	if ok, _ := l.Allow(ctx, "sign-in:a", 3, time.Minute); ok {
		t.Fatal("fourth attempt allowed")
	}
	if ok, _ := l.Allow(ctx, "sign-in:b", 3, time.Minute); !ok {
		t.Fatal("other key affected")
	}
	l.now = func() time.Time { return fixed.Add(time.Minute) }
	if ok, _ := l.Allow(ctx, "sign-in:a", 3, time.Minute); !ok {
		t.Fatal("new window still blocked")
	}
	if err := l.Reset(ctx, "sign-in:a"); err != nil {
		t.Fatal(err)
	}
}
