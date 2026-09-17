package lazydesc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// Launching the detached worker.
//
// THE SHAPE. `arac descriptions worker --job <id> --db <path>`, started and then let go of. The
// parent is almost always about to exit -- `arac read`, `arac grep` and the `arac cmd` an
// intercepted `cat` was rewritten into are all one-shot processes -- so the worker has to
// survive its parent by design rather than by luck. The ids it works on are not in argv; it
// reads them from the claim rows the caller has already taken in its name.
//
// WHAT ACTUALLY MAKES IT SURVIVE, in the order it matters:
//
//  1. Its stdio is /dev/null. This is not tidiness. The usual parent is a hook, or a Bash tool
//     call whose stdout the harness reads to EOF; a child holding that pipe open keeps the
//     harness waiting for the worker's entire life. The worker would hang the very read it
//     exists to free. It must be deaf and mute by construction.
//  2. Nothing ever calls Wait() expecting a result, but something does call it. See reap.
//  3. A new session or process group (see spawn_unix.go / spawn_windows.go), so a ctrl-c in the
//     terminal -- which signals the foreground process GROUP -- does not take it down with the
//     shell that started the read.
//  4. Its working directory is set explicitly, never inherited. A detached process holding a
//     deleted cwd is a classic leak, and the CLI provider command runs with this cwd.
const descriptionWorkerVerb = "descriptions"

// descriptionWorkerEnv marks this process, and every process it starts, as description
// machinery. New refuses to build a Filler when it is set.
//
// It has to be INHERITED to do its job, which is why it is an environment variable and not a
// flag. The CLI provider is usually `claude -p`, and a project with aracne hooks installed turns
// that into: worker -> claude -> hook -> arac cmd -> lazy fill -> another worker -> claude ->
// ... An unbounded chain of paid API calls, where each link is behaving perfectly reasonably.
// An environment variable is the one thing that reaches the whole subtree without any of them
// having to know the others exist.
const descriptionWorkerEnv = "ARACNE_LAZYDESC_WORKER"

// WorkerProcess reports whether this process is description machinery -- a worker, or anything a
// worker started.
func WorkerProcess() bool { return strings.TrimSpace(os.Getenv(descriptionWorkerEnv)) != "" }

// errSpawnDisabled is the circuit breaker tripping: this process has failed to launch a worker
// enough times that it has stopped trying.
var errSpawnDisabled = errors.New("worker spawning disabled after repeated failures")

// spawnWorker launches the worker for a job. A variable so tests can install a fake launch;
// production code never assigns it.
var spawnWorker = spawnDescriptionWorker

// spawnFailures counts consecutive launch failures in THIS process.
//
// The failure it guards against is the quiet one: `arac` not on PATH, a binary that cannot be
// resolved, a system out of processes. Without a brake, every read would pay a fork and an exec
// to rediscover the same thing, forever. Two strikes is enough to conclude the environment
// cannot launch workers; the claims are released each time, so nothing is held.
var spawnFailures struct {
	sync.Mutex
	n int
}

const maxConsecutiveSpawnFailures = 2

// NewJobID returns the id a claim and its worker share.
func NewJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A collision here would let two workers believe they own one job. The clock is a poor
		// source of uniqueness but a fine fallback, since the claim CAS is what actually
		// guarantees exclusivity -- the id only has to be unlikely to repeat.
		return hex.EncodeToString([]byte(filepath.Base(os.TempDir())))
	}
	return hex.EncodeToString(b[:])
}

// spawnDescriptionWorker starts the worker process for a job.
func spawnDescriptionWorker(dbPath, jobID, harness string) error {
	spawnFailures.Lock()
	tooManyFailures := spawnFailures.n >= maxConsecutiveSpawnFailures
	spawnFailures.Unlock()
	if tooManyFailures {
		return errSpawnDisabled
	}

	bin := helper.AracBinary()
	// The database path is made absolute HERE, not left to the worker to resolve. The caller is
	// often a hook or an intercepted command standing in a directory nobody chose, and a worker
	// that resolved a relative path against its own cwd would describe whatever database it
	// happened to find -- or none.
	if harness == "" {
		harness = helper.DefaultLazyHarness
	}
	cmd := exec.Command(bin, descriptionWorkerVerb, "worker",
		"--job", jobID, "--db", helper.CanonicalPath(dbPath), "--harness", harness)

	// nil, not os.Stdin/os.Stdout: exec opens os.DevNull for each. See the file comment -- an
	// inherited stdout is how this feature would hang a harness.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Dir = workerDir(dbPath)
	// The environment is inherited, API keys included -- a worker needs exactly what the inline
	// fill needed. The marker is what stops the worker, and anything the worker starts, from
	// triggering a fill of its own: worker -> claude -p -> hook -> arac cmd -> fill -> worker.
	cmd.Env = append(os.Environ(), descriptionWorkerEnv+"=1")
	detach(cmd)

	if err := cmd.Start(); err != nil {
		spawnFailures.Lock()
		spawnFailures.n++
		spawnFailures.Unlock()
		return err
	}
	spawnFailures.Lock()
	spawnFailures.n = 0
	spawnFailures.Unlock()

	reap(cmd)
	return nil
}

// reap waits on the child, and throws the answer away.
//
// Deliberately Wait() rather than Process.Release(), which looks like the more honest way to say
// "I am not interested". The difference only shows up in the two LONG-LIVED parents: `arac serve`
// and `arac viz serve` stay up for the whole session, and a released child becomes a zombie that
// nothing reaps -- one per read, accumulating for as long as the server runs. In a one-shot
// parent this goroutine simply dies with the process moments later and init reaps the child.
// One mechanism, correct in both, and bounded by the worker cap.
func reap(cmd *exec.Cmd) {
	go func() { _ = cmd.Wait() }() // lazydesc:untracked -- ends with the child, bounded by max_workers
}

// workerDir is the directory the worker runs in: the project root, never the caller's cwd.
func workerDir(dbPath string) string {
	root := filepath.Dir(filepath.Dir(helper.CanonicalPath(dbPath))) // <root>/.aracne/topology.db
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return os.TempDir()
}
