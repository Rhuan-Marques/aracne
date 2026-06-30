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
	key := sqliteLockKey(dbPath)
	onceVal, _ := sqliteMigrated.LoadOrStore(key, &sync.Once{})
	onceVal.(*sync.Once).Do(func() {
		if _, err := os.Stat(dbPath); err != nil {
			return
		}
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

// Opens a SQLite database connection with shared cache, busy timeout, and WAL mode for write access.
func openSQLite(dbPath string, write bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?cache=shared&_busy_timeout=%d", dbPath, sqliteBusyTimeoutMillis))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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
