package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMissingTimeGap  = 6 * time.Hour
	defaultImpossibleGap   = 90 * time.Minute
	defaultChronosAlertLog = ".memory/chronos_anomaly_alerts.jsonl"
	defaultMirrorBriefLog  = ".memory/reasoning_mirror_briefs.jsonl"
)

// ScoutFinding is a normalized evidence unit from scout/documentary pipelines.
type ScoutFinding struct {
	ID         string            `json:"id"`
	SourcePath string            `json:"source_path"`
	Summary    string            `json:"summary"`
	Actor      string            `json:"actor,omitempty"`
	Location   string            `json:"location,omitempty"`
	EventType  string            `json:"event_type,omitempty"`
	DocumentID string            `json:"document_id,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
	Confidence float64           `json:"confidence"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// TimelineEvent is one reified event in chronological reasoning space.
type TimelineEvent struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Actor       string    `json:"actor,omitempty"`
	Location    string    `json:"location,omitempty"`
	EventType   string    `json:"event_type,omitempty"`
	DocumentID  string    `json:"document_id,omitempty"`
	Summary     string    `json:"summary"`
	Confidence  float64   `json:"confidence"`
	SourcePath  string    `json:"source_path,omitempty"`
	SourceID    string    `json:"source_id,omitempty"`
	IsEstimated bool      `json:"is_estimated,omitempty"`
}

// TimelineEdge links events with temporal/causal relation labels.
type TimelineEdge struct {
	FromID   string    `json:"from_id"`
	ToID     string    `json:"to_id"`
	Relation string    `json:"relation"`
	Gap      float64   `json:"gap_hours,omitempty"`
	Weight   float64   `json:"weight"`
	Created  time.Time `json:"created_at"`
}

// TimelineAnomaly captures temporal impossibilities and missing-time segments.
type TimelineAnomaly struct {
	Type          string  `json:"type"` // missing_time | impossible_sequel
	Actor         string  `json:"actor,omitempty"`
	FromEventID   string  `json:"from_event_id,omitempty"`
	ToEventID     string  `json:"to_event_id,omitempty"`
	DurationHours float64 `json:"duration_hours,omitempty"`
	Reason        string  `json:"reason"`
	Severity      float64 `json:"severity"`
}

// TimelineGraph is the ordered timeline output for forensic analysis.
type TimelineGraph struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Events      []TimelineEvent   `json:"events"`
	Edges       []TimelineEdge    `json:"edges"`
	Anomalies   []TimelineAnomaly `json:"anomalies,omitempty"`
}

