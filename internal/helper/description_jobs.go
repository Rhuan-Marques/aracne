package helper

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The record of which resources a description fill is generating RIGHT NOW, and by whom.
//
// WHY IT EXISTS. A lazy fill no longer generates inside the read: it claims the resources it
// wants, hands them to a detached worker process and waits for whatever lands before its own
// deadline. Two things follow from that, and this table is both of them.
//
//  1. A second read that names a resource already being generated must NOT start a second
//     worker for it. It watches the claim instead. Every intercepted `cat` is a fresh process,
//     so the in-process `attempted` set in lazydesc.Filler cannot see this; only a row in the
//     database can.
//  2. A read renders whatever is done and leaves. The worker outlives it. Nothing else in the
//     process tree remembers that the worker exists, so the claim has to carry enough to answer
//     "is it still alive, and when does it stop being allowed to be" without asking the OS.
//
// WHY A HEARTBEAT AND NOT A LOCK FILE. flock is released by the kernel when a process dies,
// which is a better liveness signal than any timestamp -- on Unix. `lockPath` means something
// different on Windows (an O_EXCL file with a 2-minute mtime takeover, see filelock_windows.go),
// so a liveness predicate built on it would be exact on one platform and meaningless on the
// other, and "is the worker dead" is precisely the question where a platform split costs most.
// A heartbeat column is one UPDATE every DescriptionJobHeartbeatInterval, means the same thing
// everywhere, and is readable in the same statement as the claim itself.
//
// THE CLAIM IS A REAL COMPARE-AND-SWAP. One INSERT ... ON CONFLICT ... DO UPDATE ... WHERE
// runs in a single implicit transaction holding the database write lock, so two processes
// claiming the same resource in the same millisecond cannot both see RowsAffected() == 1. That
// is the same RowsAffected()-as-miss-signal idiom UpdateBugState and DeleteBug already use
// (db.go:1103-1140), for the same reason: two agents racing on one row.
//
// EVERY TIMESTAMP HERE IS UNIX MILLISECONDS. Seconds would be cheaper to read in a sqlite3
// shell, but the worker's first heartbeat can land in the same second as its claim, and this
// table has to tell "started and then died" apart from "never started at all" (see
// started_at column). Milliseconds also let a test drive a whole claim/stale/reclaim cycle in
// well under a second.
//
// Nothing here is load-bearing for correctness of the graph. A fill that cannot read or write
// this table behaves as though the feature were off, which is what every other failure in
// lazydesc does too.

// DescriptionJobState is the lifecycle of one claimed resource.
//
// There is no "done" state: a resource whose description landed has its row DELETED, and
// row-absent is the terminal state. That is what gives a watcher a clean happens-before -- the
// row is removed only after UpdateDescription has committed, so a watcher that sees no claim
// knows the description, if there is one, is already visible to it.
const (
	// DescriptionJobRunning is a live claim: a worker owns it and is expected to heartbeat.
	DescriptionJobRunning = "running"
	// DescriptionJobFailed is a claim whose worker gave up, or one whose worker never came up
	// at all. It holds the resource for DescriptionJobFailCooldown so a broken provider cannot
	// buy a fresh worker process on every read.
	DescriptionJobFailed = "failed"
	// DescriptionJobCancel asks the owning worker to stop. Cancellation is cooperative and goes
	// through this column rather than a signal: the worker notices on its next heartbeat, which
	// needs no pid (pids are recycled), no signal permissions and no platform split.
	DescriptionJobCancel = "cancel"
)

