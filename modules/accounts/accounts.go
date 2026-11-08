// Package accounts is the core module for user accounts: sign-up, sign-in with optional TOTP,
// sessions, email verification, password reset and account settings.
package accounts

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/audit"
	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/mail"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

//go:embed migrations/*.sql
var migrations embed.FS

// SignUp describes a new account, passed to OnSignUp hooks inside the sign-up transaction.
type SignUp struct {
	UserID     string
	Email      string
	Name       string
	Invitation string // invitation token from the sign-up form, if any
	FirstUser  bool   // the first account of the instance
}

// Module is the accounts module. Create it with New.
type Module struct {
	reg      *app.Registry
	onSignUp []func(ctx context.Context, tx pgx.Tx, s SignUp) error
	onReset  []func(ctx context.Context, tx pgx.Tx, userID string) error
}

// New returns the accounts module.
func New() *Module { return &Module{} }

// OnSignUp registers fn to run inside the sign-up transaction. An error aborts the sign-up;
// return an *httpx.Error to show the user why. The orgs module uses it to place new users in an
// organization.
func (m *Module) OnSignUp(fn func(ctx context.Context, tx pgx.Tx, s SignUp) error) {
	m.onSignUp = append(m.onSignUp, fn)
}

// OnPasswordReset registers fn to run inside the password reset transaction, after the user's
// sessions are deleted. Modules that issue other credentials revoke them here: whoever reset the
// password may be locking out someone who had taken over the account.
func (m *Module) OnPasswordReset(fn func(ctx context.Context, tx pgx.Tx, userID string) error) {
	m.onReset = append(m.onReset, fn)
}

// Name implements app.Module.
func (m *Module) Name() string { return "accounts" }

// Migrations implements app.Module.
func (m *Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

// Permissions implements app.Module. Account actions concern the caller only.
func (m *Module) Permissions() []rbac.Permission { return nil }

// Register implements app.Module.
func (m *Module) Register(r *app.Registry) error {
	m.reg = r
	r.AddAuthenticator(sessionAuth{m})
	r.Every("accounts-purge", time.Hour, m.purge)

	r.Handle("GET /api/v1/auth/config", m.authConfig)
	r.Handle("POST /api/v1/auth/sign-up", m.signUp)
	r.Handle("POST /api/v1/auth/sign-in", m.signIn)
	r.Handle("POST /api/v1/auth/sign-out", m.signOut, r.RequireSession())
	r.Handle("POST /api/v1/auth/verify-email", m.verifyEmail)
	r.Handle("POST /api/v1/auth/password-reset", m.requestReset)
	r.Handle("POST /api/v1/auth/password-reset/confirm", m.confirmReset)

	r.Handle("GET /api/v1/session", m.session, r.RequireUser())
	r.Handle("PATCH /api/v1/account", m.updateAccount, r.RequireSession())
	r.Handle("POST /api/v1/account/password", m.changePassword, r.RequireSession())
	r.Handle("POST /api/v1/account/verify-email/resend", m.resendVerification, r.RequireSession())
	r.Handle("POST /api/v1/account/totp/setup", m.totpSetup, r.RequireSession())
	r.Handle("POST /api/v1/account/totp/confirm", m.totpConfirm, r.RequireSession())
	r.Handle("POST /api/v1/account/totp/disable", m.totpDisable, r.RequireSession())
	r.Handle("GET /api/v1/account/sessions", m.sessions, r.RequireSession())
	r.Handle("DELETE /api/v1/account/sessions/{id}", m.revokeSession, r.RequireSession())
	r.Handle("POST /api/v1/account/sessions/revoke-others", m.revokeOtherSessions, r.RequireSession())
	return nil
}

var (
	errInvalidCredentials = httpx.NewError(http.StatusUnauthorized, "invalid-credentials", "Email or password is incorrect")
	errMFARequired        = httpx.NewError(http.StatusUnauthorized, "mfa-required", "Enter the code from your authenticator app")
	errInvalidCode        = httpx.NewError(http.StatusUnauthorized, "invalid-code", "That code is not valid")
	errSignupClosed       = httpx.NewError(http.StatusForbidden, "signup-closed", "Sign-up is by invitation only")
	errInvalidToken       = httpx.NewError(http.StatusBadRequest, "invalid-token", "This link is invalid or has expired")
)

func (m *Module) limit(ctx context.Context, key string, n int, window time.Duration) error {
	ok, err := m.reg.Limiter.Allow(ctx, key, n, window)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.ErrTooMany
	}
	return nil
}

