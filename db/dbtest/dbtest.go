// Package dbtest gives each test a fresh, empty Postgres database.
//
// Set APP_TEST_DATABASE_URL to a database the tests may use to create and drop databases
// (for example postgres://core:core@127.0.0.1:55532/core). Tests fail, rather than skip,
// when it is missing, so a green run always means the database tests ran.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvVar names the environment variable with the admin connection URL.
const EnvVar = "APP_TEST_DATABASE_URL"

// URL creates a new database and returns its connection URL. The database is dropped when the
// test ends.
func URL(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Fatalf("%s is not set; start Postgres with `make up` and run tests with `make test`", EnvVar)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("dbtest: connect: %v", err)
	}
	defer conn.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "test_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Errorf("dbtest: connect for cleanup: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dbtest: drop database: %v", err)
		}
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("dbtest: parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name
	return u.String()
}

// Pool creates a new database and returns a pool connected to it, closed when the test ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), URL(t))
	if err != nil {
		t.Fatalf("dbtest: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
