package chat

import (
	"encoding/json"
	"fmt"

	"aracne/internal/llm/tools"
)

type CreateTasksTool struct{}

func (c *CreateTasksTool) Name() string { return "CreateTasks" }

func (c *CreateTasksTool) Description() string {
	return "Create a group of explorer sub-agent tasks. Each task includes agent_kind=explorer, prompt, and need_result. The tool returns after all tasks finish."
}

func (c *CreateTasksTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "worker_count", Type: "number", Description: "Maximum number of sub-agent workers to run concurrently", Required: false},
		{Name: "tasks", Type: "array", Description: "Explorer tasks to run. Each item has agent_kind=explorer, prompt, and need_result", Required: true},
	}
}

func (c *CreateTasksTool) Run(args json.RawMessage) (string, error) {
	return "", fmt.Errorf("CreateTasks is handled by the chat session")
}
