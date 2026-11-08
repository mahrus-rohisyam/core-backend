package db_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/db/dbtest"
)

func TestLoadMigrationsRejectsBadNames(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"no number":         {"create.sql": {Data: []byte("SELECT 1")}},
		"zero":              {"0000_x.sql": {Data: []byte("SELECT 1")}},
		"duplicate version": {"0001_a.sql": {Data: []byte("SELECT 1")}, "001_b.sql": {Data: []byte("SELECT 1")}},
	} {
		if _, err := db.LoadMigrations(fsys); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMigrateAppliesOnceInOrder(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	fsys := fstest.MapFS{
		"0002_add_column.sql": {Data: []byte("ALTER TABLE things ADD COLUMN size int")},
		"0001_create.sql":     {Data: []byte("CREATE TABLE things (id int PRIMARY KEY)")},
		"README.md":           {Data: []byte("ignored")},
	}

	done, err := db.Migrate(ctx, pool, "things", fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 || done[0] != "things/0001_create.sql" {
		t.Fatalf("applied %v, want 0001 then 0002", done)
	}
	again, err := db.Migrate(ctx, pool, "things", fsys)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run applied %v (err %v), want nothing", again, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO things (id, size) VALUES (1, 2)"); err != nil {
		t.Fatalf("table not migrated: %v", err)
	}
}

func TestMigrateFailureLeavesNoPartialMigration(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	fsys := fstest.MapFS{"0001_bad.sql": {Data: []byte("CREATE TABLE a (id int); SELECT * FROM missing_table")}}
	if _, err := db.Migrate(ctx, pool, "bad", fsys); err == nil {
		t.Fatal("expected an error")
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('a') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("table from a failed migration was left behind")
	}
}

func TestMigrateConcurrentStartsDoNotRace(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	fsys := fstest.MapFS{"0001_create.sql": {Data: []byte("CREATE TABLE once (id int)")}}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := db.Migrate(ctx, pool, "race", fsys)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if _, err := pool.Exec(ctx, "CREATE TABLE t (id int)"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows = %d (err %v), want 0 after rollback", n, err)
	}
}