// The timings a claim is judged by. They are exported because the worker, the watcher and the
// reclaimer must all use the SAME numbers -- three copies of "how long until it is dead" is
// three chances to disagree about whether a worker is alive.
const (
	// DescriptionJobHeartbeatInterval is how often a worker refreshes its claims.
	DescriptionJobHeartbeatInterval = 5 * time.Second
	// DescriptionJobStaleAfter is how long a claim survives without a heartbeat. Four missed
	// beats: long enough that a worker blocked on one slow SQLITE_BUSY retry is not declared
	// dead, short enough to be invisible next to the 45s a read is willing to wait.
	DescriptionJobStaleAfter = 20 * time.Second
	// DescriptionJobLeaseSlack is added to the worker's own timeout to get expires_at. The
	// lease is the backstop for a worker that outlives even its watchdog: it bounds the CLAIM
	// where the watchdog bounds the PROCESS, so no imaginable survivor can wedge a resource.
	DescriptionJobLeaseSlack = 60 * time.Second
	// DescriptionJobFailCooldown is how long a failed claim holds its resource.
	DescriptionJobFailCooldown = 60 * time.Second

	// descriptionJobRowTTL is how long a settled row may linger before the GC drops it. Rows
	// are deleted as they complete, so this only ever catches rows from a worker that died
	// between writing and settling.
	descriptionJobRowTTL = time.Hour
	// maxStoredDescriptionJobs bounds the table the way maxStoredErrors bounds the scan-error
	// sample (db.go:353, added after a 23,902-row incident). Live rows are never dropped.
	maxStoredDescriptionJobs = 500
)

// DescriptionJobTarget is one resource a fill wants to claim.
type DescriptionJobTarget struct {
	ID string
	// Fingerprint is DescriptionAttemptFingerprint at claim time. A claim describing code that
	// has since moved is not worth waiting for, so a claim whose fingerprint differs from the
	// one on the row is allowed to take it over even while the old one is live.
	Fingerprint string
}

// DescriptionJob is one row, as read back.
type DescriptionJob struct {
	ResourceID  string
	JobID       string
	PID         int
	Fingerprint string
	State       string
	ClaimedAt   time.Time
	StartedAt   time.Time // zero when the worker never heartbeated
	HeartbeatAt time.Time
	ExpiresAt   time.Time
	Detail      string
}

// Live reports whether this claim still speaks for a worker that might yet deliver.
func (j DescriptionJob) Live(now time.Time) bool {
	if j.State != DescriptionJobRunning {
		return false
	}
	if !j.ExpiresAt.IsZero() && now.After(j.ExpiresAt) {
		return false
	}
	return now.Sub(j.HeartbeatAt) <= DescriptionJobStaleAfter
}

// DescriptionJobStatus is what a watcher polls: the description it is waiting for, and the
// claim that is supposed to produce it. Both come from ONE query -- see ReadDescriptionJobStatus.
type DescriptionJobStatus struct {
	Exists      bool // the resource is still in the graph
	Description string
	Job         DescriptionJob
	Claimed     bool
}

// createDescriptionJobsTable creates the table on demand.
//
// Separate from createSchema for the same reason createDescriptionAttemptsTable is: this is
// bookkeeping for an optional feature, not part of the graph. A database that never runs a lazy
// fill should not carry the table, and a reader that finds it missing must behave as though it
// were empty rather than fail.
func createDescriptionJobsTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS description_jobs (
        resource_id  TEXT PRIMARY KEY,
        job_id       TEXT NOT NULL,
        pid          INTEGER NOT NULL,
        fingerprint  TEXT NOT NULL DEFAULT '',
        state        TEXT NOT NULL,
        claimed_at   INTEGER NOT NULL,
        started_at   INTEGER NOT NULL DEFAULT 0,
        heartbeat_at INTEGER NOT NULL,
        expires_at   INTEGER NOT NULL,
        detail       TEXT NOT NULL DEFAULT ''
    )`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS description_jobs_job ON description_jobs(job_id)`)
	return err
}

// missingTable reports whether an error is sqlite saying the table has never been created. A
// reader that hits it must behave as though the table were empty -- the contract
// createDescriptionJobsTable's comment describes.
func missingTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}

func millis(t time.Time) int64 { return t.UnixNano() / int64(time.Millisecond) }

func atMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.Unix(0, ms*int64(time.Millisecond))
}

