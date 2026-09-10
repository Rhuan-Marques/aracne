package helper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Descriptions are the expensive part of a topology: they are LLM-authored, they cost
// real time and tokens to produce, and nothing regenerates them for free. Yet every path
// that preserves them across a re-scan is keyed on the resource ID alone
// (TopologyManager.FullReScan and each scanner's preserveDescriptions), so ANY change to
// an ID format silently drops every description attached to the old ID, and
// `arac scan --hard` drops all of them unconditionally.
//
// This file is the ID-independent backup: it records each description beside a stable
// IDENTITY (repo-relative path, kind, name, enclosing type, and a hash of the resource's
// own source text) so it can be re-attached after the IDs underneath it change. It is
// also useful on its own — for moving descriptions between worktrees or branches, and
// for surviving a hard rebuild.

// DescriptionRecord is one exported description plus everything needed to find its
// resource again after IDs change. Serialized one-per-line as JSONL.
type DescriptionRecord struct {
	// ID is the resource ID at export time. It is the fastest match but the ONLY field
	// here that a scheme change invalidates, which is why the others exist.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Parent is the enclosing type's NAME (not its ID) for methods, so it survives an ID
	// change. Empty for free functions and top-level declarations.
	Parent string `json:"parent,omitempty"`
	// RelPath is relative to the topology root with forward slashes. `loc_path` in the DB
	// is absolute and therefore embeds the machine that scanned it, which makes it
	// useless as a portable key.
	RelPath  string `json:"rel_path"`
	StartsAt int    `json:"starts_at,omitempty"`
	EndsAt   int    `json:"ends_at,omitempty"`
	// SrcSHA256 hashes the resource's own source span, so a resource that MOVED to a
	// different file is still recognisable.
	//
	// READ FROM THE RESOURCE, NOT FROM THE FILE. This used to be computed here by opening
	// RelPath and hashing the span, which cannot work for the case it exists to serve: after a
	// real `git mv` the old path is gone, the read fails, the hash comes back empty and both
	// source-hash tiers below go dark exactly when they are needed. (The test that claimed to
	// cover it, TestSurvivesAFileMoveViaSourceHash, passed only because its fixture COPIED the
	// file and left the original in place.) The scan path now fingerprints every resource
	// while its file is still there and stores the result, so this reads what was recorded.
	SrcSHA256 string `json:"src_sha256,omitempty"`
	// NormSHA256 is the same span with comments and blank lines removed, so a resource that
	// moved AND was reformatted or recommented on the way still matches. See helper.BodyHashes.
	NormSHA256 string `json:"norm_sha256,omitempty"`
	// NormLines is the normalized body's line count, which decides whether the weaker match
	// tiers may trust the hashes above. See helper.WeakTierMinLines.
	NormLines   int    `json:"norm_lines,omitempty"`
	Description string `json:"description"`
}

// DescriptionsSidecarName is the conventional filename inside `.aracne/`.
const DescriptionsSidecarName = "descriptions.jsonl"

// --------------------------------------------------------------------------- //
// Export
// --------------------------------------------------------------------------- //

