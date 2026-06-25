// Package recursive collects self-referential and mutually-recursive Go
// constructs: a self-referential struct (a linked-list Node pointing at
// itself), a pair of mutually-referential structs (Folder/File), a method that
// recurses on itself, and two package-level functions that recurse into each
// other.
package recursive

// Node is a SELF-REFERENTIAL struct: its Next field points back at *Node, so
// the topology must record a uses_struct edge from Node to itself.
type Node struct {
	Value int
	Next  *Node
}

// Length walks the list by RECURSING on itself — a self-call edge from
// (Node).Length to (Node).Length.
func (n *Node) Length() int {
	if n == nil || n.Next == nil {
		return 1
	}
	return 1 + n.Next.Length()
}

// Folder and File are MUTUALLY-REFERENTIAL structs: Folder holds []File and
// []*Folder, File points back at its *Folder owner — a uses_struct cycle.
type Folder struct {
	Name  string
	Files []File
	Sub   []*Folder
}

// File points back at the Folder that owns it (the other half of the cycle).
type File struct {
	Name  string
	Owner *Folder
}

// Count totals files here and in every sub-folder by recursing on itself.
func (f *Folder) Count() int {
	total := len(f.Files)
	for _, sub := range f.Sub {
		total += sub.Count()
	}
	return total
}

// ping and pong are MUTUALLY-RECURSIVE functions: ping calls pong, pong calls
// ping — the topology must record a calls edge in both directions.
func ping(n int) int {
	if n <= 0 {
		return 0
	}
	return pong(n - 1)
}

func pong(n int) int {
	if n <= 0 {
		return 0
	}
	return ping(n - 1)
}

// Bounce kicks off the mutual recursion so ping/pong are reachable.
func Bounce(n int) int {
	return ping(n)
}