type sessionResponse struct {
	User      User   `json:"user"`
	CSRFToken string `json:"csrf_token,omitempty"`
}

// GET /api/v1/auth/config: public settings the sign-in screens need.
func (m *Module) authConfig(w http.ResponseWriter, r *http.Request) error {
	var hasUsers bool
	if err := m.reg.DB.QueryRow(r.Context(), "SELECT EXISTS (SELECT 1 FROM users)").Scan(&hasUsers); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"app_name":       m.reg.AppName,
		"tenancy":        m.reg.Config.Tenancy,
		"signup":         m.reg.Config.Signup,
		"setup_required": !hasUsers,
	})
	return nil
}

func (m *Module) signUp(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email      string `json:"email"`
		Name       string `json:"name"`
		Password   string `json:"password"`
		Invitation string `json:"invitation"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := m.limit(ctx, "sign-up:ip:"+m.reg.ClientIP(r), 10, time.Hour); err != nil {
		return err
	}
	var bad []httpx.FieldError
	email, ok := NormalizeEmail(in.Email)
	if !ok {
		bad = append(bad, httpx.FieldError{Field: "email", Message: "must be a valid email address"})
	}
	name, fe := checkName(in.Name)
	if fe != nil {
		bad = append(bad, *fe)
	}
	if msg := auth.CheckPassword(in.Password, email); msg != "" {
		bad = append(bad, httpx.FieldError{Field: "password", Message: msg})
	}
	if len(bad) > 0 {
		return httpx.Invalid(bad...)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}

	userID := id.New()
	var sess newSession
	var verifyToken string
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		// Serialize sign-ups so that exactly one account becomes the first (instance admin).
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7406211002)"); err != nil {
			return err
		}
		var first bool
		if err := tx.QueryRow(ctx, "SELECT NOT EXISTS (SELECT 1 FROM users)").Scan(&first); err != nil {
			return err
		}
		if !first && in.Invitation == "" && m.reg.Config.Signup == config.SignupInvite {
			return errSignupClosed
		}
		_, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, is_instance_admin) VALUES ($1, $2, $3, $4, $5)`,
			userID, email, name, hash, first)
		if db.IsUniqueViolation(err, "users_email") {
			return httpx.Invalid(httpx.FieldError{Field: "email", Message: "an account with this email already exists; sign in instead"})
		}
		if err != nil {
			return err
		}
		s := SignUp{UserID: userID, Email: email, Name: name, Invitation: in.Invitation, FirstUser: first}
		for _, fn := range m.onSignUp {
			if err := fn(ctx, tx, s); err != nil {
				return err
			}
		}
		if verifyToken, err = m.newOneTimeToken(ctx, tx, userID, "verify_email", 48*time.Hour); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorUserID: userID, Action: "user.signed_up", TargetType: "user", TargetID: userID, IP: m.reg.ClientIP(r)}); err != nil {
			return err
		}
		sess, err = m.startSession(ctx, tx, w, r, userID, false)
		return err
	})
	if err != nil {
		return err
	}
	m.sendVerification(ctx, email, verifyToken)
	u, err := GetUser(ctx, m.reg.DB, userID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, sessionResponse{User: u, CSRFToken: sess.csrf})
	return nil
}

func (m *Module) signIn(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	email, _ := NormalizeEmail(in.Email)
	ip := m.reg.ClientIP(r)
	if err := m.limit(ctx, "sign-in:ip:"+ip, 30, 15*time.Minute); err != nil {
		return err
	}
	// Per address and IP, so a stranger cannot lock someone out; the looser per-address limit
	// slows down guessing from many IPs.
	if err := m.limit(ctx, "sign-in:email-ip:"+email+"|"+ip, 10, 15*time.Minute); err != nil {
		return err
	}
	if err := m.limit(ctx, "sign-in:email:"+email, 100, time.Hour); err != nil {
		return err
	}

	var userID, status string
	var hash *string
	err := m.reg.DB.QueryRow(ctx, "SELECT id::text, password_hash, status FROM users WHERE lower(email) = $1", email).Scan(&userID, &hash, &status)
	if db.IsNotFound(err) || (err == nil && hash == nil) {
		auth.SpendHashTime(in.Password)
		return errInvalidCredentials
	}
	if err != nil {
		return err
	}
	ok, rehash, err := auth.VerifyPassword(*hash, in.Password)
	if err != nil {
		return fmt.Errorf("accounts: verify password for %s: %w", userID, err)
	}
	if !ok || status != "active" {
		return errInvalidCredentials
	}

	var sess newSession
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		mfa, err := m.checkSecondFactor(ctx, tx, userID, in.Code)
		if err != nil {
			return err
		}
		if rehash {
			if h, err := auth.HashPassword(in.Password); err == nil {
				if _, err := tx.Exec(ctx, "UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1", userID, h); err != nil {
					return err
				}
			}
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorUserID: userID, Action: "user.signed_in", TargetType: "user", TargetID: userID, IP: ip}); err != nil {
			return err
		}
		sess, err = m.startSession(ctx, tx, w, r, userID, mfa)
		return err
	})
	if err != nil {
		return err
	}
	_ = m.reg.Limiter.Reset(ctx, "sign-in:email-ip:"+email+"|"+ip)
	u, err := GetUser(ctx, m.reg.DB, userID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, sessionResponse{User: u, CSRFToken: sess.csrf})
	return nil
}

