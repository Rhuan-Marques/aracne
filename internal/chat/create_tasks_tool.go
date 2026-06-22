package chat

import (
	"encoding/json"
	"fmt"

	"aracne/internal/llm/tools"
)

// Tool that creates sub-tasks for the agent to execute in parallel.
type CreateTasksTool struct{}

// Returns the tool name "CreateTasks"
func (c *CreateTasksTool) Name() string { return "CreateTasks" }

// Returns description of CreateTasks tool for spawning explorer sub-agent tasks with prompts
func (c *CreateTasksTool) Description() string {
	return "Create a group of explorer sub-agent tasks. Each task includes agent_kind=explorer, prompt, and need_result. The tool returns after all tasks finish."
}

// Returns parameter schema for explorer sub-agent task creation
func (c *CreateTasksTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "worker_count", Type: "number", Description: "Maximum number of sub-agent workers to run concurrently", Required: false},
		{Name: "tasks", Type: "array", Description: "Explorer tasks to run. Each item has agent_kind=explorer, prompt, and need_result", Required: true},
	}
}

// Stub that defers explorer task handling to the chat session
func (c *CreateTasksTool) Run(args json.RawMessage) (string, error) {
	return "", fmt.Errorf("CreateTasks is handled by the chat session")
}
