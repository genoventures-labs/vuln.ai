package cognition

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"
)

const (
	defaultPruneThreshold = 0.30
	defaultUCB1C          = 1.25
)

type MCTSStrategy string

const (
	MCTSStrategyPUCT MCTSStrategy = "puct"
	MCTSStrategyUCB1 MCTSStrategy = "ucb1"
)

// ThoughtNode is a branch-capable node used in MCTS thought search.
type ThoughtNode struct {
	ID               string
	Answer           string
	Depth            int
	Parent           *ThoughtNode
	Children         []*ThoughtNode
	Visits           int
	VirtualVisits    int
	ValueSum         float64
	Score            float64
	Confidence       float64
	Prior            float64
	ChildrenExpanded int
	Pruned           bool
	PruneReason      string
	LastEvalErr      string
}

// MCTSConfig controls selection/expansion/pruning behavior.
type MCTSConfig struct {
	Iterations         int
	BranchFactor       int
	RolloutDepth       int
	UCB1C              float64
	PruneThreshold     float64
	BaseWeight         float64
	AdvWeight          float64
	EvidenceWeight     float64
	ContraWeight       float64
	Seed               int64
	Strategy           MCTSStrategy
	MaxChildrenPerNode int
	WideningAlpha      float64
	WideningK          float64
	PriorWeight        float64
	VirtualLoss        float64
	MaxConcurrency     int
	EvalTimeout        time.Duration
	Deterministic      bool
}

// MCTSEvaluation captures weighted score components for a branch.
type MCTSEvaluation struct {
	Confidence           float64
	Candidate            string
	Reason               string
	EvidenceScore        float64
	ContradictionPenalty float64
	Prior                float64
	Terminal             bool
}

// MCTSCallbacks provides external model/tool hooks used by search.
type MCTSCallbacks struct {
	ProposeBranches func(ctx context.Context, currentAnswer string, branchCount int) ([]string, error)
	EvaluatePath    func(ctx context.Context, candidate string) (MCTSEvaluation, error)
	AdversarialEval func(ctx context.Context, candidate string) (MCTSEvaluation, error)
}

// MCTSEngine runs branch search and backpropagation to choose a winning path.
type MCTSEngine struct {
	Config    MCTSConfig
	Callbacks MCTSCallbacks
}

type MCTSResult struct {
	BestAnswer    string
	Confidence    float64
	Root          *ThoughtNode
	IterationsRun int
	ExpandedNodes int
	PrunedNodes   int
	Strategy      string
	ElapsedMS     int64
}

type nodeEvalResult struct {
	node       *ThoughtNode
	score      float64
	candidate  string
	confidence float64
	prior      float64
	pruned     bool
	err        error
}

// Search executes MCTS and returns compatibility fields for existing callers.
func (e *MCTSEngine) Search(ctx context.Context, draftAnswer string) (string, bool, *ThoughtNode, error) {
	res, err := e.SearchV2(ctx, draftAnswer)
	if err != nil {
		return "", false, nil, err
	}
	if strings.TrimSpace(res.BestAnswer) == "" {
		return "", false, res.Root, nil
	}
	return strings.TrimSpace(res.BestAnswer), true, res.Root, nil
}