// BuildDescriptionRecords turns every described resource in `topo` into a record.
// `root` is the topology root, used to make paths relative. Source hashing is
// best-effort: an unreadable or moved file yields an empty SrcSHA256 rather than an error,
// because a missing hash only weakens one match tier and must never fail an export.
func BuildDescriptionRecords(topo *domain.Topology, root string) []DescriptionRecord {
	if topo == nil {
		return nil
	}
	cache := newLineCache()
	out := make([]DescriptionRecord, 0, len(topo.Resources))
	for id, res := range topo.Resources {
		if strings.TrimSpace(res.Description) == "" {
			continue
		}
		exact, norm, normLines := storedOrComputedHashes(cache, res)
		out = append(out, DescriptionRecord{
			ID:          id,
			Kind:        string(res.Kind),
			Name:        res.Name,
			Parent:      parentName(topo, res),
			RelPath:     relPath(root, resourcePath(res)),
			StartsAt:    res.Location.StartsAt,
			EndsAt:      res.Location.EndsAt,
			SrcSHA256:   exact,
			NormSHA256:  norm,
			NormLines:   normLines,
			Description: res.Description,
		})
	}
	// Deterministic order so an export is diffable and two runs produce identical bytes.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// WriteDescriptionRecords writes records as JSONL to `path`, creating parent dirs.
func WriteDescriptionRecords(path string, recs []DescriptionRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

// ReadDescriptionRecords loads a JSONL sidecar. Blank lines are skipped; a malformed line
// is reported with its line number rather than silently dropped, because a partial restore
// that looks complete is worse than a loud failure.
func ReadDescriptionRecords(path string) ([]DescriptionRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []DescriptionRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // descriptions are short; files are not
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r DescriptionRecord
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// --------------------------------------------------------------------------- //
// Import / matching
// --------------------------------------------------------------------------- //

// Match tiers, most to least trustworthy. Reported per-import so a migration can be
// judged rather than merely completed.
const (
	MatchExactID  = "exact_id"      // the ID is unchanged
	MatchIdentity = "path_identity" // same file, kind, name and enclosing type
	MatchMovedSrc = "moved_source"  // same kind, name, parent and source hash — file moved
	MatchNameSrc  = "name_source"   // same kind, name and source hash — last resort
)

// MatchTiers is the order tiers are attempted in, and the order they are reported.
var MatchTiers = []string{MatchExactID, MatchIdentity, MatchMovedSrc, MatchNameSrc}

// DescriptionMatch is one resolved record: which resource it landed on and how.
type DescriptionMatch struct {
	Record     DescriptionRecord
	ResourceID string
	Tier       string
}

// DescriptionImportResult reports what an import would do (or did).
type DescriptionImportResult struct {
	Matched   []DescriptionMatch
	Unmatched []DescriptionRecord
	// Skipped records matched a resource that already had a description; the existing
	// text wins so a re-import never clobbers newer work.
	Skipped   []DescriptionMatch
	ByTier    map[string]int
	Ambiguous []DescriptionRecord // matched more than one resource at every tier
}

// Total returns how many records were considered.
func (r DescriptionImportResult) Total() int {
	return len(r.Matched) + len(r.Skipped) + len(r.Unmatched)
}

// MatchRate is the share of records that found a resource (matched or already-described).
func (r DescriptionImportResult) MatchRate() float64 {
	total := r.Total()
	if total == 0 {
		return 1
	}
	return float64(len(r.Matched)+len(r.Skipped)) / float64(total)
}

// index groups a topology's resources by each match key, so every tier is a map lookup
// rather than a scan of the whole topology per record.
type descIndex struct {
	byID       map[string]domain.Resource
	byIdentity map[string][]string
	byMovedSrc map[string][]string
	byNameSrc  map[string][]string
}

// dedupeHashes returns the distinct non-empty fingerprints among its arguments, preserving
// order so the exact hash is always tried first.
func dedupeHashes(hashes ...string) []string {
	out := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if h == "" {
			continue
		}
		seen := false
		for _, kept := range out {
			if kept == h {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, h)
		}
	}
	return out
}

// storedOrComputedHashes prefers the fingerprints the scan path stamped onto the resource,
// and computes them only when they are absent -- a database written before the columns
// existed, or a topology assembled in memory that never went through StampBodyHashes.
//
// The fallback goes through the same BodyHashes the stamp uses, size floor included, so a
// computed hash and a stored one are interchangeable. Hashing the raw span here instead would
// produce a value that never equals a stored one for any small body.
func storedOrComputedHashes(cache *lineCache, res domain.Resource) (exact, norm string, normLines int) {
	if res.ExactHash != "" || res.NormHash != "" {
		return res.ExactHash, res.NormHash, res.NormLines
	}
	return cache.bodyHashes(res)
}

func identityKey(kind, relPath, name, parent string) string {
	return kind + "\x00" + relPath + "\x00" + name + "\x00" + parent
}

func movedSrcKey(kind, name, parent, sha string) string {
	return kind + "\x00" + name + "\x00" + parent + "\x00" + sha
}

func nameSrcKey(kind, name, sha string) string {
	return kind + "\x00" + name + "\x00" + sha
}

func buildDescIndex(topo *domain.Topology, root string) *descIndex {
	idx := &descIndex{
		byID:       make(map[string]domain.Resource, len(topo.Resources)),
		byIdentity: make(map[string][]string, len(topo.Resources)),
		byMovedSrc: make(map[string][]string, len(topo.Resources)),
		byNameSrc:  make(map[string][]string, len(topo.Resources)),
	}
	cache := newLineCache()
	for id, res := range topo.Resources {
		idx.byID[id] = res
		kind, name := string(res.Kind), res.Name
		parent := parentName(topo, res)
		rel := relPath(root, resourcePath(res))
		exact, norm, normLines := storedOrComputedHashes(cache, res)
		idx.byIdentity[identityKey(kind, rel, name, parent)] =
			append(idx.byIdentity[identityKey(kind, rel, name, parent)], id)
		// Indexed under BOTH fingerprints. A record carrying either one can then find this
		// resource: the exact hash answers a verbatim move without depending on the
		// normalizer, and the normalized hash answers a move that picked up a reformat or a
		// rewritten comment on the way.
		//
		// DEDUPLICATED, because a body with no comments in it normalizes to itself and the two
		// hashes are then the same string. Indexing it twice put the same id in one bucket
		// twice, and `unique` reads a two-entry bucket as an ambiguous match and refuses it --
		// so the commonest case of all, a verbatim move of uncommented code, was the one that
		// stopped working.
		for _, sha := range dedupeHashes(exact, norm) {
			idx.byMovedSrc[movedSrcKey(kind, name, parent, sha)] =
				append(idx.byMovedSrc[movedSrcKey(kind, name, parent, sha)], id)
			// name_source has dropped the enclosing type, so a short body shared by ten
			// implementations of one method would collapse them all into this bucket. Only
			// substantial bodies are offered to it -- moved_source above keeps the parent and
			// needs no such guard.
			if normLines >= WeakTierMinLines {
				idx.byNameSrc[nameSrcKey(kind, name, sha)] =
					append(idx.byNameSrc[nameSrcKey(kind, name, sha)], id)
			}
		}
	}
	return idx
}

// unique returns the single id in `ids`, or "" when the bucket is empty or ambiguous.
// An ambiguous bucket is deliberately NOT guessed at: attaching a description to the
// wrong resource is worse than leaving it unattached and reporting it.
func unique(ids []string) string {
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// MatchDescriptions resolves each record against `topo` without mutating anything.
//
// Tiers are tried in order and the first unambiguous hit wins. A record whose target
// already carries a description is reported as Skipped, never overwritten.
func MatchDescriptions(recs []DescriptionRecord, topo *domain.Topology, root string) DescriptionImportResult {
	res := DescriptionImportResult{ByTier: map[string]int{}}
	if topo == nil {
		res.Unmatched = append(res.Unmatched, recs...)
		return res
	}
	idx := buildDescIndex(topo, root)

	for _, rec := range recs {
		id, tier := "", ""
		if _, ok := idx.byID[rec.ID]; ok {
			id, tier = rec.ID, MatchExactID
		}
		if id == "" {
			if hit := unique(idx.byIdentity[identityKey(rec.Kind, rec.RelPath, rec.Name, rec.Parent)]); hit != "" {
				id, tier = hit, MatchIdentity
			}
		}
		// Exact before normalized, at each tier: a verbatim move is the commonest kind and
		// should never depend on the normalizer having classified the language correctly.
		recHashes := dedupeHashes(rec.SrcSHA256, rec.NormSHA256)
		for _, sha := range recHashes {
			if id != "" {
				break
			}
			if hit := unique(idx.byMovedSrc[movedSrcKey(rec.Kind, rec.Name, rec.Parent, sha)]); hit != "" {
				id, tier = hit, MatchMovedSrc
			}
		}
		for _, sha := range recHashes {
			if id != "" || rec.NormLines < WeakTierMinLines {
				break
			}
			if hit := unique(idx.byNameSrc[nameSrcKey(rec.Kind, rec.Name, sha)]); hit != "" {
				id, tier = hit, MatchNameSrc
			}
		}
		if id == "" {
			// Distinguish "no candidate" from "several candidates": the second is a
			// resolvable problem (tighten the key), the first usually means the resource
			// genuinely no longer exists.
			if len(idx.byIdentity[identityKey(rec.Kind, rec.RelPath, rec.Name, rec.Parent)]) > 1 {
				res.Ambiguous = append(res.Ambiguous, rec)
			}
			res.Unmatched = append(res.Unmatched, rec)
			continue
		}
		m := DescriptionMatch{Record: rec, ResourceID: id, Tier: tier}
		if strings.TrimSpace(idx.byID[id].Description) != "" {
			res.Skipped = append(res.Skipped, m)
			continue
		}
		res.Matched = append(res.Matched, m)
		res.ByTier[tier]++
	}
	return res
}

// --------------------------------------------------------------------------- //
// helpers
// --------------------------------------------------------------------------- //

// parentName resolves a method's enclosing type to its NAME. `method_from` holds the
// parent's ID, which a scheme change invalidates, so the name is stored instead. Falls
// back to the ID's trailing segment when the parent resource is absent from the topology.
func parentName(topo *domain.Topology, res domain.Resource) string {
	raw, ok := res.Properties["method_from"]
	if !ok || raw == nil {
		return enclosingSegment(res.ID, res.Name)
	}
	id, ok := raw.(string)
	if !ok || id == "" {
		return enclosingSegment(res.ID, res.Name)
	}
	if parent, ok := topo.Resources[id]; ok && parent.Name != "" {
		return parent.Name
	}
	return lastSegment(id)
}

// enclosingSegment is the identifier immediately before a resource's own name in its ID.
//
// `method_from` is only set for METHOD-kind resources. TypeScript records a member of an
// interface-shaped object as a plain `function`, so two members named `install` under
// different parents in one file were indistinguishable by (kind, path, name, parent) and
// had to be refused as ambiguous — that cost exactly two descriptions per vue/svelte
// fixture. The parent is right there in the ID, so read it from there when the property is
// missing. Token-based (not string-based) so it is stable under a scheme change that
// strips a leading prefix: "proj/a.B.install" and "a.B.install" both yield "B".
func enclosingSegment(id, name string) string {
	toks := idTokens(id)
	if len(toks) < 2 || toks[len(toks)-1] != name {
		return ""
	}
	return toks[len(toks)-2]
}

// idTokens splits a resource ID into identifier tokens on every separator the languages
// use, dropping a trailing parameter signature. Mirrors internal/topology/idresolve.
func idTokens(id string) []string {
	if i := strings.LastIndex(id, "("); i > 0 && strings.HasSuffix(id, ")") {
		id = id[:i]
	}
	id = strings.ReplaceAll(id, "::", "/")
	fields := strings.FieldsFunc(id, func(r rune) bool {
		return r == '/' || r == '.' || r == '#' || r == '$' || r == '\\'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.Trim(f, "()'\""); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// lastSegment takes the trailing identifier of an ID under any of the separators the
// languages use (Go/Python/JS `.`, Rust `::`, paths `/`).
func lastSegment(id string) string {
	if i := strings.LastIndex(id, "::"); i >= 0 {
		id = id[i+2:]
	}
	if i := strings.LastIndexAny(id, "./"); i >= 0 {
		id = id[i+1:]
	}
	return strings.Trim(id, "()")
}

// resourcePath is where a resource's source lives.
//
// `file` resources store an EMPTY loc_path — their ID *is* the absolute path (true in all
// five languages). Reading Location.Path blindly gave every file the identity
// (file, "", basename, ""), so `client.go` in ten packages collapsed to one key and every
// one of them was rejected as ambiguous.
func resourcePath(res domain.Resource) string {
	if res.Location.Path != "" {
		return res.Location.Path
	}
	if res.Kind == domain.ResourceFile {
		return res.ID
	}
	return ""
}

// relPath makes `path` relative to `root` with forward slashes. Returns the cleaned input
// when it is already relative or lies outside the root.
func relPath(root, path string) string {
	if path == "" {
		return ""
	}
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && domain.RelInside(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

// lineCache reads each source file at most once per export/import pass. A large repo has
// thousands of resources spread over hundreds of files; re-reading per resource turns a
// linear pass into a quadratic one.
type lineCache struct {
	files map[string][]string
}

func newLineCache() *lineCache { return &lineCache{files: map[string][]string{}} }

func (c *lineCache) lines(path string) []string {
	if lines, ok := c.files[path]; ok {
		return lines
	}
	c.files[path] = readLines(path)
	return c.files[path]
}

// readLines returns a file's lines, or nil when it cannot be read or is not text.
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() || info.Size() > 32*1024*1024 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if sc.Err() != nil {
		return nil
	}
	return lines
}
