package domain

type BugState string

const (
	BugPending      BugState = "pending"
	BugAcknowledged BugState = "acknowledged"
	BugDismissed    BugState = "dismissed"
)

// Represents a known bug with ID, node reference, description, and state.
type KnownBug struct {
	ID          string   `json:"id"`
	NodeID      string   `json:"node_id"`
	Description string   `json:"description"`
	State       BugState `json:"state"`
}
