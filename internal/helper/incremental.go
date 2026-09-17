package helper

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// resourceSignature produces a canonical fingerprint of everything WriteDb/
// WriteDelta persists for a resource: the row columns (with WriteDb's
// loc-zeroing rule) plus its connections normalized (conn types and targets
// sorted) so connection ordering differences from a FromGeneric/ToGeneric
// round-trip do not register as a change. Two resources with equal signatures
// serialize identically, so an unchanged resource is never re-written.
func resourceSignature(res domain.Resource) string {
	startsAt, endsAt, locPath := 0, 0, ""
	if res.Location.Path != "" {
		startsAt = res.Location.StartsAt
		endsAt = res.Location.EndsAt
		locPath = res.Location.Path
	}

	var b strings.Builder
	b.WriteString(string(res.Kind))
	b.WriteByte('\x1f')
	b.WriteString(res.Name)
	b.WriteByte('\x1f')
	b.WriteString(res.Language)
	b.WriteByte('\x1f')
	// THE STORED FORM, NOT THE PARSED ONE. WriteDb / WriteIncremental / WriteScopedResources
	// all persist domain.DescriptionForStorage(kind, desc), which collapses whitespace and
	// drops anything over its kind's budget. Hashing the raw text meant the signature of a
	// freshly-parsed resource could never equal the signature of what had been stored for
	// it: a two-line doc comment round-tripped as one line, an over-budget one as the empty
	// string. DiffResources then reported every doc-commented resource as changed on every
	// scan, and WriteIncremental re-upserted its row and rewrote all of its connection rows
	// -- write amplification that scaled with the repository instead of with the change.
	b.WriteString(domain.DescriptionForStorage(res.Kind, res.Description))
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(startsAt))
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(endsAt))
	b.WriteByte('\x1f')
	b.WriteString(locPath)
	b.WriteByte('\x1f')
	b.WriteString(toJSON(res.Properties))
	b.WriteByte('\x1f')
	// The body hashes are part of the row, so they belong in the fingerprint that decides
	// whether the row needs rewriting. Leaving them out let an edit that changed only the
	// BODY -- one line replaced in place, same span, same signature -- keep the stale hash
	// that was stored for the previous body, which is the one thing a move matcher must never
	// read.
	b.WriteString(res.ExactHash)
	b.WriteByte('\x1f')
	b.WriteString(res.NormHash)
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(res.NormLines))
	b.WriteByte('\x1f')

	connTypes := make([]string, 0, len(res.Connections))
	for ct := range res.Connections {
		connTypes = append(connTypes, ct)
	}
	sort.Strings(connTypes)
	for _, ct := range connTypes {
		targets := append([]string(nil), res.Connections[ct]...)
		sort.Strings(targets)
		b.WriteString(ct)
		b.WriteByte('=')
		b.WriteString(strings.Join(targets, ","))
		b.WriteByte(';')
	}
	return b.String()
}

// ResourceSignatureOf returns the canonical fingerprint of a single resource
// (see resourceSignature). It lets callers diff one post-update resource against
// a precomputed signature without rebuilding the whole map.
func ResourceSignatureOf(res domain.Resource) string {
	return resourceSignature(res)
}

// ResourceSignatures fingerprints every resource (see resourceSignature). Call
// it on the freshly-read topology BEFORE applying a scoped update, then pass the
// result to DiffResources after the update to compute the changed set.
func ResourceSignatures(resources map[string]domain.Resource) map[string]string {
	sigs := make(map[string]string, len(resources))
	for id, res := range resources {
		sigs[id] = resourceSignature(res)
	}
	return sigs
}

// DiffResources compares post-update resources against pre-update signatures and
// returns the resources to upsert (new or changed) and the IDs to delete (present
// before, gone after). A new resource has no prior signature ("") so it never
// matches and is upserted.
func DiffResources(beforeSigs map[string]string, after map[string]domain.Resource) (upserts []domain.Resource, deletes []string) {
	seen := make(map[string]bool, len(after))
	for id, res := range after {
		seen[id] = true
		if beforeSigs[id] != resourceSignature(res) {
			upserts = append(upserts, res)
		}
	}
	for id := range beforeSigs {
		if !seen[id] {
			deletes = append(deletes, id)
		}
	}
	return upserts, deletes
}

