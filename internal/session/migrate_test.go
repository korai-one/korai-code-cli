package session_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	korai "github.com/korai-one/korai-sdk-go"
	sdksession "github.com/korai-one/korai-sdk-go/session"
	_ "modernc.org/sqlite"

	"github.com/Nevaero/korai-code-cli/internal/session"
)

// legacySchema is the sessions table as an SDK version predating the `tool`
// column wrote it. A database in this shape is what breaks: the current SDK's
// CREATE TABLE IF NOT EXISTS sees an existing table and adds nothing, so every
// INSERT fails on the missing column.
const legacySchema = `
CREATE TABLE sessions (
	id       TEXT PRIMARY KEY,
	created  TEXT NOT NULL,
	updated  TEXT NOT NULL,
	cwd      TEXT NOT NULL,
	model    TEXT NOT NULL,
	enc      TEXT NOT NULL,
	messages BLOB NOT NULL
);`

// writeLegacyDB creates a sessions database in the pre-`tool` shape, optionally
// with one row present, and returns its path.
func writeLegacyDB(t *testing.T, withRow bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if withRow {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := db.Exec(
			`INSERT INTO sessions (id, created, updated, cwd, model, enc, messages) VALUES (?,?,?,?,?,?,?)`,
			"old-1", now, now, "/tmp/proj", "auto", "none", []byte("[]"),
		); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}
	return path
}

// columnNames returns the column set of the sessions table.
func columnNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT name FROM pragma_table_info('sessions')")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[n] = true
	}
	return cols
}

// TestMigrateSQLiteAddsMissingColumn covers the actual break: a legacy database
// gains the column, and the columns it already had are untouched.
func TestMigrateSQLiteAddsMissingColumn(t *testing.T) {
	t.Parallel()

	path := writeLegacyDB(t, false)
	if cols := columnNames(t, path); cols["tool"] {
		t.Fatal("legacy fixture already has a tool column; fixture is wrong")
	}

	added, err := session.MigrateSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("MigrateSQLite: %v", err)
	}
	if len(added) != 1 || added[0] != "tool" {
		t.Errorf("added = %v, want [tool]", added)
	}
	cols := columnNames(t, path)
	for _, want := range []string{"id", "created", "updated", "cwd", "model", "tool", "enc", "messages"} {
		if !cols[want] {
			t.Errorf("column %q missing after migration", want)
		}
	}
}

// TestMigrateSQLitePreservesRows pins that migrating a populated database keeps
// existing sessions and backfills the new column with its default.
func TestMigrateSQLitePreservesRows(t *testing.T) {
	t.Parallel()

	path := writeLegacyDB(t, true)
	if _, err := session.MigrateSQLite(context.Background(), path); err != nil {
		t.Fatalf("MigrateSQLite: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var id, tool string
	if err := db.QueryRow(`SELECT id, tool FROM sessions WHERE id = 'old-1'`).Scan(&id, &tool); err != nil {
		t.Fatalf("existing row lost: %v", err)
	}
	if tool != "" {
		t.Errorf("backfilled tool = %q, want empty default", tool)
	}
}

// TestMigrateSQLiteIdempotentAndAbsent covers the no-op paths: a database
// already at the current schema, and one that does not exist yet.
func TestMigrateSQLiteIdempotentAndAbsent(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nope.db")
	added, err := session.MigrateSQLite(context.Background(), missing)
	if err != nil || added != nil {
		t.Errorf("absent db: added=%v err=%v, want nil/nil", added, err)
	}

	path := writeLegacyDB(t, false)
	if _, err := session.MigrateSQLite(context.Background(), path); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	again, err := session.MigrateSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second migrate added %v, want nothing", again)
	}
}

// TestMigratedDBAcceptsSDKSave is the end-to-end regression: save through the
// real SDK store against a legacy database. Without the migration this fails
// with "table sessions has no column named tool" — the bug that silently
// disabled --continue, --resume and sync.
func TestMigratedDBAcceptsSDKSave(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := writeLegacyDB(t, false)

	// Establish the failure first, so the test proves the fix rather than
	// merely exercising it.
	unmigrated, err := sdksession.NewSQLiteStore(ctx, path)
	if err != nil {
		t.Fatalf("open unmigrated store: %v", err)
	}
	sess := korai.Session{
		ID: "s-1", CWD: "/tmp/proj", Model: "auto", Tool: session.Tool,
		Created: time.Now().UTC(), Updated: time.Now().UTC(),
	}
	if err := unmigrated.Save(sess); err == nil {
		t.Fatal("save against a legacy db unexpectedly succeeded; the bug this guards is gone from the SDK — retire MigrateSQLite")
	}
	if err := unmigrated.Close(); err != nil {
		t.Fatalf("close unmigrated store: %v", err)
	}

	if _, err := session.MigrateSQLite(ctx, path); err != nil {
		t.Fatalf("MigrateSQLite: %v", err)
	}

	store, err := sdksession.NewSQLiteStore(ctx, path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Save(sess); err != nil {
		t.Fatalf("save after migration: %v", err)
	}
	got, err := store.Load("s-1")
	if err != nil {
		t.Fatalf("load after migration: %v", err)
	}
	if got.ID != "s-1" || got.Tool != session.Tool {
		t.Errorf("round-tripped session = {ID:%q Tool:%q}, want {s-1 %s}", got.ID, got.Tool, session.Tool)
	}
}
