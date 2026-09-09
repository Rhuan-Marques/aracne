package helper

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sqliteBusyTimeoutMillis = 10000

var sqliteDBLocks sync.Map

// Tracks which database paths have had schema migrations applied this process
// (keyed by canonical path → *sync.Once), so the one-time migration runs at most
// once per database per process.
var sqliteMigrated sync.Map

// Runs on-disk schema migrations exactly once per database per process, under the
// write lock and bypassing the public wrappers to avoid re-entrancy. The actual
// migration is idempotent and gated by PRAGMA user_version, so even across
// processes it is a cheap no-op after the first run. Missing databases are skipped
// (createSchema will produce a current-schema DB on the first write).
func ensureSQLiteMigrated(dbPath string) {
	// STAT BEFORE THE ONCE, not inside it. A missing database used to enter the Do and
	// return, consuming the Once for the life of the process -- so a database created
	// later by the same long-lived process (`arac serve`, `arac viz serve`, `arac scanner
	// run` all open a path before it necessarily exists) never had applyMigrations run
	// against it. Nothing to migrate is not the same fact as migrated.
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	key := sqliteLockKey(dbPath)
	onceVal, _ := sqliteMigrated.LoadOrStore(key, &sync.Once{})
	onceVal.(*sync.Once).Do(func() {
		lock := sqliteLock(dbPath)
		lock.Lock()
		defer lock.Unlock()
		db, err := openSQLite(dbPath, true)
		if err != nil {
			return
		}
		defer db.Close()
		_ = applyMigrations(db)
	})
}

// Acquires a read lock and executes a read-only database operation with automatic retry.
func withSQLiteRead(dbPath string, fn func(*sql.DB) error) error {
	ensureSQLiteMigrated(dbPath)
	lock := sqliteLock(dbPath)
	lock.RLock()
	defer lock.RUnlock()
	return withSQLiteRetry(dbPath, false, fn)
}

// Acquires a per-database mutex and executes a write operation with retry logic.
func withSQLiteWrite(dbPath string, fn func(*sql.DB) error) error {
	ensureSQLiteMigrated(dbPath)
	lock := sqliteLock(dbPath)
	lock.Lock()
	defer lock.Unlock()
	return withSQLiteRetry(dbPath, true, fn)
}

// Executes a database operation with exponential backoff retry logic for SQLite lock contention.
func withSQLiteRetry(dbPath string, write bool, fn func(*sql.DB) error) error {
	var err error
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		err = withSQLiteDB(dbPath, write, fn)
		if err == nil || !isSQLiteLocked(err) {
			return err
		}
		time.Sleep(backoff)
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}
	return err
}

// Opens a SQLite database and executes a callback function, ensuring the connection closes afterward.
func withSQLiteDB(dbPath string, write bool, fn func(*sql.DB) error) error {
	db, err := openSQLite(dbPath, write)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}

// Opens a SQLite database connection with a busy timeout, and WAL mode for write access.
//
// THE DSN PARAMETERS HAVE TO BE SPELLED THE WAY THIS DRIVER READS THEM. They used to be
// `cache=shared&_busy_timeout=N`, which are mattn/go-sqlite3's names: modernc.org/sqlite's
// applyQueryParams reads only `_pragma`, `_time_format`, `_time_integer_format`, `_timezone`,
// `_txlock` and `vfs`, and discards everything else without an error. So both parameters were
// dead text that looked like configuration -- `PRAGMA busy_timeout` came back 0 on a connection
// whose DSN asked for ten seconds.
//
// It survived here because the pragmas below are issued explicitly. It did not survive in
// GetCallers/ReadBugs/UpdateBugState, which opened their own connections with the same dead DSN
// and no follow-up pragmas, and so ran with no busy timeout at all; those now go through this
// function. Shared cache is simply gone: with SetMaxOpenConns(1) there is no second connection
// for a cache to be shared with.
func openSQLite(dbPath string, write bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite",
		fmt.Sprintf("%s?_pragma=busy_timeout(%d)", dbPath, sqliteBusyTimeoutMillis))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// busy_timeout is set twice on purpose. The DSN form applies to every connection the pool
	// opens, including one opened later to replace a dropped one; the Exec below applies to
	// whichever connection happens to serve it. The DSN is the durable half.
	pragmas := fmt.Sprintf(`
		PRAGMA busy_timeout = %d;
		PRAGMA foreign_keys = ON;
	`, sqliteBusyTimeoutMillis)
	if write {
		pragmas += `
			PRAGMA journal_mode = WAL;
			PRAGMA synchronous = NORMAL;
		`
	}
	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Returns or creates a per-database sync.RWMutex for SQLite access coordination.
func sqliteLock(dbPath string) *sync.RWMutex {
	key := sqliteLockKey(dbPath)
	lock, _ := sqliteDBLocks.LoadOrStore(key, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// Returns a canonical lock key for a SQLite database path by normalizing to absolute path.
func sqliteLockKey(dbPath string) string {
	if abs, err := filepath.Abs(dbPath); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dbPath)
}

// Detects SQLite lock errors by checking error message for database locking patterns.
func isSQLiteLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sql logic error: database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "(261)")
}
