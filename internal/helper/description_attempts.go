package helper

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The record of which resources a description fill has already tried and failed to describe.
//
// WHY IT IS PERSISTED. The lazy filler keeps an in-process `attempted` set so a resource the
// model declines to describe -- an empty function, a generated stub, a body the cut could not
// read -- is not re-planned by every later read that names it. That guard was scoped to the
// process, on the reasoning that `arac` is mostly one-shot. It is, and that is exactly why the
// guard never guarded anything on the terminal surface: every intercepted `cat` and `grep` is
// a fresh process, so each one re-planned the same undescribed nodes and paid for them again,
// which with `provider: "cli"` means spawning the provider command per read.
//
// WHY A FINGERPRINT AND NOT JUST AN ID. A failure has to expire when the code changes, or a
// resource that was undescribable in one shape is undescribable forever. The fingerprint is
// the resource's identity as the planner sees it -- kind, name and span -- so a declaration
// that moved, was renamed, or grew gets a fresh attempt, and one that did not is left alone.
// It is deliberately cheap: everything in it is already in the topology row, so recording an
// attempt costs no file read.
//
// Nothing here is load-bearing for correctness. Every failure is best-effort: a fill that
// cannot read or write this table simply behaves as it did before, which is to say it tries
// again.

// DescriptionAttemptFingerprint is the identity a recorded attempt is keyed on.
func DescriptionAttemptFingerprint(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(string(res.Kind))
	b.WriteByte('\x1f')
	b.WriteString(res.Name)
	b.WriteByte('\x1f')
	b.WriteString(res.Location.Path)
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(res.Location.StartsAt))
	b.WriteByte('-')
	b.WriteString(strconv.Itoa(res.Location.EndsAt))
	return b.String()
}

// createDescriptionAttemptsTable creates the table on demand.
//
// Separate from createSchema because this is bookkeeping for an optional feature, not part of
// the graph: a database that never runs a lazy fill should not carry the table, and a reader
// that finds it missing must behave as though it were empty rather than fail.
func createDescriptionAttemptsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS description_attempts (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL
	)`)
	return err
}

// ReadDescriptionAttempts returns, for the given ids, the fingerprint each was last attempted
// under. An id with no record, and every id when the table does not exist yet, is absent from
// the result.
func ReadDescriptionAttempts(dbPath string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		// Reset per attempt: withSQLiteRetry may run this callback again after a
		// SQLITE_BUSY, and a half-filled map from the failed attempt must not survive.
		out = make(map[string]string, len(ids))
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			rows, qerr := db.Query(
				"SELECT id, fingerprint FROM description_attempts WHERE id IN ("+placeholders(len(chunk))+")", args...)
			if qerr != nil {
				// No table yet is the ordinary state of a project that has never filled a
				// description, and it means the same thing as no rows.
				if strings.Contains(strings.ToLower(qerr.Error()), "no such table") {
					return nil
				}
				return qerr
			}
			for rows.Next() {
				var id, fp string
				if serr := rows.Scan(&id, &fp); serr != nil {
					rows.Close()
					return serr
				}
				out[id] = fp
			}
			if rerr := rows.Err(); rerr != nil {
				rows.Close()
				return rerr
			}
			rows.Close()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordDescriptionAttempts stores the fingerprint each id was attempted under, replacing any
// earlier record for the same id.
func RecordDescriptionAttempts(dbPath string, attempts map[string]string) error {
	if len(attempts) == 0 {
		return nil
	}
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if err := createDescriptionAttemptsTable(db); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare("INSERT INTO description_attempts (id, fingerprint) VALUES (?, ?) " +
			"ON CONFLICT(id) DO UPDATE SET fingerprint = excluded.fingerprint")
		if err != nil {
			return err
		}
		defer stmt.Close()
		ids := make([]string, 0, len(attempts))
		for id := range attempts {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic write order
		for _, id := range ids {
			if id == "" {
				continue
			}
			if _, err := stmt.Exec(id, attempts[id]); err != nil {
				return fmt.Errorf("record description attempt %s: %w", id, err)
			}
		}
		return tx.Commit()
	})
}

// ClearDescriptionAttempts drops every recorded attempt.
//
// `descriptions clear` MUST call this. Clearing a description asks for it to be written again,
// and a recorded attempt whose fingerprint still matches would suppress exactly that -- the
// ledger would turn "regenerate this" into "never try this again".
func ClearDescriptionAttempts(dbPath string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if _, err := db.Exec("DELETE FROM description_attempts"); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				return nil
			}
			return err
		}
		return nil
	})
}

// ForgetDescriptionAttempts drops the recorded attempts for the given ids, so the next fill
// tries them again.
func ForgetDescriptionAttempts(dbPath string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			if _, err := db.Exec(
				"DELETE FROM description_attempts WHERE id IN ("+placeholders(len(chunk))+")", args...); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "no such table") {
					return nil
				}
				return err
			}
		}
		return nil
	})
}
