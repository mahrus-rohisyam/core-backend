// Package config reads the core's settings from APP_* environment variables.
//
// Apps with settings of their own read them with the helpers on Reader, so that every
// missing or malformed variable is reported in one error instead of one at a time.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Tenancy modes. See ADR 0004.
const (
	TenancySingle = "single"
	TenancyMulti  = "multi"
)

// Sign-up policies.
const (
	SignupInvite = "invite" // only the first user and invited people can sign up
	SignupOpen   = "open"   // anyone can sign up
)

// Config holds the core's settings.
type Config struct {
	PublicURL   *url.URL // APP_PUBLIC_URL, required
	HTTPAddr    string   // APP_HTTP_ADDR, default 127.0.0.1:8080
	DatabaseURL string   // APP_DATABASE_URL, required
	MasterKey   []byte   // APP_MASTER_KEY, required: 32 bytes, base64
	Tenancy     string   // APP_TENANCY: single (default) or multi
	Signup      string   // APP_SIGNUP: invite (default) or open
	LogLevel    string   // APP_LOG_LEVEL: debug, info (default), warn, error
	LogFormat   string   // APP_LOG_FORMAT: json (default) or text
	TrustProxy  bool     // APP_TRUST_PROXY: read the client IP from X-Forwarded-For (only behind a proxy)

	SessionIdle     time.Duration // APP_SESSION_IDLE, default 168h
	SessionLifetime time.Duration // APP_SESSION_LIFETIME, default 720h

	Mail Mail
}

// Mail holds outgoing email settings. With no SMTP host, emails are written to the log.
type Mail struct {
	From     string // APP_MAIL_FROM, required when APP_SMTP_HOST is set
	SMTPHost string // APP_SMTP_HOST
	SMTPPort int    // APP_SMTP_PORT, default 587
	SMTPUser string // APP_SMTP_USER
	SMTPPass string // APP_SMTP_PASSWORD
}

// Secure reports whether the app is served over https, which turns on Secure cookies and HSTS.
func (c *Config) Secure() bool { return c.PublicURL.Scheme == "https" }