// SearchV2 executes MCTS over potential logic paths and returns structured run metadata.
func (e *MCTSEngine) SearchV2(ctx context.Context, draftAnswer string) (MCTSResult, error) {
	if strings.TrimSpace(draftAnswer) == "" {
		draftAnswer = "No draft answer yet."
	}
	cfg := e.normalizedConfig()
	if e.Callbacks.ProposeBranches == nil || e.Callbacks.EvaluatePath == nil || e.Callbacks.AdversarialEval == nil {
		return MCTSResult{}, fmt.Errorf("mcts engine requires ProposeBranches, EvaluatePath, and AdversarialEval callbacks")
	}
	if cfg.Deterministic {
		cfg.MaxConcurrency = 1
	}
	rng := rand.New(rand.NewSource(cfg.Seed))
	root := &ThoughtNode{ID: "root", Answer: strings.TrimSpace(draftAnswer), Depth: 0, Prior: 1.0}

	bestAnswer := strings.TrimSpace(draftAnswer)
	bestScore := 0.0
	iterationsRun := 0
	expandedNodes := 0
	prunedNodes := 0
	started := time.Now()
	var treeMu sync.Mutex

	for iterationsRun < cfg.Iterations {
		if ctx.Err() != nil {
			break
		}
		batch := minIntLocal(cfg.MaxConcurrency, cfg.Iterations-iterationsRun)
		selected := make([]*ThoughtNode, 0, batch)
		treeMu.Lock()
		for i := 0; i < batch; i++ {
			node, expanded := e.selectAndMaybeExpand(ctx, root, cfg, rng)
			if node == nil {
				break
			}
			if expanded {
				expandedNodes++
			}
			node.VirtualVisits += int(math.Ceil(maxFloatLocal(cfg.VirtualLoss, 1.0)))
			selected = append(selected, node)
		}
		treeMu.Unlock()
		if len(selected) == 0 {
			break
		}

		results := make(chan nodeEvalResult, len(selected))
		var wg sync.WaitGroup
		for _, node := range selected {
			n := node
			wg.Add(1)
			go func() {
				defer wg.Done()
				evalCtx := ctx
				cancel := func() {}
				if cfg.EvalTimeout > 0 {
					evalCtx, cancel = context.WithTimeout(ctx, cfg.EvalTimeout)
				}
				defer cancel()
				results <- e.evaluateNode(evalCtx, n, cfg)
			}()
		}
		wg.Wait()
		close(results)

		treeMu.Lock()
		for r := range results {
			iterationsRun++
			if r.node == nil {
				continue
			}
			if r.node.VirtualVisits > 0 {
				r.node.VirtualVisits--
			}
			if r.err != nil {
				r.node.LastEvalErr = strings.TrimSpace(r.err.Error())
				continue
			}
			r.node.Score = r.score
			r.node.Confidence = r.confidence
			if r.prior > 0 {
				r.node.Prior = clamp01Local(r.prior)
			}
			if r.pruned {
				if !r.node.Pruned {
					prunedNodes++
				}
				r.node.Pruned = true
				r.node.PruneReason = fmt.Sprintf("kill-switch: branch score %.2f < %.2f", r.score, cfg.PruneThreshold)
			}
			e.backpropagate(r.node, r.score)
			candidate := strings.TrimSpace(r.candidate)
			if candidate == "" {
				candidate = strings.TrimSpace(r.node.Answer)
			}
			if r.score > bestScore && candidate != "" {
				bestScore = r.score
				bestAnswer = candidate
			}
		}
		treeMu.Unlock()
	}

	if strings.TrimSpace(bestAnswer) == "" {
		bestAnswer = strings.TrimSpace(root.Answer)
	}
	return MCTSResult{
		BestAnswer:    strings.TrimSpace(bestAnswer),
		Confidence:    clamp01Local(bestScore),
		Root:          root,
		IterationsRun: iterationsRun,
		ExpandedNodes: expandedNodes,
		PrunedNodes:   prunedNodes,
		Strategy:      string(cfg.Strategy),
		ElapsedMS:     time.Since(started).Milliseconds(),
	}, nil
}

