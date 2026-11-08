// Package app is the composition root: it loads config, connects to Postgres, runs migrations,
// mounts modules and serves HTTP until it is told to stop.
package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mahrus-rohisyam/core-backend/audit"
	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/mail"
	"github.com/mahrus-rohisyam/core-backend/rbac"
	"github.com/mahrus-rohisyam/core-backend/secrets"
	"github.com/mahrus-rohisyam/core-backend/web"
)

// Module is implemented by every feature, in the core and in apps.
type Module interface {
	// Name is unique, lowercase and stable: it keys the module's migrations.
	Name() string
	// Migrations returns the module's SQL files (NNNN_description.sql), or nil.
	Migrations() fs.FS
	// Permissions lists what roles can grant, for example "notes.read".
	Permissions() []rbac.Permission
	// Register mounts routes, authenticators and periodic tasks.
	Register(r *Registry) error
}

// Options are the parts of an app that are code, not configuration.
type Options struct {
	// Name is shown in emails and authenticator apps.
	Name string
	// SPA is the React build (the folder with index.html). Nil serves a placeholder page.
	SPA fs.FS
	// Mail overrides the sender chosen from configuration (tests use mail.Memory).
	Mail mail.Sender
	// Logger overrides the logger built from configuration.
	Logger *slog.Logger
	// AuditRetention is how long audit events are kept. Default one year.
	AuditRetention time.Duration
}

//go:embed migrations/*.sql
var coreMigrations embed.FS

// App is a configured, migrated application.
type App struct {
	cfg      *config.Config
	pool     *pgxpool.Pool
	logger   *slog.Logger
	handler  http.Handler
	registry *Registry
}

// New connects, migrates and wires the modules. The caller must call Close.
func New(ctx context.Context, cfg *config.Config, opts Options, modules ...Module) (*App, error) {
	logger := opts.Logger
	if logger == nil {
		logger = newLogger(cfg)
	}
	if opts.Name == "" {
		opts.Name = "App"
	}
	if opts.AuditRetention == 0 {
		opts.AuditRetention = 365 * 24 * time.Hour
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			pool.Close()
		}
	}()

	if err := migrate(ctx, pool, logger, modules); err != nil {
		return nil, err
	}

	box, err := secrets.New(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	sender := opts.Mail
	if sender == nil {
		if cfg.Mail.SMTPHost != "" {
			sender = &mail.SMTP{Host: cfg.Mail.SMTPHost, Port: cfg.Mail.SMTPPort, User: cfg.Mail.SMTPUser, Password: cfg.Mail.SMTPPass, From: cfg.Mail.From}
		} else {
			sender = &mail.Log{Logger: logger}
		}
	}

	perms, err := collectPermissions(modules)
	if err != nil {
		return nil, err
	}

	reg := &Registry{
		AppName:     opts.Name,
		Config:      cfg,
		DB:          pool,
		Logger:      logger,
		Mail:        sender,
		Secrets:     box,
		Limiter:     auth.NewLimiter(pool),
		Permissions: perms,
		router:      httpx.NewRouter(),
	}
	reg.Every("rate-limit-purge", time.Hour, reg.Limiter.Purge)
	reg.Every("audit-purge", 24*time.Hour, func(ctx context.Context) error { return audit.Purge(ctx, pool, opts.AuditRetention) })

	reg.router.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) error {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return nil
	})
	reg.router.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return httpx.NewError(http.StatusServiceUnavailable, "not-ready", "Database is not reachable")
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return nil
	})

	for _, m := range modules {
		if err := m.Register(reg); err != nil {
			return nil, fmt.Errorf("app: register %s: %w", m.Name(), err)
		}
	}
	if reg.resolver == nil {
		return nil, errors.New("app: no permission resolver registered; include the orgs module")
	}

	spaFS := opts.SPA
	if spaFS == nil {
		spaFS = web.Placeholder
	}
	spa, err := web.SPA(spaFS)
	if err != nil {
		return nil, fmt.Errorf("app: SPA: %w", err)
	}
	reg.router.Handle("GET /app/", spa)
	reg.router.Handle("GET /{$}", http.RedirectHandler(web.Base, http.StatusFound))

	handler := httpx.Chain(reg.router,
		httpx.Base(logger),
		httpx.SecurityHeaders(cfg.Secure()),
		reg.authenticate,
		reg.checkOrigin,
		reg.checkCSRF,
	)
	ok = true
	return &App{cfg: cfg, pool: pool, logger: logger, handler: handler, registry: reg}, nil
}

// Handler returns the app's HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Routes lists the registered route patterns.
func (a *App) Routes() []string { return a.registry.router.Routes() }

// DB returns the database pool.
func (a *App) DB() *pgxpool.Pool { return a.pool }

// Close releases the database pool.
func (a *App) Close() { a.pool.Close() }

// RunTasks runs the periodic tasks until ctx is done.
func (a *App) RunTasks(ctx context.Context) { a.registry.runTasks(ctx) }

// Serve listens on the configured address until ctx is done, then shuts down gracefully.
func (a *App) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.cfg.HTTPAddr,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() {
		a.logger.Info("listening", slog.String("addr", a.cfg.HTTPAddr), slog.String("public_url", a.cfg.PublicURL.String()))
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	a.logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// Run is the entry point for an app's main function. It reads the command from os.Args:
//
//	serve         web server and periodic tasks (default)
//	migrate       apply migrations and exit
//	check-config  validate configuration and print it with secrets hidden
//
// It exits the process with status 1 on error.
func Run(opts Options, modules ...Module) {
	if err := run(os.Args[1:], opts, modules); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, opts Options, modules []Module) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "check-config":
		fmt.Println(strings.Join(cfg.Summary(), "\n"))
		return nil
	case "migrate":
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		return migrate(ctx, pool, newLogger(cfg), modules)
	case "serve":
		a, err := New(ctx, cfg, opts, modules...)
		if err != nil {
			return err
		}
		defer a.Close()
		go a.RunTasks(ctx)
		if err := a.Serve(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q; use serve, migrate or check-config", cmd)
	}
}

func migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, modules []Module) error {
	core, err := fs.Sub(coreMigrations, "migrations")
	if err != nil {
		return err
	}
	type set struct {
		name string
		fs   fs.FS
	}
	sets := []set{{"core", core}}
	seen := map[string]bool{"core": true}
	for _, m := range modules {
		if seen[m.Name()] {
			return fmt.Errorf("app: two modules are named %q", m.Name())
		}
		seen[m.Name()] = true
		if fsys := m.Migrations(); fsys != nil {
			sets = append(sets, set{m.Name(), fsys})
		}
	}
	for _, s := range sets {
		done, err := db.Migrate(ctx, pool, s.name, s.fs)
		if err != nil {
			return err
		}
		for _, d := range done {
			logger.Info("migration applied", slog.String("migration", d))
		}
	}
	return nil
}

func collectPermissions(modules []Module) ([]rbac.Permission, error) {
	var all []rbac.Permission
	seen := map[string]string{}
	for _, m := range modules {
		for _, p := range m.Permissions() {
			if !p.Valid() {
				return nil, fmt.Errorf("app: module %s declares invalid permission %q (use area.action)", m.Name(), p.Key)
			}
			if prev, dup := seen[p.Key]; dup {
				return nil, fmt.Errorf("app: permission %q declared by %s and %s", p.Key, prev, m.Name())
			}
			seen[p.Key] = m.Name()
			all = append(all, p)
		}
	}
	return all, nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	o := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, o))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, o))
}
