package domain

// Represents a source code location with a file path and Start/End line numbers, used to identify and extract code regions for the topology.
type Location struct {
	StartsAt int
	EndsAt   int
	Path     string
}
