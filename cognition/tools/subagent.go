package tools

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ProvisionedSubAgent holds a task-scoped client keypair.
// APIKey is secret and must never be surfaced to user output.
type ProvisionedSubAgent struct {
	ClientID  string
	APIKey    string
	Purpose   string
	CreatedAt time.Time
}

// ProvisionSubAgent creates a dedicated toolserver client for a background task.
func ProvisionSubAgent(task string) (ProvisionedSubAgent, error) {
	admin, err := NewGLMAdminClientFromEnv()
	if err != nil {
		return ProvisionedSubAgent{}, err
	}
	clientID := "client-subagent-" + slugTask(task) + "-" + fmt.Sprintf("%d", time.Now().Unix())
	created, err := admin.CreateClient(clientID)
	if err != nil {
		return ProvisionedSubAgent{}, err
	}
	return ProvisionedSubAgent{
		ClientID:  strings.TrimSpace(created.ClientID),
		APIKey:    strings.TrimSpace(created.APIKey),
		Purpose:   strings.TrimSpace(task),
		CreatedAt: time.Now().UTC(),
	}, nil
}

func slugTask(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "task"
	}
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 36 {
		s = s[:36]
	}
	if s == "" {
		return "task"
	}
	return s
}