// WriteScopedResources persists a scoped change WITHOUT a full topology in
// memory (the Phase-3 partial path). It rewrites the small warnings table
// wholesale from the provided map and applies row-level resource/connection
// deltas (delete the removed IDs, upsert the changed ones, dropping each
// upserted source's stale outgoing edges). It deliberately leaves the info
// table untouched: the partial path is used only for single-language edits that
// cannot change root/language/languages, so info is already correct from the
// prior full scan. bugs are reconciled separately by CleanupOrphanedBugsScoped.
func WriteScopedResources(dbPath string, upserts []domain.Resource, deletes []string, warnings map[string]domain.TopologyWarning) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if err := createSchema(db); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		// warnings: full rewrite (small table; warnings shift globally per update).
		if _, err := tx.Exec("DELETE FROM warnings"); err != nil {
			return err
		}
		warnStmt, err := tx.Prepare("INSERT INTO warnings VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer warnStmt.Close()
		for id, w := range warnings {
			if w.Transient {
				continue // never stored; see domain.TopologyWarning.Transient
			}
			if _, err := warnStmt.Exec(id, w.SourceID, string(w.Kind), w.TargetID, w.Message, w.Baseline); err != nil {
				return err
			}
		}

		// resources/connections: scoped deletes then upserts.
		for _, chunk := range chunkStrings(deletes, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			ph := placeholders(len(chunk))
			if _, err := tx.Exec("DELETE FROM connections WHERE source_id IN ("+ph+")", args...); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM resources WHERE id IN ("+ph+")", args...); err != nil {
				return err
			}
		}

		if len(upserts) > 0 {
			resStmt, err := tx.Prepare("INSERT OR REPLACE INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path, exact_hash, norm_hash, norm_lines) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			if err != nil {
				return err
			}
			defer resStmt.Close()
			connStmt, err := tx.Prepare("INSERT OR REPLACE INTO connections VALUES (?, ?, ?)")
			if err != nil {
				return err
			}
			defer connStmt.Close()

			for _, res := range upserts {
				startsAt, endsAt := 0, 0
				locPath := ""
				if res.Location.Path != "" {
					startsAt = res.Location.StartsAt
					endsAt = res.Location.EndsAt
					locPath = res.Location.Path
				}
				if _, err := resStmt.Exec(res.ID, string(res.Kind), res.Name, res.Language,
					domain.DescriptionForStorage(res.Kind, res.Description), toJSON(res.Properties), startsAt, endsAt, locPath,
					res.ExactHash, res.NormHash, res.NormLines); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM connections WHERE source_id = ?", res.ID); err != nil {
					return err
				}
				for kind, targets := range res.Connections {
					for _, target := range targets {
						if _, err := connStmt.Exec(res.ID, kind, target); err != nil {
							return err
						}
					}
				}
			}
		}

		return tx.Commit()
	})
}

// WriteIncremental persists a scoped topology update in one transaction. The
// large resources/connections tables are written with row-level scope (upsert
// the changed resources, delete the removed ones — like WriteDelta), while the
// small warnings table is rewritten wholesale (cheap, and warnings shift globally on an
// incremental update) while `info` is upserted key by key. This replaces a full WriteDb on the
// incremental path: cost scales with the change, not the repo. bugs are left to
// CleanupOrphanedBugs, exactly as with WriteDb.
func WriteIncremental(dbPath string, topo *domain.Topology, upserts []domain.Resource, deletes []string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if err := createSchema(db); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		// info: the three graph-wide rows are UPSERTED and the error log is rewritten under
		// its own cap. Deliberately not `DELETE FROM info` -- that took out every key this
		// function does not itself re-insert, and re-added the error rows with no ceiling, so
		// an incremental scan re-grew exactly the bloat maxStoredErrors exists to stop. See
		// writeTopologyInfo / writeScanErrors.
		if err := writeTopologyInfo(tx, topo); err != nil {
			return err
		}
		if err := writeScanErrors(tx, topo.Errors); err != nil {
			return err
		}

		// warnings: full rewrite (small table; warnings shift globally per update).
		if _, err := tx.Exec("DELETE FROM warnings"); err != nil {
			return err
		}
		warnStmt, err := tx.Prepare("INSERT INTO warnings VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer warnStmt.Close()
		for id, w := range topo.Warnings {
			if w.Transient {
				continue // never stored; see domain.TopologyWarning.Transient
			}
			if _, err := warnStmt.Exec(id, w.SourceID, string(w.Kind), w.TargetID, w.Message, w.Baseline); err != nil {
				return err
			}
		}

		// resources/connections: scoped deletes then upserts.
		for _, chunk := range chunkStrings(deletes, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			ph := placeholders(len(chunk))
			if _, err := tx.Exec("DELETE FROM connections WHERE source_id IN ("+ph+")", args...); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM resources WHERE id IN ("+ph+")", args...); err != nil {
				return err
			}
		}

		if len(upserts) > 0 {
			resStmt, err := tx.Prepare("INSERT OR REPLACE INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path, exact_hash, norm_hash, norm_lines) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			if err != nil {
				return err
			}
			defer resStmt.Close()
			connStmt, err := tx.Prepare("INSERT OR REPLACE INTO connections VALUES (?, ?, ?)")
			if err != nil {
				return err
			}
			defer connStmt.Close()

			for _, res := range upserts {
				startsAt, endsAt := 0, 0
				locPath := ""
				if res.Location.Path != "" {
					startsAt = res.Location.StartsAt
					endsAt = res.Location.EndsAt
					locPath = res.Location.Path
				}
				if _, err := resStmt.Exec(res.ID, string(res.Kind), res.Name, res.Language,
					domain.DescriptionForStorage(res.Kind, res.Description), toJSON(res.Properties), startsAt, endsAt, locPath,
					res.ExactHash, res.NormHash, res.NormLines); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM connections WHERE source_id = ?", res.ID); err != nil {
					return err
				}
				for kind, targets := range res.Connections {
					for _, target := range targets {
						if _, err := connStmt.Exec(res.ID, kind, target); err != nil {
							return err
						}
					}
				}
			}
		}

		return tx.Commit()
	})
}
