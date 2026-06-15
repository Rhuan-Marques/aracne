package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/prompts"
)

func ensureDefaultAgentFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	defaults := map[string]string{
		"explorer":                         prompts.ExplorerAgentContent(),
		"descriptions-generation-executor": prompts.DescriptionsGenerationExecutorContent(),
		"bug-hunter":                       prompts.BugHunterAgentContent(),
		"bug-judge":                        prompts.BugJudgeAgentContent(),
		"bug-solver":                       prompts.BugSolverAgentContent(),
	}
	for name, content := range defaults {
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func loadAgentKinds(dir string) ([]AgentKind, error) {
	if err := ensureDefaultAgentFiles(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var kinds []AgentKind
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		kind, err := parseAgentKindMarkdown(string(data), path)
		if err != nil {
			return nil, err
		}
		if seen[kind.Name] {
			return nil, fmt.Errorf("duplicate agent kind %q", kind.Name)
		}
		seen[kind.Name] = true
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Name < kinds[j].Name })
	return kinds, nil
}

func parseAgentKindMarkdown(content, path string) (AgentKind, error) {
	content = strings.TrimPrefix(strings.ReplaceAll(content, "\r\n", "\n"), "\ufeff")
	if !strings.HasPrefix(content, "---\n") {
		return AgentKind{}, fmt.Errorf("agent file %s is missing frontmatter", path)
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return AgentKind{}, fmt.Errorf("agent file %s has unterminated frontmatter", path)
	}
	frontmatter := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n---"):])
	kind := AgentKind{SystemPrompt: body}
	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			kind.Name = normalizeAgentKindName(value)
		case "description":
			kind.Description = value
		case "tools":
			for _, tool := range strings.Split(value, ",") {
				tool = strings.TrimSpace(tool)
				if tool != "" {
					kind.Tools = append(kind.Tools, tool)
				}
			}
		}
	}
	if kind.Name == "" {
		return AgentKind{}, fmt.Errorf("agent file %s is missing name", path)
	}
	if kind.Description == "" {
		return AgentKind{}, fmt.Errorf("agent file %s is missing description", path)
	}
	if len(kind.Tools) == 0 {
		return AgentKind{}, fmt.Errorf("agent file %s is missing tools", path)
	}
	if kind.SystemPrompt == "" {
		return AgentKind{}, fmt.Errorf("agent file %s is missing system prompt", path)
	}
	return kind, nil
}

func normalizeAgentKindName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "\"'")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

func resolveAgentKind(dir, name string) (AgentKind, error) {
	name = normalizeAgentKindName(name)
	kinds, err := loadAgentKinds(dir)
	if err != nil {
		return AgentKind{}, err
	}
	for _, kind := range kinds {
		if kind.Name == name {
			return kind, nil
		}
	}
	return AgentKind{}, fmt.Errorf("unknown agent kind %q", name)
}

func agentKindPrompt(dir string) string {
	kinds, err := loadAgentKinds(dir)
	if err != nil || len(kinds) == 0 {
		return ""
	}
	createTaskAllowed := createTasksAgentKindSet()
	var b strings.Builder
	b.WriteString("\n\nAvailable CreateTasks Agent Kinds:\n")
	count := 0
	for _, kind := range kinds {
		if !createTaskAllowed[kind.Name] {
			continue
		}
		count++
		b.WriteString("- ")
		b.WriteString(kind.Name)
		b.WriteString(": ")
		b.WriteString(kind.Description)
		b.WriteByte('\n')
	}
	if count == 0 {
		return ""
	}
	b.WriteString("\nYou have access to CreateTasks. Use it when focused exploration can be split across explorer sub-agents. Each task must use agent_kind=explorer and include prompt and need_result. CreateTasks waits until every task finishes, then returns requested outputs and failure counts before you continue.")
	return b.String()
}

func agentProfilesFromKinds(kinds []AgentKind) []AgentProfile {
	profiles := make([]AgentProfile, 0, len(kinds))
	for _, kind := range kinds {
		profiles = append(profiles, AgentProfile{
			ID:          kind.Name,
			Name:        displayAgentKindName(kind.Name),
			Description: kind.Description,
			Icon:        agentKindIcon(kind.Name),
		})
	}
	return profiles
}

func displayAgentKindName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func agentKindIcon(name string) string {
	switch normalizeAgentKindName(name) {
	case "explorer":
		return "search"
	case "descriptions-generation-executor":
		return "file-text"
	case "bug-hunter":
		return "bug"
	case "bug-judge":
		return "scale"
	case "bug-solver":
		return "wrench"
	default:
		return "bot"
	}
}