// TimelineNode is a compact temporal-causal structure for direct sequence verification.
// Core fields required by spec: Timestamp, SourceID, Entity, Action.
type TimelineNode struct {
	Timestamp        time.Time         `json:"timestamp"`
	SourceID         string            `json:"source_id"`
	Entity           string            `json:"entity"`
	Action           string            `json:"action"`
	Location         string            `json:"location,omitempty"`
	DocumentID       string            `json:"document_id,omitempty"`
	MentionedEventTS []time.Time       `json:"mentioned_event_timestamps,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Confidence       float64           `json:"confidence,omitempty"`
}

// CausalViolation flags impossible temporal states and sequencing issues.
type CausalViolation struct {
	Type     string    `json:"type"` // simultaneous_presence | impossible_sequel | metadata_predates_content
	Entity   string    `json:"entity,omitempty"`
	SourceA  string    `json:"source_a,omitempty"`
	SourceB  string    `json:"source_b,omitempty"`
	When     time.Time `json:"when,omitempty"`
	Reason   string    `json:"reason"`
	Severity float64   `json:"severity"`
}

// TemporalConflict captures contradictory event claims between sources.
type TemporalConflict struct {
	NodeA  TimelineNode `json:"node_a"`
	NodeB  TimelineNode `json:"node_b"`
	Score  float64      `json:"score"`
	Reason string       `json:"reason"`
}

// VeracityScore is the source-level trust score after MCTS arbitration.
type VeracityScore struct {
	SourceID string  `json:"source_id"`
	Score    float64 `json:"score"`
}

// ChronosAnomalyAlert is emitted to Reasoning Mirror when violations are detected.
type ChronosAnomalyAlert struct {
	Timestamp    time.Time         `json:"timestamp"`
	Message      string            `json:"message"`
	Violations   []CausalViolation `json:"violations,omitempty"`
	TopLieSource string            `json:"top_lie_source,omitempty"`
	TopLieScore  float64           `json:"top_lie_score,omitempty"`
}

// TemporalAnalysisResult bundles consistency + lie detection outcomes.
type TemporalAnalysisResult struct {
	Violations   []CausalViolation  `json:"violations"`
	Conflicts    []TemporalConflict `json:"conflicts"`
	Veracity     []VeracityScore    `json:"veracity"`
	TopLieSource string             `json:"top_lie_source,omitempty"`
	TopLieScore  float64            `json:"top_lie_score,omitempty"`
}

// EvidenceConflict stores pairwise contradictions among findings.
type EvidenceConflict struct {
	FindingA      ScoutFinding `json:"finding_a"`
	FindingB      ScoutFinding `json:"finding_b"`
	Score         float64      `json:"score"`
	ConflictType  string       `json:"conflict_type"`
	ConflictClaim string       `json:"conflict_claim"`
}

// ForgeryHypothesis is one candidate branch in epistemic conflict resolution.
type ForgeryHypothesis struct {
	FindingID      string  `json:"finding_id"`
	Likelihood     float64 `json:"likelihood"`
	SupportScore   float64 `json:"support_score"`
	ConflictScore  float64 `json:"conflict_score"`
	ContextPenalty float64 `json:"context_penalty"`
}

// EpistemicConflictResolution is the weighted outcome of conflict arbitration.
type EpistemicConflictResolution struct {
	Conflicts         []EvidenceConflict  `json:"conflicts"`
	Hypotheses        []ForgeryHypothesis `json:"hypotheses"`
	MostLikelyForgery string              `json:"most_likely_forgery,omitempty"`
	WinningScore      float64             `json:"winning_score,omitempty"`
	Rationale         string              `json:"rationale"`
	AlternativeIDs    []string            `json:"alternative_ids,omitempty"`
	EngineRoot        *ThoughtNode        `json:"-"`
}

// ReifyTimeline transforms scout findings into an ordered timeline graph.
func ReifyTimeline(findings []ScoutFinding) TimelineGraph {
	events := make([]TimelineEvent, 0, len(findings))
	for i, f := range findings {
		ts := f.Timestamp
		if ts.IsZero() {
			ts = chronosParseTS(f.Metadata["timestamp"])
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		id := strings.TrimSpace(f.ID)
		if id == "" {
			id = fmt.Sprintf("finding_%d", i+1)
		}
		conf := f.Confidence
		if conf <= 0 {
			conf = 0.58
		}
		events = append(events, TimelineEvent{
			ID:          "evt_" + sanitizeChronosID(id),
			Timestamp:   ts.UTC(),
			Actor:       strings.TrimSpace(f.Actor),
			Location:    strings.TrimSpace(f.Location),
			EventType:   strings.TrimSpace(f.EventType),
			DocumentID:  strings.TrimSpace(f.DocumentID),
			Summary:     strings.TrimSpace(f.Summary),
			Confidence:  chronosClamp01(conf),
			SourcePath:  strings.TrimSpace(f.SourcePath),
			SourceID:    id,
			IsEstimated: f.Timestamp.IsZero(),
		})
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	edges := make([]TimelineEdge, 0, maxIntChronos(0, len(events)-1))
	for i := 0; i < len(events)-1; i++ {
		gapH := events[i+1].Timestamp.Sub(events[i].Timestamp).Hours()
		relation := "happens_before"
		if events[i].Actor != "" && events[i].Actor == events[i+1].Actor {
			relation = "same_actor_sequence"
		}
		edges = append(edges, TimelineEdge{
			FromID:   events[i].ID,
			ToID:     events[i+1].ID,
			Relation: relation,
			Gap:      gapH,
			Weight:   chronosClamp01((events[i].Confidence + events[i+1].Confidence) / 2.0),
			Created:  time.Now().UTC(),
		})
	}

	graph := TimelineGraph{
		GeneratedAt: time.Now().UTC(),
		Events:      events,
		Edges:       edges,
	}
	graph.Anomalies = DetectCausalGaps(graph)
	return graph
}

// BuildTimelineNodes projects scout findings into causal nodes for VerifyConsistency.
func BuildTimelineNodes(findings []ScoutFinding) []TimelineNode {
	nodes := make([]TimelineNode, 0, len(findings))
	for _, f := range findings {
		ts := f.Timestamp
		if ts.IsZero() {
			ts = chronosParseTS(f.Metadata["timestamp"])
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		entity := strings.TrimSpace(f.Actor)
		if entity == "" {
			entity = strings.TrimSpace(f.Metadata["entity"])
		}
		if entity == "" {
			entity = "unknown_entity"
		}
		action := strings.TrimSpace(f.Summary)
		if action == "" {
			action = strings.TrimSpace(f.EventType)
		}
		sourceID := strings.TrimSpace(f.ID)
		if sourceID == "" {
			sourceID = sanitizeChronosID(f.SourcePath)
		}
		nodes = append(nodes, TimelineNode{
			Timestamp:        ts.UTC(),
			SourceID:         sourceID,
			Entity:           entity,
			Action:           action,
			Location:         strings.TrimSpace(f.Location),
			DocumentID:       strings.TrimSpace(f.DocumentID),
			MentionedEventTS: parseMentionedTS(f.Metadata),
			Metadata:         f.Metadata,
			Confidence:       chronosClamp01(nonZeroChronos(f.Confidence, 0.62)),
		})
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Timestamp.Before(nodes[j].Timestamp) })
	return nodes
}

// DetectCausalGaps finds missing-time spans and impossible sequels in timeline order.
func DetectCausalGaps(graph TimelineGraph) []TimelineAnomaly {
	if len(graph.Events) < 2 {
		return nil
	}
	events := append([]TimelineEvent(nil), graph.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })

	var out []TimelineAnomaly
	byActor := map[string][]TimelineEvent{}
	for _, e := range events {
		if strings.TrimSpace(e.Actor) == "" {
			continue
		}
		actor := strings.ToLower(strings.TrimSpace(e.Actor))
		byActor[actor] = append(byActor[actor], e)
	}

	for actor, seq := range byActor {
		sort.SliceStable(seq, func(i, j int) bool { return seq[i].Timestamp.Before(seq[j].Timestamp) })
		for i := 0; i < len(seq)-1; i++ {
			a, b := seq[i], seq[i+1]
			dt := b.Timestamp.Sub(a.Timestamp)
			if dt <= 0 {
				continue
			}
			if dt >= defaultMissingTimeGap {
				out = append(out, TimelineAnomaly{
					Type:          "missing_time",
					Actor:         a.Actor,
					FromEventID:   a.ID,
					ToEventID:     b.ID,
					DurationHours: dt.Hours(),
					Reason:        fmt.Sprintf("missing timeline coverage for %s between %s and %s", actor, a.Timestamp.Format(time.RFC3339), b.Timestamp.Format(time.RFC3339)),
					Severity:      chronosClamp01(dt.Hours() / 24.0),
				})
			}

			la := strings.ToLower(strings.TrimSpace(a.Location))
			lb := strings.ToLower(strings.TrimSpace(b.Location))
			if la == "" || lb == "" || la == lb {
				continue
			}
			minTravel := estimateTravelDuration(la, lb)
			if dt < minTravel {
				out = append(out, TimelineAnomaly{
					Type:          "impossible_sequel",
					Actor:         a.Actor,
					FromEventID:   a.ID,
					ToEventID:     b.ID,
					DurationHours: dt.Hours(),
					Reason:        fmt.Sprintf("impossible sequel: %s at %s then %s within %.1f min (< %.1f min travel)", actor, a.Location, b.Location, dt.Minutes(), minTravel.Minutes()),
					Severity:      chronosClamp01((minTravel.Hours() - dt.Hours()) / minTravel.Hours()),
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity > out[j].Severity })
	return out
}

// VerifyConsistency flags temporal causal violations in timeline nodes.
func VerifyConsistency(nodes []TimelineNode) []CausalViolation {
	if len(nodes) == 0 {
		return nil
	}
	ordered := append([]TimelineNode(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })

	var out []CausalViolation
	byEntity := map[string][]TimelineNode{}
	for _, n := range ordered {
		entity := strings.ToLower(strings.TrimSpace(n.Entity))
		if entity == "" {
			entity = "unknown_entity"
		}
		byEntity[entity] = append(byEntity[entity], n)
		if len(n.MentionedEventTS) > 0 {
			earliest := n.MentionedEventTS[0]
			for _, t := range n.MentionedEventTS[1:] {
				if t.Before(earliest) {
					earliest = t
				}
			}
			if n.Timestamp.Before(earliest) {
				out = append(out, CausalViolation{
					Type:     "metadata_predates_content",
					Entity:   n.Entity,
					SourceA:  n.SourceID,
					When:     n.Timestamp,
					Reason:   fmt.Sprintf("document timestamp %s predates mentioned event %s", n.Timestamp.Format(time.RFC3339), earliest.Format(time.RFC3339)),
					Severity: 0.86,
				})
			}
		}
	}

	for _, seq := range byEntity {
		sort.SliceStable(seq, func(i, j int) bool { return seq[i].Timestamp.Before(seq[j].Timestamp) })
		for i := 0; i < len(seq)-1; i++ {
			a, b := seq[i], seq[i+1]
			dt := b.Timestamp.Sub(a.Timestamp)
			la := strings.ToLower(strings.TrimSpace(a.Location))
			lb := strings.ToLower(strings.TrimSpace(b.Location))
			if la != "" && lb != "" && la != lb {
				if dt <= 30*time.Minute {
					out = append(out, CausalViolation{
						Type:     "simultaneous_presence",
						Entity:   a.Entity,
						SourceA:  a.SourceID,
						SourceB:  b.SourceID,
						When:     a.Timestamp,
						Reason:   fmt.Sprintf("%s appears in %s and %s within %d minutes", a.Entity, a.Location, b.Location, int(dt.Minutes())),
						Severity: 0.90,
					})
				}
				travel := estimateTravelDuration(la, lb)
				if dt > 0 && dt < travel {
					out = append(out, CausalViolation{
						Type:     "impossible_sequel",
						Entity:   a.Entity,
						SourceA:  a.SourceID,
						SourceB:  b.SourceID,
						When:     b.Timestamp,
						Reason:   fmt.Sprintf("impossible sequel: travel %s->%s needs ~%.1f min but only %.1f min available", a.Location, b.Location, travel.Minutes(), dt.Minutes()),
						Severity: chronosClamp01((travel.Hours()-dt.Hours())/travel.Hours() + 0.55),
					})
				}
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity > out[j].Severity })
	return out
}

// ResolveEpistemicConflicts weighs conflicting evidence and identifies likely forgery candidates.
// archivistContext should include historical decision traces from glm-archived.
func ResolveEpistemicConflicts(findings []ScoutFinding, archivistContext []string) EpistemicConflictResolution {
	conflicts := detectEvidenceConflicts(findings)
	if len(conflicts) == 0 {
		return EpistemicConflictResolution{
			Conflicts:  nil,
			Hypotheses: nil,
			Rationale:  "no significant contradictions detected in findings",
		}
	}

	hypMap := map[string]*ForgeryHypothesis{}
	addHyp := func(f ScoutFinding, support, conflict, penalty float64) {
		id := strings.TrimSpace(f.ID)
		if id == "" {
			id = sanitizeChronosID(f.SourcePath + "_" + f.Summary)
		}
		h, ok := hypMap[id]
		if !ok {
			h = &ForgeryHypothesis{FindingID: id}
			hypMap[id] = h
		}
		h.SupportScore += support
		h.ConflictScore += conflict
		h.ContextPenalty += penalty
	}

	for _, c := range conflicts {
		aPen := contextPenalty(c.FindingA, archivistContext)
		bPen := contextPenalty(c.FindingB, archivistContext)
		aSupport := sourceReliability(c.FindingA)
		bSupport := sourceReliability(c.FindingB)
		addHyp(c.FindingA, aSupport, c.Score, aPen)
		addHyp(c.FindingB, bSupport, c.Score, bPen)
	}

	var hyps []ForgeryHypothesis
	for _, h := range hypMap {
		// Higher conflict and context-penalty with lower support => more likely forgery.
		raw := (h.ConflictScore * 0.45) + (h.ContextPenalty * 0.35) + ((1.0 - chronosClamp01(h.SupportScore)) * 0.20)
		h.Likelihood = chronosClamp01(raw)
		hyps = append(hyps, *h)
	}
	sort.SliceStable(hyps, func(i, j int) bool { return hyps[i].Likelihood > hyps[j].Likelihood })

	bestID := ""
	bestScore := 0.0
	var alternatives []string
	if len(hyps) > 0 {
		bestID = hyps[0].FindingID
		bestScore = hyps[0].Likelihood
		for i := 1; i < len(hyps) && i < 6; i++ {
			alternatives = append(alternatives, hyps[i].FindingID)
		}
	}

	// Run a deterministic MCTS arbitration pass over hypotheses (no external model calls).
	root := runChronosMCTS(hyps)
	if root != nil && len(root.Children) > 0 {
		bestChild := root.Children[0]
		for _, c := range root.Children[1:] {
			if c != nil && c.Confidence > bestChild.Confidence {
				bestChild = c
			}
		}
		if bestChild != nil {
			if id := strings.TrimSpace(bestChild.Answer); id != "" {
				bestID = id
				bestScore = chronosClamp01(bestChild.Confidence)
			}
		}
	}

	rationale := "MCTS weighted contradiction severity, archivist inconsistency, and source reliability."
	if bestID != "" {
		rationale += " Most likely forged/tainted evidence: " + bestID + "."
	}
	return EpistemicConflictResolution{
		Conflicts:         conflicts,
		Hypotheses:        hyps,
		MostLikelyForgery: bestID,
		WinningScore:      bestScore,
		Rationale:         rationale,
		AlternativeIDs:    alternatives,
		EngineRoot:        root,
	}
}

// AnalyzeTemporalCausality performs full Chronos pass and emits mirror anomaly alerts if needed.
func AnalyzeTemporalCausality(nodes []TimelineNode, archivistReliability map[string]float64) TemporalAnalysisResult {
	violations := VerifyConsistency(nodes)
	conflicts := detectTemporalConflicts(nodes)
	veracity := AssignVeracityScores(conflicts, archivistReliability)

	topLie := ""
	topLieScore := 0.0
	for _, v := range veracity {
		suspect := 1.0 - chronosClamp01(v.Score)
		if suspect > topLieScore {
			topLieScore = suspect
			topLie = v.SourceID
		}
	}
	res := TemporalAnalysisResult{
		Violations:   violations,
		Conflicts:    conflicts,
		Veracity:     veracity,
		TopLieSource: topLie,
		TopLieScore:  topLieScore,
	}
	if len(violations) > 0 || topLieScore >= 0.72 {
		msg := "Chronos anomaly alert: temporal inconsistency detected."
		if topLie != "" {
			msg = fmt.Sprintf("Chronos anomaly alert: source %s is likely inconsistent (%.0f%%).", topLie, topLieScore*100)
		}
		_ = EmitAnomalyAlert(ChronosAnomalyAlert{
			Timestamp:    time.Now().UTC(),
			Message:      msg,
			Violations:   violations,
			TopLieSource: topLie,
			TopLieScore:  topLieScore,
		})
	}
	return res
}

// AssignVeracityScores applies MCTS weighting to conflicting events and source reliability priors.
func AssignVeracityScores(conflicts []TemporalConflict, archivistReliability map[string]float64) []VeracityScore {
	if len(conflicts) == 0 {
		return nil
	}
	sourceStats := map[string]float64{}
	for _, c := range conflicts {
		a := strings.TrimSpace(c.NodeA.SourceID)
		b := strings.TrimSpace(c.NodeB.SourceID)
		if a != "" {
			sourceStats[a] += c.Score
		}
		if b != "" {
			sourceStats[b] += c.Score
		}
	}
	var hyps []ForgeryHypothesis
	for src, conflictWeight := range sourceStats {
		prior := chronosClamp01(archivistReliability[src])
		if prior == 0 {
			prior = 0.55
		}
		hyps = append(hyps, ForgeryHypothesis{
			FindingID:      src,
			ConflictScore:  chronosClamp01(conflictWeight / maxFloatChronos(float64(len(conflicts)), 1)),
			SupportScore:   prior,
			ContextPenalty: chronosClamp01(1.0 - prior),
			Likelihood:     chronosClamp01((1.0-prior)*0.6 + (conflictWeight*0.4)/maxFloatChronos(float64(len(conflicts)), 1)),
		})
	}
	root := runChronosMCTS(hyps)
	rootScore := map[string]float64{}
	for _, h := range hyps {
		rootScore[h.FindingID] = chronosClamp01(1.0 - h.Likelihood)
	}
	if root != nil {
		for _, c := range root.Children {
			if c == nil {
				continue
			}
			id := strings.TrimSpace(c.Answer)
			if id == "" {
				continue
			}
			rootScore[id] = chronosClamp01((rootScore[id] * 0.55) + (c.Confidence * 0.45))
		}
	}
	var out []VeracityScore
	for src, score := range rootScore {
		out = append(out, VeracityScore{SourceID: src, Score: chronosClamp01(score)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// EmitAnomalyAlert appends a Chronos alert and mirror brief for narration.
func EmitAnomalyAlert(alert ChronosAnomalyAlert) error {
	if alert.Timestamp.IsZero() {
		alert.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(alert.Message) == "" {
		alert.Message = "Chronos detected a temporal anomaly."
	}
	if err := appendChronosJSONL(defaultChronosAlertLog, alert); err != nil {
		return err
	}
	brief := map[string]interface{}{
		"timestamp": alert.Timestamp.Format(time.RFC3339),
		"source":    "chronos",
		"message":   strings.TrimSpace(alert.Message),
	}
	return appendChronosJSONL(defaultMirrorBriefLog, brief)
}

func detectTemporalConflicts(nodes []TimelineNode) []TemporalConflict {
	if len(nodes) < 2 {
		return nil
	}
	var out []TemporalConflict
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			a, b := nodes[i], nodes[j]
			if !strings.EqualFold(strings.TrimSpace(a.Entity), strings.TrimSpace(b.Entity)) && !strings.EqualFold(strings.TrimSpace(a.DocumentID), strings.TrimSpace(b.DocumentID)) {
				continue
			}
			claimA := fmt.Sprintf("entity=%s location=%s action=%s time=%s source=%s", a.Entity, a.Location, a.Action, a.Timestamp.Format(time.RFC3339), a.SourceID)
			claimB := fmt.Sprintf("entity=%s location=%s action=%s time=%s source=%s", b.Entity, b.Location, b.Action, b.Timestamp.Format(time.RFC3339), b.SourceID)
			score := DetectContradiction(claimA, claimB)
			if score < 0.55 {
				continue
			}
			reason := "conflicting event claims"
			if strings.TrimSpace(a.Location) != "" && strings.TrimSpace(b.Location) != "" && !strings.EqualFold(strings.TrimSpace(a.Location), strings.TrimSpace(b.Location)) {
				reason = "same entity appears in incompatible locations"
			}
			out = append(out, TemporalConflict{
				NodeA:  a,
				NodeB:  b,
				Score:  chronosClamp01(score),
				Reason: reason,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func parseMentionedTS(meta map[string]string) []time.Time {
	if len(meta) == 0 {
		return nil
	}
	raw := strings.TrimSpace(meta["mentioned_event_ts"])
	if raw == "" {
		raw = strings.TrimSpace(meta["mentioned_timestamps"])
	}
	if raw == "" {
		return nil
	}
	var out []time.Time
	for _, part := range strings.Split(raw, ",") {
		t := chronosParseTS(strings.TrimSpace(part))
		if !t.IsZero() {
			out = append(out, t.UTC())
		}
	}
	return out
}

func detectEvidenceConflicts(findings []ScoutFinding) []EvidenceConflict {
	if len(findings) < 2 {
		return nil
	}
	var out []EvidenceConflict
	for i := 0; i < len(findings); i++ {
		for j := i + 1; j < len(findings); j++ {
			a, b := findings[i], findings[j]
			if !sharesConflictSurface(a, b) {
				continue
			}
			claimA := buildConflictClaim(a)
			claimB := buildConflictClaim(b)
			score := DetectContradiction(claimA, claimB)
			if score < 0.55 {
				continue
			}
			ct := "general_conflict"
			if strings.EqualFold(strings.TrimSpace(a.DocumentID), strings.TrimSpace(b.DocumentID)) && a.DocumentID != "" {
				ct = "document_internal_conflict"
			}
			if sameActor(a, b) && distinctLocations(a, b) {
				ct = "actor_location_conflict"
			}
			out = append(out, EvidenceConflict{
				FindingA:      a,
				FindingB:      b,
				Score:         chronosClamp01(score),
				ConflictType:  ct,
				ConflictClaim: claimA + " <> " + claimB,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func runChronosMCTS(hyps []ForgeryHypothesis) *ThoughtNode {
	if len(hyps) == 0 {
		return nil
	}
	hypByID := map[string]ForgeryHypothesis{}
	for _, h := range hyps {
		hypByID[strings.TrimSpace(h.FindingID)] = h
	}
	engine := MCTSEngine{
		Config: MCTSConfig{
			Iterations:         6,
			BranchFactor:       minIntChronos(maxIntChronos(len(hyps), 3), 5),
			RolloutDepth:       2,
			UCB1C:              1.15,
			PruneThreshold:     0.20,
			Seed:               42,
			Strategy:           MCTSStrategyPUCT,
			MaxChildrenPerNode: 5,
			WideningAlpha:      0.5,
			WideningK:          1.4,
			PriorWeight:        1.10,
			MaxConcurrency:     2,
			Deterministic:      true,
		},
		Callbacks: MCTSCallbacks{
			ProposeBranches: func(_ context.Context, _ string, branchCount int) ([]string, error) {
				var ids []string
				for _, h := range hyps {
					ids = append(ids, strings.TrimSpace(h.FindingID))
					if len(ids) >= branchCount {
						break
					}
				}
				return ids, nil
			},
			EvaluatePath: func(_ context.Context, candidate string) (MCTSEvaluation, error) {
				h, ok := hypByID[strings.TrimSpace(candidate)]
				if !ok {
					return MCTSEvaluation{}, fmt.Errorf("unknown candidate")
				}
				conf := chronosClamp01(h.Likelihood)
				return MCTSEvaluation{
					Confidence: conf,
					Candidate:  strings.TrimSpace(candidate),
					Reason:     "chronos likelihood score",
				}, nil
			},
			AdversarialEval: func(_ context.Context, candidate string) (MCTSEvaluation, error) {
				h, ok := hypByID[strings.TrimSpace(candidate)]
				if !ok {
					return MCTSEvaluation{}, fmt.Errorf("unknown candidate")
				}
				// Adversarial pass penalizes low conflict signal.
				conf := chronosClamp01((h.Likelihood * 0.75) + (h.ConflictScore * 0.25))
				return MCTSEvaluation{
					Confidence: conf,
					Candidate:  strings.TrimSpace(candidate),
					Reason:     "adversarial consistency",
				}, nil
			},
		},
	}
	res, err := engine.SearchV2(context.Background(), "chronos-forgery-arbitration")
	if err != nil {
		return nil
	}
	return res.Root
}

func sharesConflictSurface(a, b ScoutFinding) bool {
	if strings.EqualFold(strings.TrimSpace(a.DocumentID), strings.TrimSpace(b.DocumentID)) && strings.TrimSpace(a.DocumentID) != "" {
		return true
	}
	if sameActor(a, b) {
		return true
	}
	if lexicalOverlapChronos(a.Summary, b.Summary) >= 0.24 {
		return true
	}
	return false
}

func buildConflictClaim(f ScoutFinding) string {
	ts := f.Timestamp
	if ts.IsZero() {
		ts = chronosParseTS(f.Metadata["timestamp"])
	}
	return strings.TrimSpace(fmt.Sprintf(
		"source=%s actor=%s location=%s document=%s event=%s time=%s summary=%s",
		f.SourcePath,
		f.Actor,
		f.Location,
		f.DocumentID,
		f.EventType,
		ts.UTC().Format(time.RFC3339),
		f.Summary,
	))
}

func contextPenalty(f ScoutFinding, ctx []string) float64 {
	if len(ctx) == 0 {
		return 0.25
	}
	claim := buildConflictClaim(f)
	penalty := 0.0
	for _, c := range ctx {
		if strings.TrimSpace(c) == "" {
			continue
		}
		ctr := DetectContradiction(claim, c)
		ov := lexicalOverlapChronos(claim, c)
		penalty = maxFloatChronos(penalty, chronosClamp01((ctr*0.75)+(ov*0.25)))
	}
	return penalty
}

func sourceReliability(f ScoutFinding) float64 {
	p := strings.ToLower(strings.TrimSpace(f.SourcePath + " " + mapToStringChronos(f.Metadata)))
	switch {
	case strings.Contains(p, "live shell") || strings.Contains(p, "stdout") || strings.Contains(p, "stderr"):
		return 0.94
	case strings.Contains(p, "log") || strings.Contains(p, "journal"):
		return 0.90
	case strings.Contains(p, "contract") || strings.Contains(p, "ledger") || strings.Contains(p, "signature"):
		return 0.62
	case strings.Contains(p, "readme") || strings.Contains(p, "summary"):
		return 0.44
	default:
		return 0.58
	}
}

func sameActor(a, b ScoutFinding) bool {
	aa := strings.ToLower(strings.TrimSpace(a.Actor))
	bb := strings.ToLower(strings.TrimSpace(b.Actor))
	return aa != "" && bb != "" && aa == bb
}

func distinctLocations(a, b ScoutFinding) bool {
	la := strings.ToLower(strings.TrimSpace(a.Location))
	lb := strings.ToLower(strings.TrimSpace(b.Location))
	return la != "" && lb != "" && la != lb
}

func estimateTravelDuration(locA, locB string) time.Duration {
	a := normalizeCityChronos(locA)
	b := normalizeCityChronos(locB)
	if a == "" || b == "" || a == b {
		return defaultImpossibleGap
	}
	// Conservative city-to-city defaults for "impossible sequel" checks.
	key := a + "->" + b
	known := map[string]time.Duration{
		"london->new york": 7 * time.Hour,
		"new york->london": 7 * time.Hour,
		"berlin->london":   2 * time.Hour,
		"london->berlin":   2 * time.Hour,
		"paris->london":    90 * time.Minute,
		"london->paris":    90 * time.Minute,
		"tokyo->new york":  13 * time.Hour,
		"new york->tokyo":  13 * time.Hour,
	}
	if d, ok := known[key]; ok {
		return d
	}
	return 3 * time.Hour
}

func normalizeCityChronos(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(",", " ", "-", " ", "_", " ", "  ", " ")
	s = repl.Replace(s)
	switch {
	case strings.Contains(s, "new york"), strings.Contains(s, "nyc"):
		return "new york"
	case strings.Contains(s, "london"):
		return "london"
	case strings.Contains(s, "berlin"):
		return "berlin"
	case strings.Contains(s, "paris"):
		return "paris"
	case strings.Contains(s, "tokyo"):
		return "tokyo"
	default:
		return strings.TrimSpace(s)
	}
}

func lexicalOverlapChronos(a, b string) float64 {
	ta := tokensChronos(a)
	tb := tokensChronos(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	shared := 0
	for _, t := range tb {
		if set[t] {
			shared++
		}
	}
	den := len(ta)
	if len(tb) > den {
		den = len(tb)
	}
	return float64(shared) / float64(den)
}

func tokensChronos(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	rep := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ", "\"", " ")
	s = rep.Replace(s)
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true}
	var out []string
	for _, t := range strings.Fields(s) {
		if len(t) < 3 || stop[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

func sanitizeChronosID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	repl := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ";", "_", ",", "_", ".", "_")
	s = repl.Replace(s)
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func chronosParseTS(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
		// Treat as unix seconds.
		return time.Unix(v, 0).UTC()
	}
	return time.Time{}
}

func mapToStringChronos(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func chronosClamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntChronos(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntChronos(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxFloatChronos(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func appendChronosJSONL(path string, v interface{}) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(string(b)) + "\n")
	return err
}

func nonZeroChronos(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}
