// Package concurrency collects Go concurrency-related edge cases: a DEFERRED
// call (defer cleanup()), a GOROUTINE on a named function (go process(j)), a
// goroutine on a function literal, CHANNEL-typed parameters/fields, and a
// SELECT statement. defer and go should both yield ordinary calls edges.
package concurrency

// Job is a unit of work passed over channels.
type Job struct {
	ID int
}

// Pool holds CHANNEL-typed fields.
type Pool struct {
	Queue chan Job
	Done  chan struct{}
}

// process handles a single job; it is the target of a goroutine, an anonymous
// goroutine, and a direct call below.
func process(j Job) int {
	return j.ID * 2
}

// cleanup is invoked via defer.
func cleanup() {
	// nothing to release in the corpus
}

// Work runs jobs: it DEFERS cleanup and launches process in a GOROUTINE for
// each job — defer and go must both produce calls edges (-> cleanup,
// -> process).
func Work(p *Pool) {
	defer cleanup()
	for j := range p.Queue {
		go process(j) // goroutine on a named function
	}
}

// Spawn launches an ANONYMOUS goroutine that calls process — the enclosing
// function should own the calls edge to process.
func Spawn(j Job) {
	go func() {
		_ = process(j)
	}()
}

// Select multiplexes the pool's channels with a SELECT statement.
func Select(p *Pool) int {
	select {
	case j := <-p.Queue:
		return process(j)
	case <-p.Done:
		return -1
	}
}
