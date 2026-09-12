package topology

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// repeatedDeclarationSiblings returns the surviving Go files that must be re-registered because
// a file this batch deletes declared a function id they declare too.
//
// goscanner gives a function id declared more than once in a package -- `init`, `_`, a
// build-tag variant -- to the first declaration in file order and `id#2`, `id#3`, ... to the
// rest. Deleting the file holding the plain id hands it to a survivor, and only a re-parse of a
// survivor can do that: RemoveFileResources would otherwise delete the plain id, strip every
// caller's edge to it and warn them it was removed, while the survivor kept its ordinal. The
// re-parse runs before the removal, and goscanner ignores declarations whose file is gone from
// disk, so by the time the deleted file's rows are removed the plain id already belongs to the
// survivor and is kept (see helper.RemoveFileResources' moved-out check).
func repeatedDeclarationSiblings(topo *domain.Topology, deleted []string, settled map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, path := range deleted {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		file, ok := topo.Resources[abs]
		if !ok || file.Kind != domain.ResourceFile || file.Language != "go" {
			continue
		}
		bases := make(map[string]bool)
		for _, id := range file.Connections["has_function"] {
			bases[repeatedDeclarationBase(id)] = true
		}
		pkgID, _ := file.Properties["from_package"].(string)
		pkg, ok := topo.Resources[pkgID]
		if !ok || len(bases) == 0 {
			continue
		}
		for _, id := range pkg.Connections["has_function"] {
			if !bases[repeatedDeclarationBase(id)] {
				continue
			}
			sibling := topo.Resources[id].Location.Path
			if sibling == "" || sibling == abs || settled[sibling] || out[sibling] {
				continue
			}
			if _, err := os.Stat(sibling); err != nil {
				continue
			}
			out[sibling] = true
		}
	}
	return out
}

// repeatedDeclarationBase strips goscanner's duplicate ordinal: "pkg.init#2" -> "pkg.init".
func repeatedDeclarationBase(id string) string {
	i := strings.LastIndex(id, "#")
	if i < 0 {
		return id
	}
	if n, err := strconv.Atoi(id[i+1:]); err != nil || n < 2 {
		return id
	}
	return id[:i]
}
