package topology

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/scanner"
)

var bugIDCounter int64

type TopologyManager struct {
	dbPath string
}

func New() *TopologyManager {
	return &TopologyManager{}
}

func (m *TopologyManager) DbPath() string {
	return m.dbPath
}

func (m *TopologyManager) FullScan(root string, reg *scanner.Registry) error {
	langScanner := reg.Detect(root)
	if langScanner == nil {
		return fmt.Errorf("no language scanner detected for %s", root)
	}

	topo, err := langScanner.Scan(root)
	if err != nil {
		return err
	}
	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return err
	}
	helper.SyncManifest(topo, m.dbPath)
	return nil
}

func (m *TopologyManager) IncrementalScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	langScanner := reg.Detect(root)
	if langScanner == nil {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return m.FullReScan(root, reg)
	}

	manifestPath := helper.ManifestPath(m.dbPath)
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return m.FullReScan(root, reg)
	}

	added, modified, deleted := helper.DiffScanFiles(root, topo.Language, manifestPath)

	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		helper.SyncManifest(topo, m.dbPath)
		return nil, nil
	}

	var allWarnings []domain.TopologyWarning

	for _, path := range append(added, modified...) {
		warnings, err := langScanner.UpdateFile(topo, path)
		if err != nil {
			allWarnings = append(allWarnings, domain.TopologyWarning{
				ID:       "error:" + path,
				SourceID: path,
				Kind:     "",
				Message:  fmt.Sprintf("error updating %s: %v", path, err),
			})
		} else {
			allWarnings = append(allWarnings, warnings...)
		}
	}

	for _, path := range deleted {
		warnings := helper.RemoveFileResources(topo, path)
		allWarnings = append(allWarnings, warnings...)
	}

	for _, w := range allWarnings {
		topo.Warnings[w.ID] = w
	}

	helper.CleanupOrphanedWarnings(topo)

	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return allWarnings, fmt.Errorf("write topology db: %w", err)
	}

	helper.CleanupOrphanedBugs(m.dbPath, topo)
	helper.SyncManifest(topo, m.dbPath)

	return allWarnings, nil
}

func (m *TopologyManager) FullReScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	langScanner := reg.Detect(root)
	if langScanner == nil {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	newTopo, err := langScanner.Scan(root)
	if err != nil {
		return nil, err
	}

	oldTopo, readErr := helper.ReadDb(m.dbPath)
	if readErr == nil && oldTopo != nil {
		for id, oldRes := range oldTopo.Resources {
			newRes, exists := newTopo.Resources[id]
			if !exists {
				continue
			}
			if newRes.Description == "" && oldRes.Description != "" {
				newRes.Description = oldRes.Description
				newTopo.Resources[id] = newRes
			}
		}
	}

	if err := helper.WriteDb(newTopo, m.dbPath); err != nil {
		return nil, err
	}

	helper.CleanupOrphanedBugs(m.dbPath, newTopo)
	helper.SyncManifest(newTopo, m.dbPath)
	return nil, nil
}

func (m *TopologyManager) Load(path string) error {
	m.dbPath = path
	return nil
}

