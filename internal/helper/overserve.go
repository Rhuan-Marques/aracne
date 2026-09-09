package helper

// The over-serve ceiling: what an enriched answer may cost against the plain one it stands in
// for. Every aracne surface that answers in something else's place measures itself here -- an
// intercepted shell command, a denied read the guard proxies, and the read and search tools,
// which stand in for the harness's own.
//
// terminal.max_overserve is the multiple. The two bounds below are what make a multiple usable
// at the extremes: a bare ratio refuses the two-line window whose enclosing function was the
// whole point, and waves through a megabyte because the raw answer was large too.
const (
	// OverserveReadFree is what a READ is always allowed, however narrow the request. A
	// twenty-line window legitimately expands to the function around it plus a context
	// block, and refusing that would switch enrichment off for the case it exists to serve.
	OverserveReadFree = 12 * 1024
	// OverserveSearchFree is the same allowance for a SEARCH, where the additions are
	// per-row rather than one expansion: enough for a handful of resource headers, and no
	// story that justifies more. A search that answers one matching line with thirty
	// declaration lines has over-served, and a read-sized floor would call it free.
	OverserveSearchFree = 2 * 1024
	// OverserveMaxBytes is the ceiling whatever the ratio says. It was 100KB, which in
	// practice caught nothing: the worst over-serve measured in
	// compact-blocked-after-bs-20260830c was 59,177 bytes and sailed under it.
	OverserveMaxBytes = 32 * 1024
)

// OverserveBudget is the largest answer worth serving in place of one that would have cost
// rawBytes, with freeBytes always allowed whatever the ratio.
//
// A negative return means the project switched the ceiling off, and nothing is to be measured
// against it. An absent config reads as the DEFAULT ceiling rather than as no ceiling: a
// project with no config file gets the product's behaviour, not its most expensive corner.
func (c *Config) OverserveBudget(rawBytes, freeBytes int) int {
	factor := DefaultTerminalMaxOverserve
	if c != nil {
		factor = c.EffectiveTerminalMaxOverserve()
	}
	if factor <= 0 {
		return -1
	}
	budget := rawBytes * factor
	if budget < freeBytes {
		budget = freeBytes
	}
	if budget > OverserveMaxBytes {
		budget = OverserveMaxBytes
	}
	return budget
}

// WithinOverserve reports whether an answer is worth serving in place of one that would have
// cost rawBytes. Each caller decides what to do when it is not, because each has a different
// thing to fall back to: the real command, the plain denial, or the same answer without the
// topology's additions.
func (c *Config) WithinOverserve(answer string, rawBytes, freeBytes int) bool {
	budget := c.OverserveBudget(rawBytes, freeBytes)
	return budget < 0 || len(answer) <= budget
}
