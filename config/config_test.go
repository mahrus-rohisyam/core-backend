package config

import (
	"strings"
	"testing"
	"time"
)

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{
		"APP_PUBLIC_URL":   "https://notes.example.com/",
		"APP_DATABASE_URL": "postgres://u:p@db/app",
		"APP_MASTER_KEY":   testKey,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL.String() != "https://notes.example.com" {
		t.Errorf("PublicURL = %s, want trailing slash removed", c.PublicURL)
	}
	if !c.Secure() {
		t.Error("Secure() = false for https URL")
	}
	if c.HTTPAddr != "127.0.0.1:8080" || c.Tenancy != TenancySingle || c.Signup != SignupInvite {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.SessionIdle != 168*time.Hour || c.SessionLifetime != 720*time.Hour {
		t.Errorf("unexpected session defaults: %v %v", c.SessionIdle, c.SessionLifetime)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{
		"APP_PUBLIC_URL":   "notes.example.com",
		"APP_MASTER_KEY":   "c2hvcnQ=",
		"APP_TENANCY":      "many",
		"APP_SMTP_HOST":    "smtp.example.com",
		"APP_SESSION_IDLE": "forever",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"APP_PUBLIC_URL must be an absolute",
		"APP_DATABASE_URL is required",
		"APP_MASTER_KEY must be 32 random bytes",
		"APP_TENANCY must be one of single, multi",
		"APP_MAIL_FROM is required",
		"APP_SESSION_IDLE must be a positive duration",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestSummaryHidesSecrets(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{
		"APP_PUBLIC_URL":    "http://localhost:8080",
		"APP_DATABASE_URL":  "postgres://app:s3cret@db/app",
		"APP_MASTER_KEY":    testKey,
		"APP_SMTP_PASSWORD": "mailpass",
	}))
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(c.Summary(), "\n")
	for _, secret := range []string{"s3cret", "mailpass", testKey} {
		if strings.Contains(all, secret) {
			t.Errorf("summary leaks %q:\n%s", secret, all)
		}
	}
}
