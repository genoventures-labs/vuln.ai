package toolflow

import "time"

// Invocation captures one raw model-requested tool operation.
type Invocation struct {
	Tool   string                 `json:"tool"`
	Args   map[string]interface{} `json:"args"`
	Source string                 `json:"source,omitempty"`
	Raw    string                 `json:"raw,omitempty"`
}

// NormalizedInvocation is an invocation after policy normalization and schema checks.
type NormalizedInvocation struct {
	Tool         string                 `json:"tool"`
	Args         map[string]interface{} `json:"args"`
	Canonical    bool                   `json:"canonical"`
	Deprecations []string               `json:"deprecations,omitempty"`
	OriginalIdx  int                    `json:"original_idx"`
}

// RetryPolicySpec controls per-node retry behavior.
type RetryPolicySpec struct {
	MaxAttempts    int      `json:"max_attempts"`
	BackoffMS      int      `json:"backoff_ms"`
	JitterMS       int      `json:"jitter_ms"`
	RetryableCodes []string `json:"retryable_codes,omitempty"`
}

// PlanNode represents one executable node in a deterministic DAG.
type PlanNode struct {
	ID                string                 `json:"id"`
	Tool              string                 `json:"tool"`
	Args              map[string]interface{} `json:"args"`
	DependsOn         []string               `json:"depends_on,omitempty"`
	InferredDependsOn []string               `json:"inferred_depends_on,omitempty"`
	Priority          int                    `json:"priority"`
	TimeoutMS         int                    `json:"timeout_ms,omitempty"`
	RetryPolicy       RetryPolicySpec        `json:"retry_policy"`
	CostClass         string                 `json:"cost_class,omitempty"`
}

// Plan is the compiled execution graph.
type Plan struct {
	Nodes             []PlanNode `json:"nodes"`
	OrderedIDs        []string   `json:"ordered_ids"`
	DeterministicHash string     `json:"deterministic_hash"`
	ParallelGroups    int        `json:"parallel_groups"`
	InferredEdgeCount int        `json:"inferred_edge_count"`
}

// ToolResult is one node execution outcome.
type ToolResult struct {
	NodeID     string    `json:"node_id"`
	Tool       string    `json:"tool"`
	Output     string    `json:"output,omitempty"`
	Err        error     `json:"-"`
	ErrMessage string    `json:"error,omitempty"`
	Citations  []string  `json:"citations,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Attempts   int       `json:"attempts,omitempty"`
	WorkerID   string    `json:"worker_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
}

// PolicyDecision records explicit allow/deny outcomes.
type PolicyDecision struct {
	Tool      string `json:"tool"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// TraceStep records planner/executor lifecycle events.
type TraceStep struct {
	NodeID   string `json:"node_id,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Stage    string `json:"stage"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Attempt  int    `json:"attempt,omitempty"`
	Reason   string `json:"reason,omitempty"`
	WorkerID string `json:"worker_id,omitempty"`
	AtUnixMS int64  `json:"at_unix_ms"`
}

// ExecutionTrace records deterministic execution diagnostics.
type ExecutionTrace struct {
	TurnID           string           `json:"turn_id"`
	PlanHash         string           `json:"plan_hash"`
	Steps            []TraceStep      `json:"steps,omitempty"`
	PolicyDecisions  []PolicyDecision `json:"policy_decisions,omitempty"`
	DeprecationNotes []string         `json:"deprecation_notes,omitempty"`
	Citations        []string         `json:"citations,omitempty"`
}

// ExecEvent is a streaming execution signal emitted while plan nodes run.
type ExecEvent struct {
	NodeID     string
	Tool       string
	Kind       string // start|output|error|complete
	Chunk      string
	Attempt    int
	WorkerID   string
	DurationMS int64
	At         time.Time
}

// ExecOptions controls runtime execution behavior.
type ExecOptions struct {
	MaxWorkers        int
	NodeTimeout       time.Duration
	FailClosed        bool
	EnableTrace       bool
	SequentialDefault bool
	GlobalBudget      time.Duration
	RetryEnabled      bool
	RetryMaxAttempts  int
	RetryBackoff      time.Duration
	RetryJitter       time.Duration
	OnEvent           func(ExecEvent)
}

// PlannerHints carries runtime context for plan compilation.
type PlannerHints struct {
	Query    string
	Stage    string
	Surface  string
	BudgetMS int
}
