package toolflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ToolHandler executes one normalized tool invocation.
type ToolHandler func(ctx context.Context, args map[string]interface{}) (string, error)

// Registry stores tool handlers.
type Registry struct {
	handlers map[string]ToolHandler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]ToolHandler{}}
}

func (r *Registry) Register(tool string, handler ToolHandler) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return fmt.Errorf("tool is required")
	}
	if handler == nil {
		return fmt.Errorf("handler is required")
	}
	r.handlers[tool] = handler
	return nil
}

func (r *Registry) Execute(ctx context.Context, node PlanNode) (ToolResult, error) {
	if r == nil {
		return ToolResult{}, fmt.Errorf("registry is nil")
	}
	h, ok := r.handlers[node.Tool]
	if !ok {
		return ToolResult{}, fmt.Errorf("handler missing for tool=%s", node.Tool)
	}
	out, err := h(ctx, node.Args)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{NodeID: node.ID, Tool: node.Tool, Output: out}, nil
}

func (r *Registry) Tools() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
