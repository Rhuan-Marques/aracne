package domain

// Pairs a source code cut (string) with its Location for precise resource extraction from source files.
type CodeEntry struct {
	Location Location
	Cut      string
}