// checkSecondFactor verifies a TOTP or recovery code when the user has TOTP enabled. It reports
// whether a second factor was used.
func (m *Module) checkSecondFactor(ctx context.Context, tx pgx.Tx, userID, code string) (bool, error) {
	var sealed []byte
	var lastStep int64
	err := tx.QueryRow(ctx, `SELECT secret_sealed, last_used_step FROM totp_factors
		WHERE user_id = $1 AND confirmed_at IS NOT NULL FOR UPDATE`, userID).Scan(&sealed, &lastStep)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if code == "" {
		return false, errMFARequired
	}
	secret, err := m.reg.Secrets.Open(sealed, "totp:"+userID)
	if err != nil {
		return false, fmt.Errorf("accounts: open TOTP secret: %w", err)
	}
	if step, ok := auth.VerifyTOTP(string(secret), code, time.Now(), lastStep); ok {
		_, err := tx.Exec(ctx, "UPDATE totp_factors SET last_used_step = $2 WHERE user_id = $1", userID, step)
		return true, err
	}
	tag, err := tx.Exec(ctx, `UPDATE recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, auth.HashToken(auth.NormalizeRecoveryCode(code)))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	return false, errInvalidCode
}

func (m *Module) signOut(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	if _, err := m.reg.DB.Exec(r.Context(), "DELETE FROM sessions WHERE id = $1", p.SessionID); err != nil {
		return err
	}
	m.clearCookie(w)
	httpx.NoContent(w)
	return nil
}

func (m *Module) session(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	u, err := GetUser(r.Context(), m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, sessionResponse{User: u, CSRFToken: p.CSRFToken})
	return nil
}

func (m *Module) updateAccount(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, fe := checkName(in.Name)
	if fe != nil {
		return httpx.Invalid(*fe)
	}
	p := rbac.PrincipalFrom(r.Context())
	if _, err := m.reg.DB.Exec(r.Context(), "UPDATE users SET name = $2, updated_at = now() WHERE id = $1", p.UserID, name); err != nil {
		return err
	}
	u, err := GetUser(r.Context(), m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, u)
	return nil
}

func (m *Module) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if err := m.limit(ctx, "password-change:"+p.UserID, 10, time.Hour); err != nil {
		return err
	}
	if err := m.checkPassword(ctx, p.UserID, in.CurrentPassword, "current_password"); err != nil {
		return err
	}
	u, err := GetUser(ctx, m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	if msg := auth.CheckPassword(in.NewPassword, u.Email); msg != "" {
		return httpx.Invalid(httpx.FieldError{Field: "new_password", Message: msg})
	}
	if in.NewPassword == in.CurrentPassword {
		return httpx.Invalid(httpx.FieldError{Field: "new_password", Message: "must differ from the current password"})
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1", p.UserID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1 AND id <> $2", p.UserID, p.SessionID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorUserID: p.UserID, Action: "user.password_changed", TargetType: "user", TargetID: p.UserID, IP: m.reg.ClientIP(r)})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// checkPassword verifies the signed-in user's password, for actions that need re-authentication.
func (m *Module) checkPassword(ctx context.Context, userID, password, field string) error {
	var hash *string
	if err := m.reg.DB.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", userID).Scan(&hash); err != nil {
		return err
	}
	if hash == nil {
		return httpx.Invalid(httpx.FieldError{Field: field, Message: "this account has no password"})
	}
	ok, _, err := auth.VerifyPassword(*hash, password)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Invalid(httpx.FieldError{Field: field, Message: "is incorrect"})
	}
	return nil
}

func (m *Module) newOneTimeToken(ctx context.Context, q db.DBTX, userID, purpose string, ttl time.Duration) (string, error) {
	token, hash := auth.NewToken("")
	_, err := q.Exec(ctx, `INSERT INTO one_time_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, userID, purpose, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("accounts: create %s token: %w", purpose, err)
	}
	return token, nil
}

