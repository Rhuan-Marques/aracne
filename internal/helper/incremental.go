package helper

import (
	"database/sql"
	"encoding/json"
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
	b.WriteString(res.Description)
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(startsAt))
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(endsAt))
	b.WriteByte('\x1f')
	b.WriteString(locPath)
	b.WriteByte('\x1f')
	b.WriteString(toJSON(res.Properties))
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
			resStmt, err := tx.Prepare("INSERT OR REPLACE INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
					domain.DescriptionForStorage(res.Kind, res.Description), toJSON(res.Properties), startsAt, endsAt, locPath); err != nil {
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
// small info and warnings tables are rewritten wholesale (cheap, and they change
// globally on an incremental update). This replaces a full WriteDb on the
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

		// info: full rewrite (tiny table).
		if _, err := tx.Exec("DELETE FROM info"); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO info VALUES ('root', ?)", topo.Root); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO info VALUES ('language', ?)", topo.Language); err != nil {
			return err
		}
		languagesJSON, err := json.Marshal(topo.Languages)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO info VALUES ('languages', ?)", string(languagesJSON)); err != nil {
			return err
		}
		for pat, msg := range topo.Errors {
			if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)", "error:"+pat, msg); err != nil {
				return err
			}
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
			resStmt, err := tx.Prepare("INSERT OR REPLACE INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
					domain.DescriptionForStorage(res.Kind, res.Description), toJSON(res.Properties), startsAt, endsAt, locPath); err != nil {
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