func (e *MCTSEngine) normalizedConfig() MCTSConfig {
	cfg := e.Config
	if cfg.Iterations <= 0 {
		cfg.Iterations = 5
	}
	if cfg.BranchFactor <= 0 {
		cfg.BranchFactor = 3
	}
	if cfg.BranchFactor < 2 {
		cfg.BranchFactor = 2
	}
	if cfg.BranchFactor > 8 {
		cfg.BranchFactor = 8
	}
	if cfg.RolloutDepth <= 0 {
		cfg.RolloutDepth = 2
	}
	if cfg.UCB1C <= 0 {
		cfg.UCB1C = defaultUCB1C
	}
	if cfg.PruneThreshold <= 0 {
		cfg.PruneThreshold = defaultPruneThreshold
	}
	if cfg.BaseWeight < 0 {
		cfg.BaseWeight = 0
	}
	if cfg.AdvWeight < 0 {
		cfg.AdvWeight = 0
	}
	if cfg.EvidenceWeight < 0 {
		cfg.EvidenceWeight = 0
	}
	if cfg.ContraWeight < 0 {
		cfg.ContraWeight = 0
	}
	if cfg.BaseWeight == 0 && cfg.AdvWeight == 0 && cfg.EvidenceWeight == 0 && cfg.ContraWeight == 0 {
		cfg.BaseWeight = 0.45
		cfg.AdvWeight = 0.30
		cfg.EvidenceWeight = 0.15
		cfg.ContraWeight = 0.10
	}
	sum := cfg.BaseWeight + cfg.AdvWeight + cfg.EvidenceWeight + cfg.ContraWeight
	if sum > 0 {
		cfg.BaseWeight /= sum
		cfg.AdvWeight /= sum
		cfg.EvidenceWeight /= sum
		cfg.ContraWeight /= sum
	}
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}
	if cfg.Strategy == "" {
		cfg.Strategy = MCTSStrategyPUCT
	}
	if cfg.MaxChildrenPerNode <= 0 {
		cfg.MaxChildrenPerNode = cfg.BranchFactor
	}
	if cfg.WideningAlpha <= 0 {
		cfg.WideningAlpha = 0.5
	}
	if cfg.WideningK <= 0 {
		cfg.WideningK = 1.5
	}
	if cfg.PriorWeight <= 0 {
		cfg.PriorWeight = 1.25
	}
	if cfg.VirtualLoss <= 0 {
		cfg.VirtualLoss = 0.2
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	if cfg.MaxConcurrency > 12 {
		cfg.MaxConcurrency = 12
	}
	if cfg.EvalTimeout <= 0 {
		cfg.EvalTimeout = 8 * time.Second
	}
	return cfg
}

func (e *MCTSEngine) selectAndMaybeExpand(ctx context.Context, root *ThoughtNode, cfg MCTSConfig, rng *rand.Rand) (*ThoughtNode, bool) {
	node := root
	expanded := false
	for node != nil && node.Depth < cfg.RolloutDepth {
		children := unprunedChildren(node.Children)
		if shouldExpand(node, cfg) {
			if e.expandOneChild(ctx, node, cfg) {
				expanded = true
				children = unprunedChildren(node.Children)
			}
		}
		if len(children) == 0 {
			return node, expanded
		}
		var unvisited []*ThoughtNode
		for _, c := range children {
			if c.Visits == 0 {
				unvisited = append(unvisited, c)
			}
		}
		if len(unvisited) > 0 {
			node = unvisited[rng.Intn(len(unvisited))]
			continue
		}
		best := children[0]
		bestScore := e.selectionScore(best, node, cfg)
		for _, c := range children[1:] {
			s := e.selectionScore(c, node, cfg)
			if s > bestScore {
				best = c
				bestScore = s
			}
		}
		node = best
	}
	return node, expanded
}

func shouldExpand(node *ThoughtNode, cfg MCTSConfig) bool {
	if node == nil || node.Pruned || node.Depth >= cfg.RolloutDepth {
		return false
	}
	visits := maxIntLocal(node.Visits+node.VirtualVisits, 1)
	allowed := int(math.Floor(cfg.WideningK * math.Pow(float64(visits), cfg.WideningAlpha)))
	if allowed < 1 {
		allowed = 1
	}
	if allowed > cfg.MaxChildrenPerNode {
		allowed = cfg.MaxChildrenPerNode
	}
	return len(node.Children) < allowed
}

func (e *MCTSEngine) expandOneChild(ctx context.Context, node *ThoughtNode, cfg MCTSConfig) bool {
	if node == nil || node.Pruned {
		return false
	}
	branches, err := e.Callbacks.ProposeBranches(ctx, node.Answer, cfg.BranchFactor)
	if err != nil || len(branches) == 0 {
		return false
	}
	existing := map[string]bool{}
	for _, c := range node.Children {
		existing[strings.ToLower(strings.TrimSpace(c.Answer))] = true
	}
	for _, b := range branches {
		ans := strings.TrimSpace(b)
		if ans == "" {
			continue
		}
		k := strings.ToLower(ans)
		if existing[k] {
			continue
		}
		idx := len(node.Children) + 1
		prior := 1.0 / float64(maxIntLocal(cfg.BranchFactor, 1))
		node.Children = append(node.Children, &ThoughtNode{
			ID:     fmt.Sprintf("%s.%d", node.IDOrDefault(), idx),
			Answer: ans,
			Depth:  node.Depth + 1,
			Parent: node,
			Prior:  prior,
		})
		node.ChildrenExpanded++
		return true
	}
	return false
}