// Load reads the configuration from the process environment.
func Load() (*Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads the configuration through getenv, so tests can pass a map.
func LoadFrom(getenv func(string) string) (*Config, error) {
	r := &Reader{getenv: getenv}
	c := &Config{
		PublicURL:       r.URL("APP_PUBLIC_URL"),
		HTTPAddr:        r.String("APP_HTTP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:     r.Required("APP_DATABASE_URL"),
		MasterKey:       r.Key32("APP_MASTER_KEY"),
		Tenancy:         r.OneOf("APP_TENANCY", TenancySingle, TenancySingle, TenancyMulti),
		Signup:          r.OneOf("APP_SIGNUP", SignupInvite, SignupInvite, SignupOpen),
		LogLevel:        r.OneOf("APP_LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		LogFormat:       r.OneOf("APP_LOG_FORMAT", "json", "json", "text"),
		TrustProxy:      r.OneOf("APP_TRUST_PROXY", "false", "true", "false") == "true",
		SessionIdle:     r.Duration("APP_SESSION_IDLE", 7*24*time.Hour),
		SessionLifetime: r.Duration("APP_SESSION_LIFETIME", 30*24*time.Hour),
		Mail: Mail{
			From:     r.String("APP_MAIL_FROM", ""),
			SMTPHost: r.String("APP_SMTP_HOST", ""),
			SMTPPort: r.Int("APP_SMTP_PORT", 587),
			SMTPUser: r.String("APP_SMTP_USER", ""),
			SMTPPass: r.String("APP_SMTP_PASSWORD", ""),
		},
	}
	if c.Mail.SMTPHost != "" && c.Mail.From == "" {
		r.fail("APP_MAIL_FROM", "is required when APP_SMTP_HOST is set")
	}
	if c.SessionIdle > c.SessionLifetime {
		r.fail("APP_SESSION_IDLE", "must not be longer than APP_SESSION_LIFETIME")
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// Summary lists the settings with secrets redacted, for `app check-config`.
func (c *Config) Summary() []string {
	redact := func(s string) string {
		if s == "" {
			return "(not set)"
		}
		return "(set, hidden)"
	}
	return []string{
		"APP_PUBLIC_URL=" + c.PublicURL.String(),
		"APP_HTTP_ADDR=" + c.HTTPAddr,
		"APP_DATABASE_URL=" + redactURL(c.DatabaseURL),
		"APP_MASTER_KEY=(set, hidden)",
		"APP_TENANCY=" + c.Tenancy,
		"APP_SIGNUP=" + c.Signup,
		"APP_LOG_LEVEL=" + c.LogLevel,
		"APP_LOG_FORMAT=" + c.LogFormat,
		"APP_TRUST_PROXY=" + strconv.FormatBool(c.TrustProxy),
		"APP_SESSION_IDLE=" + c.SessionIdle.String(),
		"APP_SESSION_LIFETIME=" + c.SessionLifetime.String(),
		"APP_MAIL_FROM=" + c.Mail.From,
		"APP_SMTP_HOST=" + c.Mail.SMTPHost,
		"APP_SMTP_PORT=" + strconv.Itoa(c.Mail.SMTPPort),
		"APP_SMTP_USER=" + c.Mail.SMTPUser,
		"APP_SMTP_PASSWORD=" + redact(c.Mail.SMTPPass),
	}
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(set, unparsable)"
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "hidden")
	}
	return u.String()
}

// Reader reads environment variables and collects every problem it finds.
type Reader struct {
	getenv func(string) string
	errs   []error
}

// NewReader returns a Reader over getenv (usually os.Getenv).
func NewReader(getenv func(string) string) *Reader { return &Reader{getenv: getenv} }

// Err returns all problems found so far, or nil.
func (r *Reader) Err() error { return errors.Join(r.errs...) }

func (r *Reader) fail(key, msg string) { r.errs = append(r.errs, fmt.Errorf("%s %s", key, msg)) }

func (r *Reader) get(key string) string { return strings.TrimSpace(r.getenv(key)) }

// String returns the variable, or def when it is empty.
func (r *Reader) String(key, def string) string {
	if v := r.get(key); v != "" {
		return v
	}
	return def
}

// Required returns the variable and records an error when it is empty.
func (r *Reader) Required(key string) string {
	v := r.get(key)
	if v == "" {
		r.fail(key, "is required")
	}
	return v
}

// OneOf returns the variable if it is one of allowed, def when empty, and records an error otherwise.
func (r *Reader) OneOf(key, def string, allowed ...string) string {
	v := r.get(key)
	if v == "" {
		return def
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.fail(key, "must be one of "+strings.Join(allowed, ", "))
	return def
}

// Int returns the variable as an int, or def when empty.
func (r *Reader) Int(key string, def int) int {
	v := r.get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, "must be a whole number")
		return def
	}
	return n
}

// Duration returns the variable as a time.Duration ("90m", "168h"), or def when empty.
func (r *Reader) Duration(key string, def time.Duration) time.Duration {
	v := r.get(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		r.fail(key, "must be a positive duration such as 90m or 168h")
		return def
	}
	return d
}

// URL returns the variable as an absolute http or https URL without a trailing slash. It is required.
func (r *Reader) URL(key string) *url.URL {
	v := strings.TrimRight(r.Required(key), "/")
	u, err := url.Parse(v)
	if v != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
		r.fail(key, "must be an absolute http or https URL")
	}
	if u == nil {
		u = &url.URL{}
	}
	return u
}

// Key32 returns the variable decoded from base64, which must be exactly 32 bytes. It is required.
func (r *Reader) Key32(key string) []byte {
	v := r.Required(key)
	if v == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 32 {
		r.fail(key, "must be 32 random bytes in base64 (openssl rand -base64 32)")
		return nil
	}
	return b
}