// useOneTimeToken marks a token used and returns its user. Each token works once.
func useOneTimeToken(ctx context.Context, q db.DBTX, token, purpose string) (string, error) {
	var userID string
	err := q.QueryRow(ctx, `UPDATE one_time_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id::text`, auth.HashToken(token), purpose).Scan(&userID)
	if db.IsNotFound(err) {
		return "", errInvalidToken
	}
	return userID, err
}

func (m *Module) send(ctx context.Context, msg mail.Message) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := m.reg.Mail.Send(ctx, msg); err != nil {
		httpx.Logger(ctx).Error("email not sent", slog.String("subject", msg.Subject), slog.String("error", err.Error()))
	}
}

func (m *Module) sendVerification(ctx context.Context, email, token string) {
	link := m.reg.URL("/app/verify-email", url.Values{"token": {token}})
	m.send(ctx, mail.Message{
		To:      email,
		Subject: "Confirm your email for " + m.reg.AppName,
		Body:    "Confirm your email address by opening this link:\n\n" + link + "\n\nThe link works once and expires in 48 hours. If you did not sign up, ignore this email.",
	})
}

func (m *Module) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := m.limit(ctx, "verify-email:ip:"+m.reg.ClientIP(r), 30, time.Hour); err != nil {
		return err
	}
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		userID, err := useOneTimeToken(ctx, tx, in.Token, "verify_email")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now() WHERE id = $1", userID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (m *Module) resendVerification(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if err := m.limit(ctx, "verify-resend:"+p.UserID, 5, time.Hour); err != nil {
		return err
	}
	u, err := GetUser(ctx, m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	if u.EmailVerified {
		return httpx.Conflict("your email is already confirmed")
	}
	token, err := m.newOneTimeToken(ctx, m.reg.DB, u.ID, "verify_email", 48*time.Hour)
	if err != nil {
		return err
	}
	m.sendVerification(ctx, u.Email, token)
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// POST /api/v1/auth/password-reset always answers 202, so it does not reveal which emails have accounts.
func (m *Module) requestReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	email, _ := NormalizeEmail(in.Email)
	if err := m.limit(ctx, "reset:ip:"+m.reg.ClientIP(r), 20, time.Hour); err != nil {
		return err
	}
	ok, err := m.reg.Limiter.Allow(ctx, "reset:email:"+email, 3, time.Hour)
	if err != nil {
		return err
	}
	if !ok {
		w.WriteHeader(http.StatusAccepted) // silently stop mail bombing one address
		return nil
	}
	var userID string
	err = m.reg.DB.QueryRow(ctx, "SELECT id::text FROM users WHERE lower(email) = $1 AND status = 'active'", email).Scan(&userID)
	if err == nil {
		token, err := m.newOneTimeToken(ctx, m.reg.DB, userID, "reset_password", time.Hour)
		if err != nil {
			return err
		}
		link := m.reg.URL("/app/reset-password", url.Values{"token": {token}})
		// Sent in the background: waiting for the mail server would make known addresses answer
		// slower than unknown ones.
		go m.send(ctx, mail.Message{
			To:      email,
			Subject: "Reset your " + m.reg.AppName + " password",
			Body:    "Choose a new password by opening this link:\n\n" + link + "\n\nThe link works once and expires in 1 hour. If you did not ask for this, ignore this email; your password stays the same.",
		})
	} else if !db.IsNotFound(err) {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (m *Module) confirmReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := m.limit(ctx, "reset-confirm:ip:"+m.reg.ClientIP(r), 30, time.Hour); err != nil {
		return err
	}
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		userID, err := useOneTimeToken(ctx, tx, in.Token, "reset_password")
		if err != nil {
			return err
		}
		u, err := GetUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if msg := auth.CheckPassword(in.Password, u.Email); msg != "" {
			return httpx.Invalid(httpx.FieldError{Field: "password", Message: msg})
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return err
		}
		// Proving control of the inbox also confirms the address.
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, email_verified_at = coalesce(email_verified_at, now()),
			updated_at = now() WHERE id = $1`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE one_time_tokens SET used_at = now() WHERE user_id = $1 AND purpose = 'reset_password' AND used_at IS NULL", userID); err != nil {
			return err
		}
		for _, fn := range m.onReset {
			if err := fn(ctx, tx, userID); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, tx, audit.Event{ActorUserID: userID, Action: "user.password_reset", TargetType: "user", TargetID: userID, IP: m.reg.ClientIP(r)}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}

// POST /api/v1/account/totp/setup needs the password, so a stolen session cannot lock the owner
// out by enrolling its own authenticator.
func (m *Module) totpSetup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if err := m.limit(ctx, "totp-setup:"+p.UserID, 10, time.Hour); err != nil {
		return err
	}
	if err := m.checkPassword(ctx, p.UserID, in.Password, "password"); err != nil {
		return err
	}
	u, err := GetUser(ctx, m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	if u.TOTPEnabled {
		return httpx.Conflict("two-factor authentication is already on; turn it off first to set it up again")
	}
	secret := auth.NewTOTPSecret()
	sealed, err := m.reg.Secrets.Seal([]byte(secret), "totp:"+p.UserID)
	if err != nil {
		return err
	}
	_, err = m.reg.DB.Exec(ctx, `INSERT INTO totp_factors (user_id, secret_sealed) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret_sealed = excluded.secret_sealed, last_used_step = 0, created_at = now()`,
		p.UserID, sealed)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_url": auth.TOTPURL(m.reg.AppName, u.Email, secret),
	})
	return nil
}

func (m *Module) totpConfirm(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if err := m.limit(ctx, "totp-confirm:"+p.UserID, 10, 15*time.Minute); err != nil {
		return err
	}
	codes := auth.NewRecoveryCodes(10)
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		var sealed []byte
		err := tx.QueryRow(ctx, "SELECT secret_sealed FROM totp_factors WHERE user_id = $1 AND confirmed_at IS NULL FOR UPDATE", p.UserID).Scan(&sealed)
		if db.IsNotFound(err) {
			return httpx.Conflict("start the setup first, or two-factor authentication is already on")
		}
		if err != nil {
			return err
		}
		secret, err := m.reg.Secrets.Open(sealed, "totp:"+p.UserID)
		if err != nil {
			return err
		}
		step, ok := auth.VerifyTOTP(string(secret), in.Code, time.Now(), 0)
		if !ok {
			return httpx.Invalid(httpx.FieldError{Field: "code", Message: "is not valid; check the time on your device"})
		}
		if _, err := tx.Exec(ctx, "UPDATE totp_factors SET confirmed_at = now(), last_used_step = $2 WHERE user_id = $1", p.UserID, step); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM recovery_codes WHERE user_id = $1", p.UserID); err != nil {
			return err
		}
		for _, c := range codes {
			if _, err := tx.Exec(ctx, "INSERT INTO recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)", id.New(), p.UserID, auth.HashToken(c)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "UPDATE sessions SET mfa_verified_at = now() WHERE id = $1", p.SessionID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorUserID: p.UserID, Action: "user.totp_enabled", TargetType: "user", TargetID: p.UserID, IP: m.reg.ClientIP(r)})
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
	return nil
}

func (m *Module) totpDisable(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if err := m.limit(ctx, "totp-disable:"+p.UserID, 10, time.Hour); err != nil {
		return err
	}
	if err := m.checkPassword(ctx, p.UserID, in.Password, "password"); err != nil {
		return err
	}
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM totp_factors WHERE user_id = $1", p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM recovery_codes WHERE user_id = $1", p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorUserID: p.UserID, Action: "user.totp_disabled", TargetType: "user", TargetID: p.UserID, IP: m.reg.ClientIP(r)})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (m *Module) sessions(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	list, err := m.listSessions(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (m *Module) revokeSession(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	sid := r.PathValue("id")
	if !id.Valid(sid) {
		return httpx.ErrNotFound
	}
	tag, err := m.reg.DB.Exec(r.Context(), "DELETE FROM sessions WHERE id = $1 AND user_id = $2", sid, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	if sid == p.SessionID {
		m.clearCookie(w)
	}
	httpx.NoContent(w)
	return nil
}

func (m *Module) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	if _, err := m.reg.DB.Exec(r.Context(), "DELETE FROM sessions WHERE user_id = $1 AND id <> $2", p.UserID, p.SessionID); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