func (e *MCTSEngine) selectionScore(child *ThoughtNode, parent *ThoughtNode, cfg MCTSConfig) float64 {
	if child == nil || child.Pruned {
		return math.Inf(-1)
	}
	if child.Visits == 0 {
		return math.Inf(1)
	}
	switch cfg.Strategy {
	case MCTSStrategyUCB1:
		return ucb(child, maxIntLocal(parent.Visits, 1), cfg.UCB1C)
	default:
		q := child.AverageValue()
		p := child.Prior
		if p <= 0 {
			p = 1.0 / float64(maxIntLocal(len(parent.Children), 1))
		}
		n := float64(maxIntLocal(parent.Visits+parent.VirtualVisits, 1))
		return q + (cfg.PriorWeight * p * math.Sqrt(n) / float64(1+child.Visits+child.VirtualVisits))
	}
}

func (e *MCTSEngine) evaluateNode(ctx context.Context, node *ThoughtNode, cfg MCTSConfig) nodeEvalResult {
	if node == nil || node.Pruned {
		return nodeEvalResult{node: node, err: fmt.Errorf("node unavailable")}
	}
	base, err := e.Callbacks.EvaluatePath(ctx, node.Answer)
	if err != nil {
		return nodeEvalResult{node: node, err: err}
	}
	adv, err := e.Callbacks.AdversarialEval(ctx, node.Answer)
	if err != nil {
		return nodeEvalResult{node: node, err: err}
	}

	baseConf := clamp01Local(base.Confidence)
	advConf := clamp01Local(adv.Confidence)
	evidence := clamp01Local(maxFloatLocal(base.EvidenceScore, adv.EvidenceScore))
	contra := clamp01Local(maxFloatLocal(base.ContradictionPenalty, adv.ContradictionPenalty))
	weighted := clamp01Local((baseConf * cfg.BaseWeight) + (advConf * cfg.AdvWeight) + (evidence * cfg.EvidenceWeight) - (contra * cfg.ContraWeight))
	candidate := strings.TrimSpace(base.Candidate)
	if candidate == "" {
		candidate = strings.TrimSpace(adv.Candidate)
	}
	if candidate == "" {
		candidate = strings.TrimSpace(node.Answer)
	}
	prior := maxFloatLocal(base.Prior, adv.Prior)
	if prior <= 0 {
		prior = node.Prior
	}
	return nodeEvalResult{
		node:       node,
		score:      weighted,
		candidate:  candidate,
		confidence: weighted,
		prior:      prior,
		pruned:     weighted < cfg.PruneThreshold,
	}
}

func (e *MCTSEngine) backpropagate(node *ThoughtNode, score float64) {
	for n := node; n != nil; n = n.Parent {
		if n.VirtualVisits > 0 {
			n.VirtualVisits--
		}
		n.Visits++
		n.ValueSum += score
		n.Confidence = clamp01Local(maxFloatLocal(n.Confidence, score))
	}
}

func (n *ThoughtNode) IDOrDefault() string {
	if n == nil || strings.TrimSpace(n.ID) == "" {
		return "node"
	}
	return strings.TrimSpace(n.ID)
}

func (n *ThoughtNode) AverageValue() float64 {
	if n == nil || n.Visits == 0 {
		return 0
	}
	return n.ValueSum / float64(n.Visits)
}

func unprunedChildren(in []*ThoughtNode) []*ThoughtNode {
	out := make([]*ThoughtNode, 0, len(in))
	for _, c := range in {
		if c == nil || c.Pruned {
			continue
		}
		out = append(out, c)
	}
	return out
}

func ucb(node *ThoughtNode, parentVisits int, c float64) float64 {
	if node == nil || node.Pruned {
		return math.Inf(-1)
	}
	if node.Visits == 0 {
		return math.Inf(1)
	}
	avg := node.ValueSum / float64(node.Visits)
	explore := c * math.Sqrt(math.Log(float64(maxIntLocal(parentVisits, 1)))/float64(node.Visits))
	return avg + explore
}

func clamp01Local(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntLocal(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntLocal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxFloatLocal(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
