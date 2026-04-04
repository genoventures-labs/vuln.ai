package connectors

import (
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

// ConnectorSubAgent wraps a Connector with a task-scoped provisioned sub-agent.
// This allows TALOS to trigger connector ingestion autonomously from
// research and planning daemon workflows.
type ConnectorSubAgent struct {
	Agent     tools.ProvisionedSubAgent
	Connector Connector
}

// ProvisionConnectorSubAgent creates a dedicated toolserver client for
// the given connector, suitable for use in background daemon workflows.
func ProvisionConnectorSubAgent(c Connector) (ConnectorSubAgent, error) {
	if c == nil {
		return ConnectorSubAgent{}, fmt.Errorf("connector must not be nil")
	}
	task := fmt.Sprintf("connector-%s-%d", slugify(c.Name()), time.Now().Unix())
	agent, err := tools.ProvisionSubAgent(task)
	if err != nil {
		return ConnectorSubAgent{}, fmt.Errorf("provisioning sub-agent for connector %s: %w", c.Name(), err)
	}
	return ConnectorSubAgent{
		Agent:     agent,
		Connector: c,
	}, nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return strings.Trim(string(out), "-")
}
