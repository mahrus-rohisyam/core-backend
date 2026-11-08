package accounts_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

func start(t *testing.T, env map[string]string) *apptest.Server {
	t.Helper()
	acc := accounts.New()
	return apptest.New(t, apptest.Options{Env: env}, acc, orgs.New(acc))
}

type session struct {
	User      accounts.User `json:"user"`
	CSRFToken string        `json:"csrf_token"`
}

func signIn(t *testing.T, srv *apptest.Server, email, password, code string) (*apptest.Client, *apptest.Response) {
	t.Helper()
	c := srv.Client()
	resp := c.Post(t, "/api/v1/auth/sign-in", map[string]string{"email": email, "password": password, "code": code})
	if resp.StatusCode == http.StatusOK {
		var s session
		resp.Decode(t, &s)
		c.CSRF = s.CSRFToken
	}
	return c, resp
}

func TestSignUpAndSession(t *testing.T) {
	srv := start(t, map[string]string{"APP_SIGNUP": "invite"})

	var cfg map[string]any
	srv.Client().Get(t, "/api/v1/auth/config").Expect(t, http.StatusOK).Decode(t, &cfg)
	if cfg["setup_required"] != true {
		t.Fatalf("setup_required = %v before the first account", cfg["setup_required"])
	}

	alice := srv.SignUp(t, "Alice@Example.com")
	var s session
	alice.Get(t, "/api/v1/session").Expect(t, http.StatusOK).Decode(t, &s)
	if s.User.Email != "alice@example.com" || !s.User.IsInstanceAdmin || s.User.EmailVerified {
		t.Fatalf("first user = %+v", s.User)
	}
	if s.CSRFToken == "" || s.CSRFToken != alice.CSRF {
		t.Fatal("session does not return the CSRF token")
	}

	// Sign-up is by invitation after the first account.
	resp := srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]string{"email": "bob@example.com", "name": "Bob", "password": apptest.Password})
	resp.Expect(t, http.StatusForbidden)

	srv.Client().Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)
}

func TestSignUpValidation(t *testing.T) {
	srv := start(t, nil)
	srv.SignUp(t, "alice@example.com")
	cases := []map[string]string{
		{"email": "not-an-email", "name": "A", "password": apptest.Password},
		{"email": "x@example.com", "name": "", "password": apptest.Password},
		{"email": "y@example.com", "name": "Y", "password": "short"},
		{"email": "z@example.com", "name": "Z", "password": "password1234"},
		{"email": "ALICE@example.com", "name": "Again", "password": apptest.Password},
	}
	for _, body := range cases {
		srv.Client().Post(t, "/api/v1/auth/sign-up", body).Expect(t, http.StatusUnprocessableEntity)
	}
	// Unknown fields are rejected.
	srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]any{"email": "q@example.com", "name": "Q", "password": apptest.Password, "is_instance_admin": true}).
		Expect(t, http.StatusBadRequest)
}

func TestSignInAndOut(t *testing.T) {
	srv := start(t, nil)
	srv.SignUp(t, "alice@example.com")

	_, resp := signIn(t, srv, "alice@example.com", "wrong password here", "")
	resp.Expect(t, http.StatusUnauthorized)
	_, resp = signIn(t, srv, "nobody@example.com", apptest.Password, "")
	resp.Expect(t, http.StatusUnauthorized)

	c, resp := signIn(t, srv, " ALICE@example.com ", apptest.Password, "")
	resp.Expect(t, http.StatusOK)
	c.Get(t, "/api/v1/session").Expect(t, http.StatusOK)

	c.Post(t, "/api/v1/auth/sign-out", nil).Expect(t, http.StatusNoContent)
	c.Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)
}

