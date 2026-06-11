package helper

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sqliteBusyTimeoutMillis = 10000

var sqliteDBLocks sync.Map

func withSQLiteRead(dbPath string, fn func(*sql.DB) error) error {
	lock := sqliteLock(dbPath)
	lock.RLock()
	defer lock.RUnlock()
	return withSQLiteRetry(dbPath, false, fn)
}

func withSQLiteWrite(dbPath string, fn func(*sql.DB) error) error {
	lock := sqliteLock(dbPath)
	lock.Lock()
	defer lock.Unlock()
	return withSQLiteRetry(dbPath, true, fn)
}

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

func withSQLiteDB(dbPath string, write bool, fn func(*sql.DB) error) error {
	db, err := openSQLite(dbPath, write)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}

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

func sqliteLock(dbPath string) *sync.RWMutex {
	key := sqliteLockKey(dbPath)
	lock, _ := sqliteDBLocks.LoadOrStore(key, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

func sqliteLockKey(dbPath string) string {
	if abs, err := filepath.Abs(dbPath); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dbPath)
}

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
