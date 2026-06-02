package domain

type BugState string

const (
	BugPending      BugState = "pending"
	BugAcknowledged BugState = "acknowledged"
	BugDismissed    BugState = "dismissed"
)

type KnownBug struct {
	ID          string   `json:"id"`
	NodeID      string   `json:"node_id"`
	Description string   `json:"description"`
	State       BugState `json:"state"`
}
