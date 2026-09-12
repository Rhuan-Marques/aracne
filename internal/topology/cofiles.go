package topology

import (
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// coOwnerFiles returns the indexed files, outside skip, that must be re-resolved because a
// deleted file shared its declarations with them: the files that declare a resource the
// deleted file also declares, and the files that pointed at the deleted file itself.
//
// An ID can have two owners where IDs do not name the file: a Java FQN in src/main/java and
// src/main/java11, or in two modules of one build. The graph holds one copy, located in one of
// the files, and removing a file removes every resource located in it (see
// helper.RemoveFileResources) -- so deleting the file the copy happened to sit in took the type
// with it, although the other file still declares it and a full scan keeps it. Re-resolving the
// surviving owners before the removal moves the copy into them, and the removal then spares it.
// An import of the deleted file then names the survivor instead, so its importers go too.
func coOwnerFiles(topo *domain.Topology, deleted []string, skip map[string]bool) map[string]bool {
	ownerOf := make(map[string]string) // resource -> the deleted file declaring it
	gone := make(map[string]bool, len(deleted))
	for _, path := range deleted {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		gone[abs] = true
		for connType, targets := range topo.Resources[abs].Connections {
			if strings.HasPrefix(connType, "has_") {
				for _, target := range targets {
					ownerOf[target] = abs
				}
			}
		}
	}
	if len(ownerOf) == 0 {
		return nil
	}
	out := make(map[string]bool)
	shared := make(map[string]bool) // deleted files with a surviving co-owner
	for id, res := range topo.Resources {
		if res.Kind != domain.ResourceFile || gone[id] {
			continue
		}
		for connType, targets := range res.Connections {
			if !strings.HasPrefix(connType, "has_") {
				continue
			}
			for _, target := range targets {
				if d, ok := ownerOf[target]; ok {
					shared[d] = true
					out[id] = true
				}
			}
		}
	}
	if len(shared) > 0 {
		for _, res := range topo.Resources {
			src := fileOf(res)
			if src == "" || gone[src] {
				continue
			}
			for _, targets := range res.Connections {
				for _, target := range targets {
					if shared[target] {
						out[src] = true
					}
				}
			}
		}
	}
	for f := range skip {
		delete(out, f)
	}
	return out
}

// inheritingFiles maps each file to the files holding a type that directly extends or
// implements one of its types.
//
// A subclass offers the members it inherits as its own, so for dependentFiles it passes on a
// change to its superclass's file the way a barrel passes on what it re-exports: a method added
// to a grandparent binds an unqualified call two classes down, in a file that has no edge to
// the grandparent's file at all.
func inheritingFiles(before map[string]domain.Resource) map[string][]string {
	out := make(map[string][]string)
	for _, res := range before {
		if res.Kind == domain.ResourceFile {
			continue
		}
		sub := res.Location.Path
		for _, connType := range []string{"inherits", "implements"} {
			for _, parent := range res.Connections[connType] {
				p, ok := before[parent]
				if !ok {
					continue
				}
				if super := fileOf(p); sub != "" && super != "" && super != sub {
					out[super] = append(out[super], sub)
				}
			}
		}
	}
	return out
}
