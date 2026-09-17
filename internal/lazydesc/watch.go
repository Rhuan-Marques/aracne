package lazydesc

import (
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// Waiting for a background worker, from inside the read that started it.
//
// WHAT IT MUST NOT DO is the shape of the whole thing. It must not call ReadAll -- that is 13ms
// on a small repository and 3.5 SECONDS on mui/material-ui (see ReadDb, db.go), and a watcher
// runs several times a second, so the wait would cost more than the generation it is waiting for.
// It must not start a goroutine: the old inline fill ran parallel+2 of them per read and
// abandoned them on its deadline, and replacing that with a wait that can outlive its own caller
// would be a worse trade than the one being undone. And it must not be able to hang: an
// intercepted `cat` is on the other end of it.
//
// So it is a plain loop on the calling goroutine, polling one small query, and every iteration
// either shrinks the set it is waiting for or moves the clock toward a deadline it cannot pass.
// There is nothing to leak and nothing to abandon.
const descriptionPollInterval = 300 * time.Millisecond

// watchOutcome is what one poll concluded about one resource.
type watchOutcome int

const (
	watchPending watchOutcome = iota // a live worker still owns it
	watchLanded                      // it has a description now
	watchDone                        // settled, died, or gone: nothing more is coming
)

// waitForDescriptions blocks until every id has been decided or the deadline passes, and reports
// whether any description landed.
//
// The return is the caller's existing "the database changed, re-read and re-render" signal, so
// no call site of FillForRead has to learn about any of this.
func (f *Filler) waitForDescriptions(ids []string, timeout time.Duration) bool {
	if len(ids) == 0 || timeout <= 0 {
		// timeout <= 0 is DO NOT WAIT, not "wait forever". The old inline reading of a
		// non-positive timeout was "no deadline", which was survivable only because the work
		// was inline and finite; against a background worker it would be a `cat` that hangs
		// until generation finishes. The work still happens -- the caller has already claimed
		// and spawned -- it just lands for a later read.
		return false
	}

	waiting := make(map[string]bool, len(ids))
	for _, id := range ids {
		waiting[id] = true
	}
	deadline := time.Now().Add(timeout)
	landed := false

	for len(waiting) > 0 {
		now := time.Now()
		if !now.Before(deadline) {
			return landed
		}

		status, err := helper.ReadDescriptionJobStatus(f.mgr.DbPath(), keysOf(waiting))
		if err != nil {
			// A transient SQLITE_BUSY must not end the wait: the worker is still out there and
			// the next poll is 300ms away. The deadline is what ends this loop, always.
			status = nil
		}
		for id := range waiting {
			switch outcome, desc := classifyWatch(status, id, now); outcome {
			case watchLanded:
				_ = desc
				landed = true
				delete(waiting, id)
			case watchDone:
				delete(waiting, id)
			case watchPending:
			}
		}
		if len(waiting) == 0 {
			break
		}

		sleep := descriptionPollInterval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			break
		}
		time.Sleep(sleep)
	}
	return landed
}

// classifyWatch decides what one poll means for one resource.
//
// Four outcomes, and the difference between the last three is what stops a read waiting out its
// whole deadline for something that is never coming:
//
//   - a description is present: done, and the caller re-renders;
//   - the resource is not in the graph at all: it was deleted while the worker ran;
//   - there is no claim on it: the worker settled it and wrote nothing -- the model declined;
//   - the claim is not live: its worker died, or its lease ran out, and nobody is generating.
func classifyWatch(status map[string]helper.DescriptionJobStatus, id string, now time.Time) (watchOutcome, string) {
	if status == nil {
		return watchPending, "" // the poll itself failed; try again
	}
	st, ok := status[id]
	if !ok {
		return watchDone, ""
	}
	if st.Description != "" {
		return watchLanded, st.Description
	}
	if !st.Claimed {
		return watchDone, ""
	}
	if !st.Job.Live(now) {
		return watchDone, ""
	}
	return watchPending, ""
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
