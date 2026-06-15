package topology

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
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
	topo, err := scanAllLanguages(root, reg)
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
	if info, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("topology root %s is not accessible: %w", root, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("topology root %s is not a directory", root)
	}
	if len(reg.DetectAll(root)) == 0 {
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

	langScanners := reg.DetectAll(root)
	var added, modified, deleted []string
	for _, ls := range langScanners {
		a, m, d, diffErr := helper.DiffScanFiles(root, ls.Name(), manifestPath)
		if diffErr != nil {
			return nil, diffErr
		}
		added = append(added, a...)
		modified = append(modified, m...)
		deleted = append(deleted, d...)
	}

	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		helper.SyncManifest(topo, m.dbPath)
		return nil, nil
	}

	var allWarnings []domain.TopologyWarning

	for _, path := range append(added, modified...) {
		langScanner := reg.DetectFile(path)
		if langScanner == nil {
			continue
		}
		warnings, err := updateFileWithScanner(topo, langScanner, path)
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
	normalizeTopologyLanguages(topo)

	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return allWarnings, fmt.Errorf("write topology db: %w", err)
	}

	helper.CleanupOrphanedBugs(m.dbPath, topo)
	helper.SyncManifest(topo, m.dbPath)

	return allWarnings, nil
}

func (m *TopologyManager) FullReScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	newTopo, err := scanAllLanguages(root, reg)
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
	if loc.StartsAt == 0 && loc.EndsAt == 0 {
		loc.StartsAt = 1
		loc.EndsAt = len(lines)
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
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	finish := func(warnings []domain.TopologyWarning) ([]domain.TopologyWarning, error) {
		for _, w := range warnings {
			topo.Warnings[w.ID] = w
		}
		helper.CleanupOrphanedWarnings(topo)
		if err := helper.WriteDb(topo, m.dbPath); err != nil {
			return nil, fmt.Errorf("write topology db: %w", err)
		}
		helper.CleanupOrphanedBugs(m.dbPath, topo)
		helper.SyncManifest(topo, m.dbPath)
		return warnings, nil
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return finish(helper.RemoveFileResources(topo, absPath))
	} else if err != nil {
		return nil, err
	}

	langScanner := reg.DetectFile(absPath)
	if langScanner == nil {
		return finish(helper.RemoveFileResources(topo, absPath))
	}
	if !helper.IsSourceFile(absPath, langScanner.Name()) {
		return finish(helper.RemoveFileResources(topo, absPath))
	}

	beforeWarnings := cloneWarnings(topo.Warnings)

	_, err = updateFileWithScanner(topo, langScanner, absPath)
	if err != nil {
		return nil, err
	}

	warnings := addedWarnings(beforeWarnings, topo.Warnings)
	return finish(warnings)
}