func (m *TopologyManager) Write(path string) error {
	if m.dbPath == "" {
		return nil
	}
	src, err := os.Open(m.dbPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func (m *TopologyManager) ReadAll() (*domain.Topology, error) {
	return helper.ReadDb(m.dbPath)
}

func (m *TopologyManager) Cut(loc domain.Location) (*domain.CodeEntry, error) {
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", loc.Path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if loc.EndsAt < loc.StartsAt {
		return nil, fmt.Errorf("EndsAt %d < StartsAt %d", loc.EndsAt, loc.StartsAt)
	}
	if loc.StartsAt < 1 || loc.StartsAt > len(lines) {
		return nil, fmt.Errorf("StartsAt %d out of range (1-%d)", loc.StartsAt, len(lines))
	}
	if loc.EndsAt > len(lines) {
		return nil, fmt.Errorf("EndsAt %d out of range (max %d)", loc.EndsAt, len(lines))
	}
	cut := strings.Join(lines[loc.StartsAt-1:loc.EndsAt], "\n")
	return &domain.CodeEntry{Location: loc, Cut: cut}, nil
}

func (m *TopologyManager) UpdateFile(path string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, fmt.Errorf("read topology db: %w", err)
	}

	var langScanner scanner.LanguageScanner
	if topo.Language != "" {
		for _, s := range reg.All() {
			if s.Name() == topo.Language {
				langScanner = s
				break
			}
		}
	}
	if langScanner == nil {
		langScanner = reg.Detect(topo.Root)
	}
	if langScanner == nil {
		return nil, fmt.Errorf("no language scanner found")
	}

	beforeWarnings := cloneWarnings(topo.Warnings)

	_, err = langScanner.UpdateFile(topo, path)
	if err != nil {
		return nil, err
	}

	helper.CleanupOrphanedWarnings(topo)
	warnings := addedWarnings(beforeWarnings, topo.Warnings)

	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return nil, fmt.Errorf("write topology db: %w", err)
	}

	helper.SyncManifest(topo, m.dbPath)

	return warnings, nil
}

func cloneWarnings(warnings map[string]domain.TopologyWarning) map[string]domain.TopologyWarning {
	cloned := make(map[string]domain.TopologyWarning, len(warnings))
	for id, warning := range warnings {
		cloned[id] = warning
	}
	return cloned
}

func addedWarnings(before, after map[string]domain.TopologyWarning) []domain.TopologyWarning {
	var added []domain.TopologyWarning
	for id, warning := range after {
		if _, exists := before[id]; !exists {
			added = append(added, warning)
		}
	}
	sort.Slice(added, func(i, j int) bool {
		return added[i].ID < added[j].ID
	})
	return added
}

func (m *TopologyManager) FindResourcesByName(name string, kinds ...domain.ResourceKind) ([]string, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}
	var results []string
	for id, res := range topo.Resources {
		if res.Name != name {
			continue
		}
		if len(kindSet) > 0 && !kindSet[res.Kind] {
			continue
		}
		results = append(results, id)
	}
	return results, nil
}

func (m *TopologyManager) CreateBug(nodeID string, description string) (*domain.KnownBug, error) {
	bug := domain.KnownBug{
		ID:          fmt.Sprintf("bug_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&bugIDCounter, 1)),
		NodeID:      nodeID,
		Description: description,
		State:       domain.BugPending,
	}
	if err := helper.CreateBug(m.dbPath, bug); err != nil {
		return nil, fmt.Errorf("create bug: %w", err)
	}
	return &bug, nil
}

func (m *TopologyManager) ListBugs(nodeID string, state domain.BugState) ([]domain.KnownBug, error) {
	return helper.ReadBugs(m.dbPath, nodeID, state)
}

func (m *TopologyManager) AcknowledgeBug(bugID string) error {
	return helper.UpdateBugState(m.dbPath, bugID, domain.BugAcknowledged)
}

func (m *TopologyManager) DismissBug(bugID string) error {
	return helper.UpdateBugState(m.dbPath, bugID, domain.BugDismissed)
}

func (m *TopologyManager) DeleteBug(bugID string) error {
	return helper.DeleteBug(m.dbPath, bugID)
}

func (m *TopologyManager) DeleteAllBugs() error {
	return helper.DeleteAllBugs(m.dbPath)
}

func (m *TopologyManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.dbPath, kind, id, description)
}

func (m *TopologyManager) GetWarnings() (map[string]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	return topo.Warnings, nil
}

func (m *TopologyManager) ListWarnings(sourceID, targetID string, kind domain.WarningKind) ([]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	var results []domain.TopologyWarning
	for _, w := range topo.Warnings {
		if sourceID != "" && w.SourceID != sourceID {
			continue
		}
		if targetID != "" && w.TargetID != targetID {
			continue
		}
		if kind != "" && w.Kind != kind {
			continue
		}
		results = append(results, w)
	}
	return results, nil
}
