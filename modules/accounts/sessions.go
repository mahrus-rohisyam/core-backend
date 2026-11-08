package accounts

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

// cookieName returns the session cookie name. Over https it uses the __Host- prefix, which the
// browser only accepts with Secure, Path=/ and no Domain, so a subdomain cannot overwrite it.
func (m *Module) cookieName() string {
	if m.reg.Config.Secure() {
		return "__Host-session"
	}
	return "session"
}

func (m *Module) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName(),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   m.reg.Config.Secure(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Module) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: m.reg.Config.Secure(), SameSite: http.SameSiteLaxMode,
	})
}

type newSession struct {
	token string
	csrf  string
}

// startSession creates a session for userID and sets the cookie.
func (m *Module) startSession(ctx context.Context, q db.DBTX, w http.ResponseWriter, r *http.Request, userID string, mfa bool) (newSession, error) {
	token, hash := auth.NewToken("")
	csrf, _ := auth.NewToken("")
	expires := time.Now().Add(m.reg.Config.SessionLifetime)
	var mfaAt *time.Time
	if mfa {
		now := time.Now()
		mfaAt = &now
	}
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err := q.Exec(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, csrf_token, mfa_verified_at, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::inet, $7, $8)`,
		id.New(), hash, userID, csrf, mfaAt, m.reg.ClientIP(r), ua, expires)
	if err != nil {
		return newSession{}, fmt.Errorf("accounts: create session: %w", err)
	}
	m.setCookie(w, token, expires)
	return newSession{token: token, csrf: csrf}, nil
}

// sessionAuth identifies callers by the session cookie.
type sessionAuth struct{ m *Module }

func (a sessionAuth) Authenticate(r *http.Request) (*rbac.Principal, error) {
	c, err := r.Cookie(a.m.cookieName())
	if err != nil || c.Value == "" || len(c.Value) > 100 {
		return nil, nil
	}
	ctx := r.Context()
	idle := a.m.reg.Config.SessionIdle
	var p rbac.Principal
	var lastSeen time.Time
	err = a.m.reg.DB.QueryRow(ctx, `
		SELECT s.id::text, s.user_id::text, s.csrf_token, s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND s.last_seen_at > now() - $2::interval
		  AND u.status = 'active'`,
		auth.HashToken(c.Value), idle.String()).Scan(&p.SessionID, &p.UserID, &p.CSRFToken, &lastSeen)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: load session: %w", err)
	}
	if time.Since(lastSeen) > time.Minute {
		_, _ = a.m.reg.DB.Exec(ctx, "UPDATE sessions SET last_seen_at = now() WHERE id = $1", p.SessionID)
	}
	return &p, nil
}

// SessionInfo is one entry in the account's session list.
type SessionInfo struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

func (m *Module) listSessions(ctx context.Context, userID, currentID string) ([]SessionInfo, error) {
	rows, err := m.reg.DB.Query(ctx, `
		SELECT id::text, coalesce(host(ip), ''), user_agent, created_at, last_seen_at
		FROM sessions WHERE user_id = $1 AND expires_at > now() AND last_seen_at > now() - $2::interval
		ORDER BY last_seen_at DESC`, userID, m.reg.Config.SessionIdle.String())
	if err != nil {
		return nil, fmt.Errorf("accounts: list sessions: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SessionInfo, error) {
		var s SessionInfo
		err := r.Scan(&s.ID, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt)
		s.Current = s.ID == currentID
		return s, err
	})
}

func (m *Module) purge(ctx context.Context) error {
	if _, err := m.reg.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now() OR last_seen_at < now() - $1::interval`,
		m.reg.Config.SessionIdle.String()); err != nil {
		return err
	}
	_, err := m.reg.DB.Exec(ctx, `DELETE FROM one_time_tokens WHERE expires_at < now() - interval '7 days'`)
	return err
}
