package skills

import (
	"sort"
)

type OverlayPriority string

const (
	OverlayPriorityLow      OverlayPriority = "low"
	OverlayPriorityMedium   OverlayPriority = "medium"
	OverlayPriorityHigh     OverlayPriority = "high"
	OverlayPriorityCritical OverlayPriority = "critical"
)

type OverlayCandidate struct {
	Kind       OverlayKind
	Text       string
	X          int
	Y          int
	W          int
	H          int
	Confidence float64
	Priority   OverlayPriority
	Source     string
	Tags       []string
}

type scoredOverlayCandidate struct {
	Candidate OverlayCandidate
	Score     float64
}

func rankOverlayCandidates(cands []OverlayCandidate) []OverlayCandidate {
	scored := make([]scoredOverlayCandidate, 0, len(cands))
	for _, c := range cands {
		c = normalizeOverlayCandidate(c)
		score := c.Confidence + priorityBoost(c.Priority) + kindBoost(c.Kind)
		scored = append(scored, scoredOverlayCandidate{Candidate: c, Score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Candidate.Kind < scored[j].Candidate.Kind
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]OverlayCandidate, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.Candidate)
	}
	return out
}

func priorityBoost(p OverlayPriority) float64 {
	switch p {
	case OverlayPriorityCritical:
		return 0.40
	case OverlayPriorityHigh:
		return 0.25
	case OverlayPriorityMedium:
		return 0.10
	default:
		return 0
	}
}

func kindBoost(k OverlayKind) float64 {
	switch k {
	case OverlayKindContradiction:
		return 0.18
	case OverlayKindEvidence:
		return 0.12
	case OverlayKindRisk:
		return 0.10
	default:
		return 0
	}
}

func normalizeOverlayCandidate(c OverlayCandidate) OverlayCandidate {
	c.Kind = normalizeOverlayKind(string(c.Kind))
	c.Text = normalizeOverlayLabel(c.Kind, c.Text)
	c.Confidence = clampOverlayFloat(c.Confidence, 0, 1)
	if c.Priority == "" {
		c.Priority = OverlayPriorityMedium
	}
	if c.W <= 0 {
		c.W = 180
	}
	if c.H <= 0 {
		c.H = 96
	}
	if c.X < 0 {
		c.X = 0
	}
	if c.Y < 0 {
		c.Y = 0
	}
	return c
}

func clampOverlayFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