func TestCSRFAndOrigin(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")

	token := alice.CSRF
	alice.CSRF = ""
	alice.Patch(t, "/api/v1/account", map[string]string{"name": "Mallory"}).Expect(t, http.StatusForbidden)
	alice.CSRF = "wrong"
	alice.Patch(t, "/api/v1/account", map[string]string{"name": "Mallory"}).Expect(t, http.StatusForbidden)
	alice.CSRF = token

	alice.Do(t, http.MethodPatch, "/api/v1/account", map[string]string{"name": "Mallory"},
		http.Header{"Origin": {"https://evil.example"}}).Expect(t, http.StatusForbidden)
	alice.Do(t, http.MethodPatch, "/api/v1/account", map[string]string{"name": "Mallory"},
		http.Header{"Sec-Fetch-Site": {"cross-site"}}).Expect(t, http.StatusForbidden)

	var u accounts.User
	alice.Patch(t, "/api/v1/account", map[string]string{"name": " Alice A. "}).Expect(t, http.StatusOK).Decode(t, &u)
	if u.Name != "Alice A." {
		t.Fatalf("name = %q", u.Name)
	}
}

func TestVerifyEmail(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")
	token := srv.LinkToken(t, "alice@example.com")

	anon := srv.Client()
	anon.Post(t, "/api/v1/auth/verify-email", map[string]string{"token": "made-up"}).Expect(t, http.StatusBadRequest)
	anon.Post(t, "/api/v1/auth/verify-email", map[string]string{"token": token}).Expect(t, http.StatusNoContent)
	anon.Post(t, "/api/v1/auth/verify-email", map[string]string{"token": token}).Expect(t, http.StatusBadRequest)

	var s session
	alice.Get(t, "/api/v1/session").Expect(t, http.StatusOK).Decode(t, &s)
	if !s.User.EmailVerified {
		t.Fatal("email not verified")
	}
	alice.Post(t, "/api/v1/account/verify-email/resend", nil).Expect(t, http.StatusConflict)
}

func TestPasswordReset(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")

	// Unknown addresses get the same answer and no email.
	srv.Client().Post(t, "/api/v1/auth/password-reset", map[string]string{"email": "nobody@example.com"}).Expect(t, http.StatusAccepted)
	if _, ok := srv.Mail.Last("nobody@example.com"); ok {
		t.Fatal("email sent to an unknown address")
	}

	srv.Client().Post(t, "/api/v1/auth/password-reset", map[string]string{"email": "alice@example.com"}).Expect(t, http.StatusAccepted)
	token := srv.LinkToken(t, "alice@example.com")

	srv.Client().Post(t, "/api/v1/auth/password-reset/confirm", map[string]string{"token": token, "password": "short"}).
		Expect(t, http.StatusUnprocessableEntity)
	const newPassword = "a whole new passphrase"
	srv.Client().Post(t, "/api/v1/auth/password-reset/confirm", map[string]string{"token": token, "password": newPassword}).
		Expect(t, http.StatusNoContent)
	srv.Client().Post(t, "/api/v1/auth/password-reset/confirm", map[string]string{"token": token, "password": newPassword}).
		Expect(t, http.StatusBadRequest)

	// The reset signs out every session.
	alice.Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)
	_, resp := signIn(t, srv, "alice@example.com", apptest.Password, "")
	resp.Expect(t, http.StatusUnauthorized)
	_, resp = signIn(t, srv, "alice@example.com", newPassword, "")
	resp.Expect(t, http.StatusOK)
}

func TestChangePassword(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")
	other, _ := signIn(t, srv, "alice@example.com", apptest.Password, "")

	alice.Post(t, "/api/v1/account/password", map[string]string{"current_password": "wrong", "new_password": "another good passphrase"}).
		Expect(t, http.StatusUnprocessableEntity)
	alice.Post(t, "/api/v1/account/password", map[string]string{"current_password": apptest.Password, "new_password": "another good passphrase"}).
		Expect(t, http.StatusNoContent)

	alice.Get(t, "/api/v1/session").Expect(t, http.StatusOK)
	other.Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)
}

