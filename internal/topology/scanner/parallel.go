package scanner

import (
	"fmt"
	"os"
	"runtime"
	"sync"
)

// workers is the maximum number of goroutines ParallelParse may run at once.
// 0 (the default) means auto: runtime.NumCPU(). Guarded by workersMu, following
// the same package-global idiom as domain.SetActivePathVisibility so the count
// can be installed once per scan without threading it through every Scan call.
var (
	workersMu sync.Mutex
	workers   int
)

// SetWorkers sets the maximum number of files parsed concurrently by
// ParallelParse. A value <= 0 means auto (runtime.NumCPU()). Safe for
// concurrent use; install it once before a scan.
func SetWorkers(n int) {
	workersMu.Lock()
	workers = n
	workersMu.Unlock()
}

// Workers returns the configured worker count, falling back to
// runtime.NumCPU() when unset (<= 0). It never returns less than 1.
func Workers() int {
	workersMu.Lock()
	n := workers
	workersMu.Unlock()
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if n < 1 {
		n = 1
	}
	return n
}

// ParallelParse runs parse(path) for every path using up to Workers()
// goroutines, writing each result into results[i] so the returned slice is
// index-aligned with paths (INPUT ORDER preserved). Callers then register the
// results sequentially, keeping scan output identical to the old single-threaded
// loop while the CPU/IO-bound parse runs concurrently. Bounding the goroutine
// count also bounds how many files' ASTs/parse-trees are live at once, which is
// what keeps peak RAM in check on large projects.
//
// label names the phase for the progress bar (e.g. "parsing go"); it advances as
// each path completes when progress reporting is enabled.
func ParallelParse[R any](paths []string, label string, parse func(path string) R) []R {
	results := make([]R, len(paths))
	if len(paths) == 0 {
		return results
	}

	progressStartPhase(label, len(paths))
	defer progressEndPhase()

	n := Workers()
	if n > len(paths) {
		n = len(paths)
	}

	// Workers pull indices off idx and write their own results[i] slot — disjoint
	// writes, so no lock is needed on results. Feeding indices (rather than the
	// paths themselves) keeps the result slice index-aligned with the input.
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				results[i] = safeParse(parse, paths[i])
				progressStep()
			}
		}()
	}
	for i := range paths {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return results
}

// safeParse runs one file's parse and turns a panic into the zero result, which every caller
// already handles as "this file did not parse".
//
// WHY. A tree-sitter binding or an index calculation that panics on one malformed file used to
// take `arac scan` down with it -- the whole scan, for one file. Every other place a scan runs
// on a critical path recovers explicitly and says why (runGuardScan, proxyRead, trackedFiles);
// the scan itself was the one that did not, and it is the one with the most files to be wrong
// about. The panic is reported on stderr so it is not silent.
func safeParse[R any](parse func(path string) R, path string) (out R) {
	defer func() {
		if r := recover(); r != nil {
			var zero R
			out = zero
			fmt.Fprintf(os.Stderr, "aracne: parsing %s panicked (%v); skipping this file\n", path, r)
		}
	}()
	return parse(path)
}
