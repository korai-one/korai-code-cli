package session

// migrate.go — forward-compatibility for pre-existing session databases.
//
// The SDK's SQLite store declares its schema as a single
// `CREATE TABLE IF NOT EXISTS sessions (...)`. That statement creates the table
// correctly on a fresh machine, but it is a NO-OP against a database whose
// table already exists — including one created by an SDK version that predates
// a column. The table is then permanently short a column, every INSERT fails
// with "table sessions has no column named <x>", and because the CLI only warns
// on a failed save the breakage is silent: no session is ever persisted, so
// --continue, --resume, /resume and sync all quietly do nothing.
//
// The SDK owns the schema but cannot repair a database it did not create in
// this process, so reconciliation belongs to the caller that owns the file path
// — us. MigrateSQLite runs before the store is opened and adds any column the
// current SDK schema expects but an older file lacks.
//
// TODO(sdk): delete this once korai-sdk-go's session store performs its own
// ALTER TABLE migration; until then it must stay in lockstep with that schema.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// sqliteAdditiveColumns are the columns the SDK's sessions schema declares that
// a database written by an older SDK may not have. Each DDL carries a DEFAULT
// so `ALTER TABLE ... ADD COLUMN` succeeds even on a populated table — SQLite
// rejects adding a NOT NULL column without one. Ordered oldest-addition-first;
// append here when the SDK schema grows.
var sqliteAdditiveColumns = []struct {
	name string
	ddl  string
}{
	{name: "tool", ddl: `tool TEXT NOT NULL DEFAULT ''`},
}

// MigrateSQLite brings the session database at path up to the column set the
// SDK's schema expects, adding whatever an older file is missing. It returns
// the columns it added, if any.
//
// It is a no-op — nil error, nil slice — when the file does not exist or has no
// sessions table yet, because the SDK creates a correct schema in those cases.
// Callers should log a failure and carry on rather than abort: the store open
// that follows has its own fallback, and a database this cannot repair is no
// worse off than before.
func MigrateSQLite(ctx context.Context, path string) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // fresh install; the SDK will create the table
		}
		return nil, fmt.Errorf("stat session db %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening session db %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	have, err := sqliteColumns(ctx, db, "sessions")
	if err != nil {
		return nil, err
	}
	if len(have) == 0 {
		return nil, nil // no sessions table yet; the SDK will create it
	}

	var added []string
	for _, col := range sqliteAdditiveColumns {
		if have[col.name] {
			continue
		}
		// ALTER TABLE ADD COLUMN is not parameterizable; col.ddl is a package
		// constant, never caller input.
		if _, err := db.ExecContext(ctx, "ALTER TABLE sessions ADD COLUMN "+col.ddl); err != nil {
			return added, fmt.Errorf("adding column %s to session db %s: %w", col.name, path, err)
		}
		added = append(added, col.name)
	}
	return added, nil
}

// sqliteColumns returns the set of column names on table. An absent table
// yields an empty set and no error — PRAGMA table_info reports no rows rather
// than failing.
func sqliteColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning %s columns: %w", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	return cols, nil
}
