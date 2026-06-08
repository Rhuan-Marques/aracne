package helper

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	AgentRouteAccessFullCut     = "full_cut"
	AgentRouteAccessDescription = "description"
	DefaultAgentRouteTTLSeconds = 600
)

type AgentRoutesConfig struct {
	TTLSeconds int `json:"ttl_seconds"`
}

type AgentRouteSummary struct {
	SessionID  string `json:"session_id"`
	Platform   string `json:"platform"`
	Label      string `json:"label,omitempty"`
	StartedAt  int64  `json:"started_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	NodeCount  int    `json:"node_count"`
}

type AgentRouteResource struct {
	ResourceID string `json:"resource_id"`
	AccessKind string `json:"access_kind"`
	LastSeenAt int64  `json:"last_seen_at"`
}

func (c *Config) EffectiveAgentRouteTTL() time.Duration {
	if c == nil || c.AgentRoutes.TTLSeconds <= 0 {
		return time.Duration(DefaultAgentRouteTTLSeconds) * time.Second
	}
	return time.Duration(c.AgentRoutes.TTLSeconds) * time.Second
}

func createAgentRouteSchema(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS agent_routes (
		session_id TEXT PRIMARY KEY,
		platform TEXT NOT NULL,
		label TEXT DEFAULT '',
		started_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_agent_routes_last_seen ON agent_routes(last_seen_at);

	CREATE TABLE IF NOT EXISTS agent_route_resources (
		session_id TEXT NOT NULL,
		resource_id TEXT NOT NULL,
		access_kind TEXT NOT NULL,
		last_seen_at INTEGER NOT NULL,
		PRIMARY KEY(session_id, resource_id)
	);
	CREATE INDEX IF NOT EXISTS idx_agent_route_resources_session ON agent_route_resources(session_id);
	CREATE INDEX IF NOT EXISTS idx_agent_route_resources_resource ON agent_route_resources(resource_id);
	`
	_, err := db.Exec(ddl)
	return err
}

func CleanupStaleAgentRoutes(dbPath string, ttl time.Duration) error {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return err
	}
	return cleanupStaleAgentRoutesDB(db, ttl)
}

func cleanupStaleAgentRoutesDB(db *sql.DB, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Duration(DefaultAgentRouteTTLSeconds) * time.Second
	}
	cutoff := time.Now().Add(-ttl).UnixMilli()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM agent_route_resources WHERE session_id IN (SELECT session_id FROM agent_routes WHERE last_seen_at < ?)", cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM agent_routes WHERE last_seen_at < ?", cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

func RecordAgentRouteAccess(dbPath, sessionID, platform, label, accessKind string, resourceIDs []string, ttl time.Duration) error {
	sessionID = strings.TrimSpace(sessionID)
	platform = strings.TrimSpace(platform)
	label = strings.TrimSpace(label)
	accessKind = normalizeAgentRouteAccess(accessKind)
	if sessionID == "" {
		return fmt.Errorf("missing session id")
	}
	if platform == "" {
		platform = "unknown"
	}
	if accessKind == "" {
		return fmt.Errorf("invalid agent route access kind")
	}
	ids := dedupeNonEmpty(resourceIDs)
	if len(ids) == 0 {
		return fmt.Errorf("missing resource id")
	}

	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return err
	}
	if err := cleanupStaleAgentRoutesDB(db, ttl); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO agent_routes (session_id, platform, label, started_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET platform = excluded.platform, label = excluded.label, last_seen_at = excluded.last_seen_at`, sessionID, platform, label, now, now); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO agent_route_resources (session_id, resource_id, access_kind, last_seen_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(session_id, resource_id) DO UPDATE SET
				access_kind = CASE WHEN agent_route_resources.access_kind = ? THEN agent_route_resources.access_kind ELSE excluded.access_kind END,
				last_seen_at = excluded.last_seen_at`, sessionID, id, accessKind, now, AgentRouteAccessFullCut); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ReadAgentRoutes(dbPath string, ttl time.Duration) ([]AgentRouteSummary, error) {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return nil, err
	}
	if err := cleanupStaleAgentRoutesDB(db, ttl); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT r.session_id, r.platform, r.label, r.started_at, r.last_seen_at, COUNT(rr.resource_id)
		FROM agent_routes r
		LEFT JOIN agent_route_resources rr ON rr.session_id = r.session_id
		GROUP BY r.session_id, r.platform, r.label, r.started_at, r.last_seen_at
		ORDER BY r.last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]AgentRouteSummary, 0)
	for rows.Next() {
		var route AgentRouteSummary
		if err := rows.Scan(&route.SessionID, &route.Platform, &route.Label, &route.StartedAt, &route.LastSeenAt, &route.NodeCount); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func ReadAgentRouteResources(dbPath, sessionID string, ttl time.Duration) (map[string]AgentRouteResource, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("missing session id")
	}
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return nil, err
	}
	if err := cleanupStaleAgentRoutesDB(db, ttl); err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT resource_id, access_kind, last_seen_at FROM agent_route_resources WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resources := make(map[string]AgentRouteResource)
	for rows.Next() {
		var res AgentRouteResource
		if err := rows.Scan(&res.ResourceID, &res.AccessKind, &res.LastSeenAt); err != nil {
			return nil, err
		}
		resources[res.ResourceID] = res
	}
	return resources, rows.Err()
}

func AgentRoutesVersion(dbPath string, ttl time.Duration) (string, error) {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return "", err
	}
	if err := cleanupStaleAgentRoutesDB(db, ttl); err != nil {
		return "", err
	}
	var routeCount, resourceCount int
	var maxRouteSeen, maxResourceSeen sql.NullInt64
	if err := db.QueryRow("SELECT COUNT(*), MAX(last_seen_at) FROM agent_routes").Scan(&routeCount, &maxRouteSeen); err != nil {
		return "", err
	}
	if err := db.QueryRow("SELECT COUNT(*), MAX(last_seen_at) FROM agent_route_resources").Scan(&resourceCount, &maxResourceSeen); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d:%d", routeCount, nullInt64(maxRouteSeen), resourceCount, nullInt64(maxResourceSeen)), nil
}

func normalizeAgentRouteAccess(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case AgentRouteAccessFullCut, "fullcut", "full-cut":
		return AgentRouteAccessFullCut
	case AgentRouteAccessDescription, "desc":
		return AgentRouteAccessDescription
	default:
		return ""
	}
}

func dedupeNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func nullInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}