func scanAllLanguages(root string, reg *scanner.Registry) (*domain.Topology, error) {
	langScanners := reg.DetectAll(root)
	if len(langScanners) == 0 {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	merged := &domain.Topology{
		Resources: make(map[string]domain.Resource),
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	for _, langScanner := range langScanners {
		topo, err := langScanner.Scan(root)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", langScanner.Name(), err)
		}
		tagTopologyLanguage(topo, langScanner.Name())
		mergeTopology(merged, topo)
	}
	normalizeTopologyLanguages(merged)
	return merged, nil
}

func updateFileWithScanner(topo *domain.Topology, langScanner scanner.LanguageScanner, path string) ([]domain.TopologyWarning, error) {
	lang := langScanner.Name()
	subTopo := languageSubTopology(topo, lang)
	warnings, err := langScanner.UpdateFile(subTopo, path)
	if err != nil {
		return nil, err
	}
	tagTopologyLanguage(subTopo, lang)
	topo.Warnings = cloneWarnings(subTopo.Warnings)
	removeLanguageResources(topo, lang)
	mergeTopology(topo, subTopo)
	normalizeTopologyLanguages(topo)
	return warnings, nil
}

func languageSubTopology(topo *domain.Topology, language string) *domain.Topology {
	sub := &domain.Topology{
		Root:      topo.Root,
		Language:  language,
		Languages: []string{language},
		Resources: make(map[string]domain.Resource),
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	for id, res := range topo.Resources {
		if resourceLanguage(res, topo.Language) == language {
			sub.Resources[id] = cloneResource(res)
		}
	}
	for id, warning := range topo.Warnings {
		sub.Warnings[id] = warning
	}
	for path, msg := range topo.Errors {
		if helper.IsSourceFile(path, language) {
			sub.Errors[path] = msg
		}
	}
	return sub
}

func tagTopologyLanguage(topo *domain.Topology, language string) {
	if topo == nil {
		return
	}
	if topo.Resources == nil {
		topo.Resources = make(map[string]domain.Resource)
	}
	if topo.Warnings == nil {
		topo.Warnings = make(map[string]domain.TopologyWarning)
	}
	if topo.Errors == nil {
		topo.Errors = make(map[string]string)
	}
	for id, res := range topo.Resources {
		if res.Language == "" {
			res.Language = language
			topo.Resources[id] = res
		}
	}
	if len(topo.Languages) == 0 && language != "" {
		topo.Languages = []string{language}
	}
	if topo.Language == "" {
		topo.Language = language
	}
}

func mergeTopology(dst, src *domain.Topology) {
	if src == nil {
		return
	}
	if dst.Root == "" {
		dst.Root = src.Root
	}
	if dst.Resources == nil {
		dst.Resources = make(map[string]domain.Resource)
	}
	if dst.Warnings == nil {
		dst.Warnings = make(map[string]domain.TopologyWarning)
	}
	if dst.Errors == nil {
		dst.Errors = make(map[string]string)
	}
	for id, res := range src.Resources {
		if existing, exists := dst.Resources[id]; exists && resourceLanguage(existing, dst.Language) != resourceLanguage(res, src.Language) {
			dst.Errors["resource-collision:"+id] = fmt.Sprintf("resource id %s exists in both %s and %s", id, resourceLanguage(existing, dst.Language), resourceLanguage(res, src.Language))
			continue
		}
		dst.Resources[id] = cloneResource(res)
	}
	for id, warning := range src.Warnings {
		dst.Warnings[id] = warning
	}
	for path, msg := range src.Errors {
		dst.Errors[path] = msg
	}
}

func removeLanguageResources(topo *domain.Topology, language string) {
	removed := make(map[string]bool)
	for id, res := range topo.Resources {
		if resourceLanguage(res, topo.Language) == language {
			removed[id] = true
			delete(topo.Resources, id)
		}
	}
	if len(removed) == 0 {
		return
	}
	for id, res := range topo.Resources {
		for connType, targets := range res.Connections {
			kept := targets[:0]
			for _, target := range targets {
				if !removed[target] {
					kept = append(kept, target)
				}
			}
			if len(kept) == 0 {
				delete(res.Connections, connType)
			} else {
				res.Connections[connType] = kept
			}
		}
		topo.Resources[id] = res
	}
	for path := range topo.Errors {
		if helper.IsSourceFile(path, language) {
			delete(topo.Errors, path)
		}
	}
}

func normalizeTopologyLanguages(topo *domain.Topology) {
	if topo == nil {
		return
	}
	seen := make(map[string]bool)
	for id, res := range topo.Resources {
		lang := resourceLanguage(res, topo.Language)
		if lang == "" {
			continue
		}
		if res.Language == "" {
			res.Language = lang
			topo.Resources[id] = res
		}
		seen[lang] = true
	}
	languages := make([]string, 0, len(seen))
	for lang := range seen {
		languages = append(languages, lang)
	}
	sort.Strings(languages)
	topo.Languages = languages
	if len(languages) == 1 {
		topo.Language = languages[0]
	} else if len(languages) > 1 {
		topo.Language = "multi"
	}
}

func resourceLanguage(res domain.Resource, fallback string) string {
	if res.Language != "" {
		return res.Language
	}
	if fallback != "multi" {
		return fallback
	}
	return ""
}

func cloneResource(res domain.Resource) domain.Resource {
	clone := res
	clone.Properties = make(map[string]any, len(res.Properties))
	for key, value := range res.Properties {
		clone.Properties[key] = value
	}
	clone.Connections = make(map[string][]string, len(res.Connections))
	for kind, targets := range res.Connections {
		clone.Connections[kind] = append([]string(nil), targets...)
	}
	return clone
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

func (m *TopologyManager) ClearDescriptions(targets []domain.ResourceKind) (int64, error) {
	return helper.ClearDescriptions(m.dbPath, targets)
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
