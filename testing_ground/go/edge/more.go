// This file EXTENDS the edge package with more edge cases: an iota const block
// using EXPRESSIONS (bit-shift byte sizes), a TYPED const block, and a struct
// carrying STRUCT TAGS.
package edge

// Byte-size constants via an IOTA EXPRESSION (1 << (10 * iota)). The blank
// first entry is skipped; KB/MB/GB are successive powers of 1024.
const (
	_  = iota // skip 0
	KB = 1 << (10 * iota)
	MB
	GB
)

// Priority is a defined type used by the TYPED const block below.
type Priority int

// A TYPED const block: each constant carries the explicit Priority type.
const (
	Low    Priority = 1
	Medium Priority = 5
	High   Priority = 10
)

// Tagged carries STRUCT TAGS (json/xml). The parser must not choke on the tag
// strings or turn them into edges.
type Tagged struct {
	Name  string `json:"name" xml:"name"`
	Count int    `json:"count,omitempty"`
}