func TestTOTP(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")

	var setup struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	alice.Post(t, "/api/v1/account/totp/setup", map[string]string{"password": "wrong"}).Expect(t, http.StatusUnprocessableEntity)
	alice.Post(t, "/api/v1/account/totp/setup", map[string]string{"password": apptest.Password}).Expect(t, http.StatusOK).Decode(t, &setup)
	if !strings.HasPrefix(setup.OTPAuthURL, "otpauth://totp/") {
		t.Fatalf("otpauth_url = %q", setup.OTPAuthURL)
	}
	alice.Post(t, "/api/v1/account/totp/confirm", map[string]string{"code": "000000"}).Expect(t, http.StatusUnprocessableEntity)

	code, err := auth.TOTPCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var confirmed struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	alice.Post(t, "/api/v1/account/totp/confirm", map[string]string{"code": code}).Expect(t, http.StatusOK).Decode(t, &confirmed)
	if len(confirmed.RecoveryCodes) != 10 {
		t.Fatalf("got %d recovery codes", len(confirmed.RecoveryCodes))
	}

	_, resp := signIn(t, srv, "alice@example.com", apptest.Password, "")
	resp.Expect(t, http.StatusUnauthorized)
	if !strings.Contains(string(resp.Body), "mfa-required") {
		t.Fatalf("body = %s", resp.Body)
	}
	// The code that confirmed the setup cannot be used again.
	_, resp = signIn(t, srv, "alice@example.com", apptest.Password, code)
	resp.Expect(t, http.StatusUnauthorized)

	next, _ := auth.TOTPCode(setup.Secret, time.Now().Add(30*time.Second))
	_, resp = signIn(t, srv, "alice@example.com", apptest.Password, next)
	resp.Expect(t, http.StatusOK)

	rc := strings.ToUpper(strings.ReplaceAll(confirmed.RecoveryCodes[0], "-", ""))
	_, resp = signIn(t, srv, "alice@example.com", apptest.Password, rc)
	resp.Expect(t, http.StatusOK)
	_, resp = signIn(t, srv, "alice@example.com", apptest.Password, rc)
	resp.Expect(t, http.StatusUnauthorized)

	alice.Post(t, "/api/v1/account/totp/disable", map[string]string{"password": "wrong"}).Expect(t, http.StatusUnprocessableEntity)
	alice.Post(t, "/api/v1/account/totp/disable", map[string]string{"password": apptest.Password}).Expect(t, http.StatusNoContent)
	_, resp = signIn(t, srv, "alice@example.com", apptest.Password, "")
	resp.Expect(t, http.StatusOK)
}

func TestSessions(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")
	phone, _ := signIn(t, srv, "alice@example.com", apptest.Password, "")
	bob := srv.SignUp(t, "bob@example.com")

	var list struct {
		Items []accounts.SessionInfo `json:"items"`
	}
	alice.Get(t, "/api/v1/account/sessions").Expect(t, http.StatusOK).Decode(t, &list)
	if len(list.Items) != 2 {
		t.Fatalf("got %d sessions", len(list.Items))
	}
	var phoneID string
	for _, s := range list.Items {
		if !s.Current {
			phoneID = s.ID
		}
	}
	// Someone else's session is not found.
	bob.Delete(t, "/api/v1/account/sessions/"+phoneID).Expect(t, http.StatusNotFound)
	alice.Delete(t, "/api/v1/account/sessions/"+phoneID).Expect(t, http.StatusNoContent)
	phone.Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)

	phone, _ = signIn(t, srv, "alice@example.com", apptest.Password, "")
	alice.Post(t, "/api/v1/account/sessions/revoke-others", nil).Expect(t, http.StatusNoContent)
	phone.Get(t, "/api/v1/session").Expect(t, http.StatusUnauthorized)
	alice.Get(t, "/api/v1/session").Expect(t, http.StatusOK)
}

func TestSignInRateLimit(t *testing.T) {
	srv := start(t, map[string]string{"APP_TRUST_PROXY": "true"})
	srv.SignUp(t, "alice@example.com")
	for range 10 {
		_, resp := signIn(t, srv, "alice@example.com", "wrong password here", "")
		resp.Expect(t, http.StatusUnauthorized)
	}
	_, resp := signIn(t, srv, "alice@example.com", apptest.Password, "")
	resp.Expect(t, http.StatusTooManyRequests)

	// Guessing from one address does not lock the owner out on another.
	srv.Client().Do(t, http.MethodPost, "/api/v1/auth/sign-in", map[string]string{"email": "alice@example.com", "password": apptest.Password},
		http.Header{"X-Forwarded-For": {"203.0.113.7"}}).Expect(t, http.StatusOK)
}
