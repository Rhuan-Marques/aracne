package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/prompts"
)

// defaultChatAgentDefs lists the proprietary-chat sub-agents and the prompt
// body each ships with. Their tool lists and the "## Tools" listing are filled
// in from config (viz.chat.agents), never from the llm section.
func defaultChatAgentDefs() []struct{ name, description, prompt string } {
	return []struct{ name, description, prompt string }{
		{"explorer", "Explores the codebase for focused Viz chat questions", prompts.ExplorerPrompt()},
		{"descriptions-generation-executor", "Generates descriptions for one assigned batch of undocumented topology resources", prompts.DescriptionsGenerationExecutorPrompt()},
		{"bug-hunter", "Scans the entire project topology looking for bugs", prompts.BugHunterPrompt()},
		{"bug-judge", "Triages pending bugs by comparing against dismissed bug patterns", prompts.BugJudgePrompt()},
		{"bug-solver", "Fixes acknowledged bugs in the codebase and removes them", prompts.BugSolverPrompt()},
	}
}

// chatAgentTools resolves a chat sub-agent's tools from config, falling back to
// the built-in defaults when cfg is nil or the agent is unset.
func chatAgentTools(cfg *helper.Config, name string) []string {
	if cfg != nil {
		if ag, ok := cfg.Viz.Chat.Agents.Agents[name]; ok && len(ag.Tools) > 0 {
			return ag.Tools
		}
	}
	return helper.DefaultChatAgentTools(name)
}

// chatAgentMarkdown assembles a chat sub-agent .md: frontmatter (with the
// resolved tools) plus the prompt body with a dynamic "## Tools" listing.
func chatAgentMarkdown(name, description string, tools []string, prompt string) string {
	body := prompts.WithToolsListing(prompt, tools)
	return "---\nname: " + name + "\ndescription: " + description + "\ntools: " + strings.Join(tools, ", ") + "\n---\n\n" + body
}

// Creates default agent definition markdown files in a directory if they don't already exist.
func ensureDefaultAgentFiles(cfg *helper.Config, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, def := range defaultChatAgentDefs() {
		path := filepath.Join(dir, def.name+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		content := chatAgentMarkdown(def.name, def.description, chatAgentTools(cfg, def.name), def.prompt)
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Loads and parses agent kind definitions from markdown files in a directory.
func loadAgentKinds(dir string) ([]AgentKind, error) {
	if err := ensureDefaultAgentFiles(nil, dir); err != nil {
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

// Parses frontmatter and body from a markdown agent definition file into an AgentKind struct.
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

// Normalizes agent kind names by trimming whitespace, quotes, and replacing underscores with hyphens.
func normalizeAgentKindName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "\"'")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

// Looks up an agent kind by name from the loaded agent registry.
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

// Builds a prompt listing available CreateTasks agent kinds with their descriptions.
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

// Converts AgentKind list to AgentProfile list for display with names, descriptions, and icons.
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

// Converts kebab/snake_case agent name to title-case display format.
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

// Maps agent kinds to UI icon names.
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