// ClaimDescriptionTargets is the partition every fill runs: it tries to claim all of `targets`
// and reports which ones this process now owns and which are already being generated by someone
// else who is still alive.
//
// THE RESULT IS BOTH LISTS, AND THAT IS THE POINT. A read that plans forty resources, three of
// which another worker is already describing, owns thirty-seven and watches forty. Doing one or
// the other -- spawning for everything, or standing down because something was taken -- are both
// wrong: the first duplicates paid work, the second abandons resources nobody is working on.
//
// `maxWorkers` is checked INSIDE the same transaction as the claims, not before it. Two reads
// starting at once would otherwise both read "one worker running", both decide there was room,
// and both spawn.  Being at the cap means claiming nothing at all -- there is no capacity to
// work it -- but the watch list is still returned, so a capped read is slower, never wronger.
func ClaimDescriptionTargets(dbPath, jobID string, pid int, targets []DescriptionJobTarget,
	now, expires time.Time, maxWorkers int) (won []string, watch []string, err error) {
	if len(targets) == 0 || jobID == "" {
		return nil, nil, nil
	}
	err = withSQLiteWrite(dbPath, func(db *sql.DB) error {
		// Reset per attempt: withSQLiteRetry may run this callback again after a SQLITE_BUSY,
		// and half-filled lists from the failed attempt must not survive into the retry.
		won, watch = nil, nil
		if cerr := createDescriptionJobsTable(db); cerr != nil {
			return cerr
		}
		tx, terr := db.Begin()
		if terr != nil {
			return terr
		}
		defer tx.Rollback()

		if gerr := gcDescriptionJobs(tx, now); gerr != nil {
			return gerr
		}
		// A claim whose worker never came up cannot mark itself failed -- it is not running to
		// do so. Without this it would simply go stale and be re-claimed, and a worker that
		// dies on startup (bad config, provider binary missing, panic in init) would buy a
		// fresh spawn on every single read, forever. Converting it to `failed` HERE, with the
		// cooldown anchored at now, is what makes a failed start distinguishable from a
		// mid-run death.
		if _, uerr := tx.Exec(
			`UPDATE description_jobs SET state = ?, detail = ?, heartbeat_at = ?
             WHERE state = ? AND started_at = 0 AND heartbeat_at <= ?`,
			DescriptionJobFailed, "worker never started", millis(now),
			DescriptionJobRunning, millis(now.Add(-DescriptionJobStaleAfter)),
		); uerr != nil {
			return uerr
		}

		capped := false
		if maxWorkers > 0 {
			live, cerr := countLiveWorkers(tx, jobID, now)
			if cerr != nil {
				return cerr
			}
			capped = live >= maxWorkers
		}

		ids := make([]string, 0, len(targets))
		byID := make(map[string]DescriptionJobTarget, len(targets))
		for _, t := range targets {
			if t.ID == "" || byID[t.ID].ID != "" {
				continue
			}
			byID[t.ID] = t
			ids = append(ids, t.ID)
		}
		sort.Strings(ids) // deterministic claim order, so two fills cannot deadlock on each other

		lost := make([]string, 0, len(ids))
		if !capped {
			stmt, perr := tx.Prepare(claimDescriptionJobSQL)
			if perr != nil {
				return perr
			}
			defer stmt.Close()
			for _, id := range ids {
				res, xerr := stmt.Exec(
					id, jobID, pid, byID[id].Fingerprint, DescriptionJobRunning,
					millis(now), millis(now), millis(expires),
					byID[id].Fingerprint,
					millis(now.Add(-DescriptionJobFailCooldown)),
					millis(now.Add(-DescriptionJobStaleAfter)),
					millis(now),
				)
				if xerr != nil {
					return fmt.Errorf("claim description job %s: %w", id, xerr)
				}
				n, aerr := res.RowsAffected()
				if aerr != nil {
					return aerr
				}
				if n == 1 {
					won = append(won, id)
					continue
				}
				lost = append(lost, id)
			}
		} else {
			lost = append(lost, ids...)
		}

		// A lost id is only worth watching if a LIVE worker holds it. Lost to a cooling-down
		// failure means nobody is generating it, and waiting would be waiting for nothing.
		if len(lost) > 0 {
			live, lerr := liveClaimedIDs(tx, lost, now)
			if lerr != nil {
				return lerr
			}
			watch = live
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, nil, err
	}
	return won, watch, nil
}

// claimDescriptionJobSQL is the compare-and-swap.
//
// The WHERE on the DO UPDATE is what makes it a claim rather than an overwrite: an existing row
// yields only when it is provably not someone's live work.
//
//   - a `failed` row yields once its cooldown has elapsed;
//   - any other row yields when its heartbeat has gone stale or its lease has expired;
//   - a row describing a DIFFERENT fingerprint yields immediately, live or not: it is generating
//     a description of code that has since moved, which nobody wants to wait for.
const claimDescriptionJobSQL = `
INSERT INTO description_jobs
    (resource_id, job_id, pid, fingerprint, state, claimed_at, started_at, heartbeat_at, expires_at, detail)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, '')
ON CONFLICT(resource_id) DO UPDATE SET
    job_id       = excluded.job_id,
    pid          = excluded.pid,
    fingerprint  = excluded.fingerprint,
    state        = excluded.state,
    claimed_at   = excluded.claimed_at,
    started_at   = 0,
    heartbeat_at = excluded.heartbeat_at,
    expires_at   = excluded.expires_at,
    detail       = ''
WHERE
    (description_jobs.fingerprint <> '' AND description_jobs.fingerprint <> ?)
    OR (description_jobs.state =  'failed' AND description_jobs.heartbeat_at <= ?)
    OR (description_jobs.state <> 'failed' AND (description_jobs.heartbeat_at <= ?
                                             OR description_jobs.expires_at   <= ?))`

// countLiveWorkers counts the distinct workers holding at least one live claim, ignoring jobID
// so a worker re-claiming its own resources does not count itself out.
func countLiveWorkers(tx *sql.Tx, jobID string, now time.Time) (int, error) {
	var n int
	err := tx.QueryRow(
		`SELECT COUNT(DISTINCT job_id) FROM description_jobs
         WHERE state = ? AND job_id <> ? AND heartbeat_at > ? AND expires_at > ?`,
		DescriptionJobRunning, jobID,
		millis(now.Add(-DescriptionJobStaleAfter)), millis(now),
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// liveClaimedIDs returns the subset of ids held by a claim that is still live.
func liveClaimedIDs(tx *sql.Tx, ids []string, now time.Time) ([]string, error) {
	var out []string
	for _, chunk := range chunkStrings(ids, sqliteMaxVariables-3) {
		args := make([]interface{}, 0, len(chunk)+3)
		args = append(args, DescriptionJobRunning,
			millis(now.Add(-DescriptionJobStaleAfter)), millis(now))
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := tx.Query(
			`SELECT resource_id FROM description_jobs
             WHERE state = ? AND heartbeat_at > ? AND expires_at > ?
               AND resource_id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if serr := rows.Scan(&id); serr != nil {
				rows.Close()
				return nil, serr
			}
			out = append(out, id)
		}
		if rerr := rows.Err(); rerr != nil {
			rows.Close()
			return nil, rerr
		}
		rows.Close()
	}
	sort.Strings(out)
	return out, nil
}

// gcDescriptionJobs drops settled rows, piggybacking on a transaction the caller is running
// anyway. No timer, no goroutine, no sweep command: the only moment the table can grow is a
// claim, so the only moment it needs trimming is a claim.
func gcDescriptionJobs(tx *sql.Tx, now time.Time) error {
	if _, err := tx.Exec(
		`DELETE FROM description_jobs WHERE state <> ? AND heartbeat_at < ?`,
		DescriptionJobRunning, millis(now.Add(-descriptionJobRowTTL)),
	); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM description_jobs`).Scan(&n); err != nil {
		return err
	}
	if n <= maxStoredDescriptionJobs {
		return nil
	}
	// Over the ceiling: drop the oldest rows that are not live work. A live claim is never
	// dropped -- deleting one would licence a second worker on a resource someone is already
	// describing, which is the one thing this table exists to prevent.
	_, err := tx.Exec(
		`DELETE FROM description_jobs WHERE resource_id IN (
             SELECT resource_id FROM description_jobs
             WHERE NOT (state = ? AND heartbeat_at > ? AND expires_at > ?)
             ORDER BY heartbeat_at ASC LIMIT ?)`,
		DescriptionJobRunning, millis(now.Add(-DescriptionJobStaleAfter)), millis(now),
		n-maxStoredDescriptionJobs,
	)
	return err
}

// ReadDescriptionJobStatus is the watcher's one query: for each id, the description it is
// waiting for and the claim that is meant to produce it.
//
// IT MUST STAY ONE CHEAP QUERY. The obvious implementation -- re-read the topology and look --
// costs 13ms on a small repository and 3.5 SECONDS on mui/material-ui (see ReadDb's comment at
// db.go:625), and a watcher runs it several times a second. This is a LEFT JOIN over at most
// max_nodes primary keys, which is the difference between a watch that is free and a watch that
// costs more than the generation it is waiting for.
func ReadDescriptionJobStatus(dbPath string, ids []string) (map[string]DescriptionJobStatus, error) {
	out := make(map[string]DescriptionJobStatus, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		out = make(map[string]DescriptionJobStatus, len(ids))
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			rows, qerr := db.Query(
				`SELECT r.id, COALESCE(r.description, ''),
                        COALESCE(j.job_id, ''), COALESCE(j.state, ''), COALESCE(j.pid, 0),
                        COALESCE(j.started_at, 0), COALESCE(j.heartbeat_at, 0),
                        COALESCE(j.expires_at, 0), COALESCE(j.detail, '')
                 FROM resources r LEFT JOIN description_jobs j ON j.resource_id = r.id
                 WHERE r.id IN (`+placeholders(len(chunk))+`)`, args...)
			if qerr != nil {
				if missingTable(qerr) {
					// The table has never been created, so nothing is claimed. Fall back to
					// the descriptions alone rather than reporting a failure: "no claims" is
					// the honest answer, and it is what a watcher can act on.
					return readDescriptionsInto(db, chunk, out)
				}
				return qerr
			}
			for rows.Next() {
				var (
					id, desc, jobID, state, detail    string
					pid                               int
					startedAt, heartbeatAt, expiresAt int64
				)
				if serr := rows.Scan(&id, &desc, &jobID, &state, &pid,
					&startedAt, &heartbeatAt, &expiresAt, &detail); serr != nil {
					rows.Close()
					return serr
				}
				st := DescriptionJobStatus{Exists: true, Description: desc}
				if jobID != "" {
					st.Claimed = true
					st.Job = DescriptionJob{
						ResourceID: id, JobID: jobID, PID: pid, State: state,
						StartedAt: atMillis(startedAt), HeartbeatAt: atMillis(heartbeatAt),
						ExpiresAt: atMillis(expiresAt), Detail: detail,
					}
				}
				out[id] = st
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

// readDescriptionsInto is the no-claims-table fallback for ReadDescriptionJobStatus.
func readDescriptionsInto(db *sql.DB, ids []string, out map[string]DescriptionJobStatus) error {
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(
		`SELECT id, COALESCE(description, '') FROM resources WHERE id IN (`+
			placeholders(len(ids))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, desc string
		if serr := rows.Scan(&id, &desc); serr != nil {
			return serr
		}
		out[id] = DescriptionJobStatus{Exists: true, Description: desc}
	}
	return rows.Err()
}

// ReadDescriptionJobTargets is how a worker learns what it owns.
//
// The targets are deliberately NOT passed in argv. Forty resource ids is kilobytes of command
// line, and argv has platform limits nobody should have to think about here; more importantly,
// it would make the worker's idea of its job and the table's idea of it two things that can
// disagree. The table is the only copy.
func ReadDescriptionJobTargets(dbPath, jobID string) ([]DescriptionJobTarget, error) {
	if jobID == "" {
		return nil, nil
	}
	var out []DescriptionJobTarget
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		out = nil
		rows, qerr := db.Query(
			`SELECT resource_id, fingerprint FROM description_jobs
             WHERE job_id = ? AND state = ? ORDER BY resource_id`, jobID, DescriptionJobRunning)
		if qerr != nil {
			if missingTable(qerr) {
				return nil
			}
			return qerr
		}
		defer rows.Close()
		for rows.Next() {
			var t DescriptionJobTarget
			if serr := rows.Scan(&t.ID, &t.Fingerprint); serr != nil {
				return serr
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HeartbeatDescriptionJob refreshes a worker's claims and reports how many it still owns.
//
// IT IS ALSO THE OWNERSHIP CHECK, and that is the more important half. A return of 0 means this
// worker no longer owns anything it thought it owned -- it was cancelled through the table, its
// lease expired and someone else re-claimed the resources, the rows were cleared by a hard
// rebuild, or the database is simply gone because the project was deleted underneath it. In
// every one of those cases the work it is doing is now duplicate work or work for nobody, and
// it must stop. One mechanism, four failure modes, no extra bookkeeping.
//
// It also stamps started_at, so a claim that never heartbeated at all is distinguishable from
// one whose worker ran and then died -- see the failed-start conversion in
// ClaimDescriptionTargets.
func HeartbeatDescriptionJob(dbPath, jobID string, now time.Time) (owned int, err error) {
	if jobID == "" {
		return 0, nil
	}
	err = withSQLiteWrite(dbPath, func(db *sql.DB) error {
		owned = 0
		res, xerr := db.Exec(
			`UPDATE description_jobs SET heartbeat_at = ?, started_at = ?
             WHERE job_id = ? AND state = ?`,
			millis(now), millis(now), jobID, DescriptionJobRunning)
		if xerr != nil {
			if missingTable(xerr) {
				return nil
			}
			return xerr
		}
		n, aerr := res.RowsAffected()
		if aerr != nil {
			return aerr
		}
		owned = int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return owned, nil
}

// DescriptionJobCancelled reports whether this job has been asked to stop.
func DescriptionJobCancelled(dbPath, jobID string) (bool, error) {
	if jobID == "" {
		return false, nil
	}
	var cancelled bool
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		cancelled = false
		var n int
		qerr := db.QueryRow(
			`SELECT COUNT(*) FROM description_jobs WHERE job_id = ? AND state = ?`,
			jobID, DescriptionJobCancel).Scan(&n)
		if qerr != nil {
			if missingTable(qerr) {
				return nil
			}
			return qerr
		}
		cancelled = n > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return cancelled, nil
}

// SettleDescriptionJobTargets deletes the claims for ids this worker has finished with,
// whatever the outcome was. Row-absent is the terminal state; see DescriptionJobState.
func SettleDescriptionJobTargets(dbPath, jobID string, ids []string) error {
	if jobID == "" || len(ids) == 0 {
		return nil
	}
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables-1) {
			args := make([]interface{}, 0, len(chunk)+1)
			args = append(args, jobID)
			for _, id := range chunk {
				args = append(args, id)
			}
			if _, err := db.Exec(
				`DELETE FROM description_jobs WHERE job_id = ? AND resource_id IN (`+
					placeholders(len(chunk))+`)`, args...); err != nil {
				if missingTable(err) {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

// MarkDescriptionJobFailed records that this worker gave up on these ids, holding them for
// DescriptionJobFailCooldown. heartbeat_at is the cooldown anchor, which is why it is stamped
// with now rather than left where it was.
func MarkDescriptionJobFailed(dbPath, jobID string, ids []string, detail string, now time.Time) error {
	if jobID == "" || len(ids) == 0 {
		return nil
	}
	if len(detail) > 200 {
		detail = detail[:200]
	}
	detail = strings.Join(strings.Fields(detail), " ")
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables-4) {
			args := make([]interface{}, 0, len(chunk)+4)
			args = append(args, DescriptionJobFailed, detail, millis(now), jobID)
			for _, id := range chunk {
				args = append(args, id)
			}
			if _, err := db.Exec(
				`UPDATE description_jobs SET state = ?, detail = ?, heartbeat_at = ?
                 WHERE job_id = ? AND resource_id IN (`+placeholders(len(chunk))+`)`,
				args...); err != nil {
				if missingTable(err) {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

// ReleaseDescriptionJob drops every claim held by one job.
//
// The compensating action when a spawn fails: the claims were taken on behalf of a worker that
// does not exist, and leaving them would make every later read watch a job that will never
// report. If this fails too, the claims were born stale-able (started_at = 0) and the next
// read's failed-start conversion collects them.
func ReleaseDescriptionJob(dbPath, jobID string) error {
	if jobID == "" {
		return nil
	}
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if _, err := db.Exec(`DELETE FROM description_jobs WHERE job_id = ?`, jobID); err != nil {
			if missingTable(err) {
				return nil
			}
			return err
		}
		return nil
	})
}

// RequestDescriptionJobCancel asks one job, or every job, to stop. The owning worker notices on
// its next heartbeat.
func RequestDescriptionJobCancel(dbPath, jobID string) (int, error) {
	var n int
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		n = 0
		var (
			res sql.Result
			err error
		)
		if jobID == "" {
			res, err = db.Exec(`UPDATE description_jobs SET state = ? WHERE state = ?`,
				DescriptionJobCancel, DescriptionJobRunning)
		} else {
			res, err = db.Exec(
				`UPDATE description_jobs SET state = ? WHERE job_id = ? AND state = ?`,
				DescriptionJobCancel, jobID, DescriptionJobRunning)
		}
		if err != nil {
			if missingTable(err) {
				return nil
			}
			return err
		}
		rows, aerr := res.RowsAffected()
		if aerr != nil {
			return aerr
		}
		n = int(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListDescriptionJobs returns every claim, newest heartbeat first, for `arac descriptions jobs`.
func ListDescriptionJobs(dbPath string) ([]DescriptionJob, error) {
	var out []DescriptionJob
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		out = nil
		rows, qerr := db.Query(
			`SELECT resource_id, job_id, pid, fingerprint, state,
                    claimed_at, started_at, heartbeat_at, expires_at, detail
             FROM description_jobs ORDER BY heartbeat_at DESC, resource_id`)
		if qerr != nil {
			if missingTable(qerr) {
				return nil
			}
			return qerr
		}
		defer rows.Close()
		for rows.Next() {
			var (
				j                                            DescriptionJob
				claimedAt, startedAt, heartbeatAt, expiresAt int64
			)
			if serr := rows.Scan(&j.ResourceID, &j.JobID, &j.PID, &j.Fingerprint, &j.State,
				&claimedAt, &startedAt, &heartbeatAt, &expiresAt, &j.Detail); serr != nil {
				return serr
			}
			j.ClaimedAt = atMillis(claimedAt)
			j.StartedAt = atMillis(startedAt)
			j.HeartbeatAt = atMillis(heartbeatAt)
			j.ExpiresAt = atMillis(expiresAt)
			out = append(out, j)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClearDescriptionJobs drops every claim.
//
// `descriptions clear` MUST call this, for the same reason it must clear the attempt ledger: a
// claim left behind by a worker that is no longer running would turn "regenerate this" into
// "watch a job that will never finish".
func ClearDescriptionJobs(dbPath string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if _, err := db.Exec(`DELETE FROM description_jobs`); err != nil {
			if missingTable(err) {
				return nil
			}
			return err
		}
		return nil
	})
}

// ClearStaleDescriptionJobs drops every claim EXCEPT the live ones.
//
// This is what a hard rebuild calls. It deliberately spares live claims: a worker generating
// right now will settle its own rows, and its writes either land (the resources survived the
// rebuild) or fail harmlessly on an id that no longer exists. Deleting a live claim would
// licence a second worker on a resource already being described, which costs real tokens to
// learn.
func ClearStaleDescriptionJobs(dbPath string, now time.Time) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if _, err := db.Exec(
			`DELETE FROM description_jobs
             WHERE NOT (state = ? AND heartbeat_at > ? AND expires_at > ?)`,
			DescriptionJobRunning, millis(now.Add(-DescriptionJobStaleAfter)), millis(now),
		); err != nil {
			if missingTable(err) {
				return nil
			}
			return err
		}
		return nil
	})
}
