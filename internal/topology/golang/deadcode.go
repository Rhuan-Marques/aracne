package golang

import (
	"fmt"
	"go/token"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

type DeadConfidence string

const (
	DeadCertain  DeadConfidence = "certain"
	DeadPossible DeadConfidence = "possible"
)

// Represents an unused code resource with its kind, location, confidence level, and reason for being marked as dead.
type DeadResource struct {
	ID         string
	Kind       domain.ResourceKind
	Name       string
	Location   domain.Location
	Confidence DeadConfidence
	Reason     string
}

// Contains a list of dead code resources identified by analysis.
type DeadCodeReport struct {
	Resources []DeadResource
}

// Analyzes a Go topology to identify unused functions, structs, interfaces, named types, and variables, returning them sorted by confidence and kind.
func FindDeadResources(gt *GolangTopology) *DeadCodeReport {
	incoming := buildIncomingIndex(gt)
	aliveIDs := markAlwaysAlive(gt)

	var dead []DeadResource

	dead = append(dead, findDeadFunctions(gt, incoming, aliveIDs)...)
	dead = append(dead, findDeadStructs(gt, incoming, aliveIDs)...)
	dead = append(dead, findDeadInterfaces(gt, incoming, aliveIDs)...)
	dead = append(dead, findDeadNamedTypes(gt, incoming, aliveIDs)...)
	dead = append(dead, findDeadExtVars(gt, incoming, aliveIDs)...)

	sort.SliceStable(dead, func(i, j int) bool {
		if dead[i].Confidence != dead[j].Confidence {
			return dead[i].Confidence < dead[j].Confidence
		}
		if dead[i].Kind != dead[j].Kind {
			return dead[i].Kind < dead[j].Kind
		}
		return dead[i].ID < dead[j].ID
	})

	return &DeadCodeReport{Resources: dead}
}

// Builds a map of resource IDs to their incoming reference counts from functions, structs, interfaces, named types, and file imports.
func buildIncomingIndex(gt *GolangTopology) map[string]int {
	index := make(map[string]int)

	for _, fn := range gt.Functions {
		for kind, targets := range fn.Connections {
			switch kind {
			case ConnCalls, ConnUsesStruct, ConnUsesNamedType, ConnUsesIface, ConnUsesExtVar, ConnUsesPkg, ConnUsesDep:
				for _, t := range targets {
					index[t]++
				}
			}
		}
	}

	for _, s := range gt.Structs {
		for kind, targets := range s.Connections {
			switch kind {
			case ConnUsesStruct, ConnUsesNamedType, ConnUsesIface, ConnUsesExtVar, ConnUsesPkg, ConnUsesDep:
				for _, t := range targets {
					index[t]++
				}
			}
		}
	}

	for _, iface := range gt.Interfaces {
		for kind, targets := range iface.Connections {
			switch kind {
			case ConnUsesNamedType, ConnUsesPkg, ConnUsesDep:
				for _, t := range targets {
					index[t]++
				}
			}
		}
		for _, implStrID := range iface.ImplementedBy() {
			index[fmt.Sprintf("__implements__%s", string(implStrID))]++
		}
	}

	for _, nt := range gt.NamedTypes {
		for kind, targets := range nt.Connections {
			switch kind {
			case ConnUsesNamedType, ConnUsesPkg, ConnUsesDep:
				for _, t := range targets {
					index[t]++
				}
			}
		}
	}

	for _, f := range gt.Files {
		for kind, targets := range f.Connections {
			switch kind {
			case ConnImportsPkg, ConnImportsDep:
				for _, t := range targets {
					index[t]++
				}
			}
		}
	}

	return index
}

// Checks if a Go identifier name is exported (public).
func isExported(name string) bool {
	if name == "" {
		return false
	}
	return token.IsExported(name)
}

// Identifies entry points and interface implementations as always-alive code.
func markAlwaysAlive(gt *GolangTopology) map[string]bool {
	alive := make(map[string]bool)

	for id, fn := range gt.Functions {
		if fn.Name == "main" {
			alive[id] = true
		}
		if fn.Name == "init" {
			alive[id] = true
		}
	}

	for _, fn := range gt.Functions {
		if fn.MethodFrom != nil {
			str, ok := gt.Structs[*fn.MethodFrom]
			if !ok {
				continue
			}
			for _, ifaceID := range str.Implements() {
				iface, used := gt.Interfaces[ifaceID]
				if !used {
					continue
				}
				alive[string(ifaceID)] = true
				for _, implID := range iface.ImplementedBy() {
					alive[string(implID)] = true
					implStr := gt.Structs[implID]
					for _, mid := range implStr.Methods() {
						alive[string(mid)] = true
					}
				}
			}
		}
	}

	return alive
}

// Identifies uncalled functions and methods, marking them as dead with confidence levels based on export status.
func findDeadFunctions(gt *GolangTopology, incoming map[string]int, alive map[string]bool) []DeadResource {
	var dead []DeadResource
	for id, fn := range gt.Functions {
		if alive[id] {
			continue
		}
		count := incoming[id]
		if fn.MethodFrom != nil {
			methodConnection := fmt.Sprintf("__method__%s", id)
			count += incoming[methodConnection]
		}
		if count > 0 {
			continue
		}
		confidence := DeadCertain
		if isExported(fn.Name) {
			confidence = DeadPossible
		}
		kind := domain.ResourceFunction
		if fn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		reason := "no callers within project"
		if confidence == DeadPossible {
			reason = "no internal callers (exported — may have external users)"
		}
		dead = append(dead, DeadResource{
			ID:         id,
			Kind:       kind,
			Name:       fn.Name,
			Location:   fn.Loc,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	return dead
}

// Identifies unused structs by checking for zero incoming references and interface implementations, marking exported ones as possibly dead.
func findDeadStructs(gt *GolangTopology, incoming map[string]int, alive map[string]bool) []DeadResource {
	var dead []DeadResource
	for id, s := range gt.Structs {
		if alive[id] {
			continue
		}
		count := incoming[id]

		for _, ifaceID := range s.Implements() {
			if incoming[fmt.Sprintf("__implements__%s", string(ifaceID))] > 0 {
				count++
			}
		}

		if count > 0 {
			continue
		}
		confidence := DeadCertain
		if isExported(s.Name) {
			confidence = DeadPossible
		}
		reason := "no references within project"
		if confidence == DeadPossible {
			reason = "no internal references (exported — may have external users)"
		}
		dead = append(dead, DeadResource{
			ID:         id,
			Kind:       domain.ResourceStruct,
			Name:       s.Name,
			Location:   s.Loc,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	return dead
}

// Identifies unused interfaces by checking for zero incoming references, marking exported ones as possibly dead.
func findDeadInterfaces(gt *GolangTopology, incoming map[string]int, alive map[string]bool) []DeadResource {
	var dead []DeadResource
	for id, iface := range gt.Interfaces {
		if alive[id] {
			continue
		}
		count := incoming[id]
		if count > 0 {
			continue
		}
		confidence := DeadCertain
		if isExported(iface.Name) {
			confidence = DeadPossible
		}
		reason := "no references within project"
		if confidence == DeadPossible {
			reason = "no internal references (exported — may have external users)"
		}
		dead = append(dead, DeadResource{
			ID:         id,
			Kind:       domain.ResourceInterface,
			Name:       iface.Name,
			Location:   iface.Loc,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	return dead
}

// Identifies unused named types by checking for zero incoming references, marking exported ones as possibly dead.
func findDeadNamedTypes(gt *GolangTopology, incoming map[string]int, alive map[string]bool) []DeadResource {
	var dead []DeadResource
	for id, nt := range gt.NamedTypes {
		if alive[id] {
			continue
		}
		count := incoming[id]
		if count > 0 {
			continue
		}
		confidence := DeadCertain
		if isExported(nt.Name) {
			confidence = DeadPossible
		}
		reason := "no references within project"
		if confidence == DeadPossible {
			reason = "no internal references (exported — may have external users)"
		}
		shortName := nt.Name
		if dotIdx := strings.LastIndex(shortName, "."); dotIdx >= 0 {
			shortName = shortName[dotIdx+1:]
		}
		dead = append(dead, DeadResource{
			ID:         id,
			Kind:       domain.ResourceNamedType,
			Name:       shortName,
			Location:   nt.Loc,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	return dead
}

// Identifies unreferenced external variables, marking them as dead with confidence levels based on export status.
func findDeadExtVars(gt *GolangTopology, incoming map[string]int, alive map[string]bool) []DeadResource {
	var dead []DeadResource
	for id, v := range gt.ExternalVars {
		if alive[id] {
			continue
		}
		count := incoming[id]
		if count > 0 {
			continue
		}
		confidence := DeadCertain
		if isExported(v.Name) {
			confidence = DeadPossible
		}
		reason := "no references within project"
		if confidence == DeadPossible {
			reason = "no internal references (exported — may have external users)"
		}
		dead = append(dead, DeadResource{
			ID:         id,
			Kind:       domain.ResourceVariable,
			Name:       v.Name,
			Location:   v.Location,
			Confidence: confidence,
			Reason:     reason,
		})
	}
	return dead
}

// Returns a filtered report containing only dead resources matching the given kinds.
func (r *DeadCodeReport) FilterByKinds(kinds ...domain.ResourceKind) *DeadCodeReport {
	if len(kinds) == 0 {
		return r
	}
	set := make(map[domain.ResourceKind]bool, len(kinds))
	for _, k := range kinds {
		set[k] = true
	}
	var filtered []DeadResource
	for _, res := range r.Resources {
		if set[res.Kind] {
			filtered = append(filtered, res)
		}
	}
	return &DeadCodeReport{Resources: filtered}
}

// Returns a filtered report containing only dead resources from the given package path.
func (r *DeadCodeReport) FilterByPackage(pkgPath string) *DeadCodeReport {
	if pkgPath == "" {
		return r
	}
	var filtered []DeadResource
	for _, res := range r.Resources {
		if strings.HasPrefix(res.ID, pkgPath) {
			filtered = append(filtered, res)
		}
	}
	return &DeadCodeReport{Resources: filtered}
}

// Returns a filtered report containing only resources marked as definitely dead.
func (r *DeadCodeReport) FilterCertain() *DeadCodeReport {
	var filtered []DeadResource
	for _, res := range r.Resources {
		if res.Confidence == DeadCertain {
			filtered = append(filtered, res)
		}
	}
	return &DeadCodeReport{Resources: filtered}
}

// Returns a filtered report containing only resources marked as possibly dead.
func (r *DeadCodeReport) FilterPossible() *DeadCodeReport {
	var filtered []DeadResource
	for _, res := range r.Resources {
		if res.Confidence == DeadPossible {
			filtered = append(filtered, res)
		}
	}
	return &DeadCodeReport{Resources: filtered}
}

// Returns counts of certain and possible dead resources in the report.
func (r *DeadCodeReport) Count() (certain, possible int) {
	for _, res := range r.Resources {
		switch res.Confidence {
		case DeadCertain:
			certain++
		case DeadPossible:
			possible++
		}
	}
	return
}
