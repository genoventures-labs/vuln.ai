package state

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultStateFile     = ".memory/session_state.json"
	defaultDecayHalfLife = 6 * time.Hour
	numericBaseline      = 0.5
	numericLowerBound    = 0.0
	numericUpperBound    = 1.0
	toneBiasLowerBound   = -1.0
	toneBiasUpperBound   = 1.0
	moodHistoryLimit     = 15
	moodCarryDecay       = 0.654 // 5-turn-old sentiment ~= 11.9% carry weight
	goalPersistenceKey   = "goalpersistence"
	confidenceKey        = "confidence"
	urgencyKey           = "urgency"
	analyticalModeKey    = "analyticalmode"
	frustrationKey       = "frustration"
	toneBiasKey          = "tonebias"
	moodScoreKey         = "moodscore"
)

// SessionState is the persisted structural state for a running session.
type SessionState struct {
	Confidence     float64 `json:"confidence"`
	Urgency        float64 `json:"urgency"`
	AnalyticalMode float64 `json:"analytical_mode"`
	Frustration    float64 `json:"frustration"`
	// Mood vector: [valence, intensity, resilience], each normalized to [0,1].
	Mood        [3]float64 `json:"mood"`
	ToneBias    float64    `json:"tone_bias"`
	MoodHistory []float64  `json:"mood_history"`
	Subtext     []string   `json:"subtext_markers,omitempty"`

	PrimaryGoal     string    `json:"primary_goal"`
	ActiveNamespace string    `json:"active_namespace,omitempty"`
	GoalPersistence float64   `json:"goal_persistence"`
	LastUpdate      time.Time `json:"last_update"`
}

// Manager provides thread-safe access to session state with persistence and decay.
type Manager struct {
	mu            sync.RWMutex
	state         SessionState
	filePath      string
	decayHalfLife time.Duration
}

// NewManager creates a new state manager and attempts to load persisted state.
func NewManager() (*Manager, error) {
	return NewManagerWithPath(defaultStateFile)
}

// NewManagerWithPath creates a state manager with a custom backing JSON file.
func NewManagerWithPath(path string) (*Manager, error) {
	m := &Manager{
		state:         defaultSessionState(),
		filePath:      path,
		decayHalfLife: defaultDecayHalfLife,
	}

	if err := m.Load(); err != nil {
		return nil, err
	}
	return m, nil
}

// GetSnapshot returns a copy of the current session state.
func (m *Manager) GetSnapshot() SessionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// SetPrimaryGoal updates the primary goal.
func (m *Manager) SetPrimaryGoal(goal string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())
	m.state.PrimaryGoal = strings.TrimSpace(goal)
	m.state.LastUpdate = time.Now().UTC()
}

// SetActiveNamespace updates the persisted active namespace for memory-scoped runtime flows.
func (m *Manager) SetActiveNamespace(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())
	m.state.ActiveNamespace = strings.ToLower(strings.TrimSpace(namespace))
	m.state.LastUpdate = time.Now().UTC()
}

// ActiveNamespace returns the currently persisted active namespace.
func (m *Manager) ActiveNamespace() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return strings.TrimSpace(m.state.ActiveNamespace)
}

// UpdateDelta adjusts numeric values and keeps them bounded.
//
// Accepted keys (case-insensitive, punctuation ignored):
// confidence, urgency, analytical_mode, frustration, goal_persistence, tone_bias, mood_score.
func (m *Manager) UpdateDelta(delta map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.decayLocked(now)

	for k, v := range delta {
		key := normalizeKey(k)
		switch key {
		case confidenceKey:
			m.state.Confidence = clamp01(m.state.Confidence + v)
		case urgencyKey:
			m.state.Urgency = clamp01(m.state.Urgency + v)
		case analyticalModeKey:
			m.state.AnalyticalMode = clamp01(m.state.AnalyticalMode + v)
		case frustrationKey:
			m.state.Frustration = clamp01(m.state.Frustration + v)
		case goalPersistenceKey:
			m.state.GoalPersistence = clamp01(m.state.GoalPersistence + v)
		case toneBiasKey:
			m.state.ToneBias = clampSigned(m.state.ToneBias + v)
		case moodScoreKey:
			m.addMoodSampleLocked(v)
		}
	}

	m.state.LastUpdate = now.UTC()
}

// UpdateState applies a sentiment-aware state update for the current turn.
//
// sentiment range: [-1.0, 1.0]
// Rule: negative sentiment spikes AnalyticalMode and Frustration while lowering Confidence.
func (m *Manager) UpdateState(sentiment float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.decayLocked(now)
	s := clampSigned(sentiment)

	// Track sentiment carryover and tone inertia.
	m.addMoodSampleLocked(s)
	m.updateMoodVectorLocked(s)

	if s < 0 {
		mag := -s
		m.state.AnalyticalMode = clamp01(m.state.AnalyticalMode + (0.26 * mag))
		m.state.Frustration = clamp01(m.state.Frustration + (0.30 * mag))
		m.state.Confidence = clamp01(m.state.Confidence - (0.24 * mag))
		m.state.Urgency = clamp01(m.state.Urgency + (0.10 * mag))
	} else if s > 0 {
		mag := s
		m.state.AnalyticalMode = clamp01(m.state.AnalyticalMode + (0.08 * mag))
		m.state.Frustration = clamp01(m.state.Frustration - (0.16 * mag))
		m.state.Confidence = clamp01(m.state.Confidence + (0.20 * mag))
		m.state.Urgency = clamp01(m.state.Urgency - (0.05 * mag))
	}

	m.state.LastUpdate = now.UTC()
}

// AddMoodSample records sentiment without applying full turn-level heuristics.
func (m *Manager) AddMoodSample(score float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())
	m.addMoodSampleLocked(score)
	m.updateMoodVectorLocked(clampSigned(score))
}

// IngestSubtext maps subtext signals into the mood curve.
// Known tags: sarcasm, vulnerability, fatigue, excitement.
func (m *Manager) IngestSubtext(subtext []string, detectedScore float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())

	var derived float64
	for _, raw := range subtext {
		tag := strings.ToLower(strings.TrimSpace(raw))
		switch tag {
		case "sarcasm":
			derived -= 0.25
			m.addSubtextLocked("sarcasm")
		case "vulnerability":
			derived -= 0.15
			m.addSubtextLocked("vulnerability")
		case "fatigue":
			derived -= 0.35
			m.addSubtextLocked("fatigue")
		case "excitement":
			derived += 0.35
			m.addSubtextLocked("excitement")
		}
	}

	score := detectedScore
	if derived != 0 && score != 0 {
		score = (score * 0.7) + (derived * 0.3)
	} else if score == 0 {
		score = derived
	}
	if score != 0 {
		m.addMoodSampleLocked(score)
	}
}

// SentimentCarryover computes emotional inertia from mood history.
func (m *Manager) SentimentCarryover() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sentimentCarryover(m.state.MoodHistory)
}

// Decay regresses numeric fields toward a neutral baseline (0.5) over time.
func (m *Manager) Decay() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())
}

// GetToneBias returns final-output tone weighting derived from current affective state.
func (m *Manager) GetToneBias() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())
	return toneBiasDescriptor(m.state)
}

// GetActivePosture returns a concise high-level posture summary.
func (m *Manager) GetActivePosture() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decayLocked(time.Now())

	urgency := classify(m.state.Urgency, "High Urgency", "Low Urgency", "Balanced Urgency")
	mode := classify(m.state.AnalyticalMode, "Analytical", "Intuitive", "Balanced Mode")
	conf := classify(m.state.Confidence, "Confident", "Uncertain", "Measured Confidence")
	fr := classify(m.state.Frustration, "High Frustration", "Calm", "Managed Frustration")

	return fmt.Sprintf("%s / %s / %s / %s / %s", urgency, mode, conf, fr, tonePostureLocked(m.state))
}

// Save persists the current state to the configured JSON file.
func (m *Manager) Save() error {
	m.mu.RLock()
	stateCopy := m.state
	filePath := m.filePath
	m.mu.RUnlock()

	if stateCopy.LastUpdate.IsZero() {
		stateCopy.LastUpdate = time.Now().UTC()
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(stateCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session state: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write session state file: %w", err)
	}
	return nil
}

// Load restores the state from disk if present; otherwise defaults are used.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			m.state = defaultSessionState()
			return nil
		}
		return fmt.Errorf("failed to read session state file: %w", err)
	}

	var loaded SessionState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to unmarshal session state: %w", err)
	}

	loaded.Confidence = clamp01(loaded.Confidence)
	loaded.Urgency = clamp01(loaded.Urgency)
	loaded.AnalyticalMode = clamp01(loaded.AnalyticalMode)
	loaded.Frustration = clamp01(loaded.Frustration)
	for i := range loaded.Mood {
		loaded.Mood[i] = clamp01(loaded.Mood[i])
	}
	loaded.ToneBias = clampSigned(loaded.ToneBias)
	loaded.MoodHistory = sanitizeMoodHistory(loaded.MoodHistory)
	loaded.Subtext = sanitizeSubtextMarkers(loaded.Subtext)
	loaded.ActiveNamespace = strings.ToLower(strings.TrimSpace(loaded.ActiveNamespace))
	loaded.GoalPersistence = clamp01(loaded.GoalPersistence)
	if loaded.LastUpdate.IsZero() {
		loaded.LastUpdate = time.Now().UTC()
	}

	m.state = loaded
	return nil
}

func (m *Manager) decayLocked(now time.Time) {
	if m.state.LastUpdate.IsZero() {
		m.state.LastUpdate = now.UTC()
		return
	}
	if !now.After(m.state.LastUpdate) {
		return
	}

	elapsed := now.Sub(m.state.LastUpdate)
	if elapsed <= 0 {
		return
	}

	// Exponential decay toward neutral baseline based on half-life.
	pull := 1.0 - pow2(-elapsed.Seconds()/m.decayHalfLife.Seconds())
	m.state.Confidence = decayTowardBaseline(m.state.Confidence, pull)
	m.state.Urgency = decayTowardBaseline(m.state.Urgency, pull)
	m.state.AnalyticalMode = decayTowardBaseline(m.state.AnalyticalMode, pull)
	m.state.Frustration = decayTowardBaseline(m.state.Frustration, pull)
	for i := range m.state.Mood {
		m.state.Mood[i] = decayTowardBaseline(m.state.Mood[i], pull)
	}
	m.state.GoalPersistence = decayTowardBaseline(m.state.GoalPersistence, pull)
	m.state.ToneBias = decayTowardZero(m.state.ToneBias, pull)
	m.state.LastUpdate = now.UTC()
}

func defaultSessionState() SessionState {
	return SessionState{
		Confidence:      numericBaseline,
		Urgency:         numericBaseline,
		AnalyticalMode:  numericBaseline,
		Frustration:     numericBaseline,
		Mood:            [3]float64{numericBaseline, numericBaseline, numericBaseline},
		ToneBias:        0,
		MoodHistory:     []float64{},
		Subtext:         []string{},
		GoalPersistence: numericBaseline,
		LastUpdate:      time.Now().UTC(),
	}
}

func decayTowardBaseline(v, pull float64) float64 {
	return clamp01(v + (numericBaseline-v)*pull)
}

func decayTowardZero(v, pull float64) float64 {
	return clampSigned(v + (0-v)*pull)
}

func classify(v float64, high, low, neutral string) string {
	switch {
	case v >= 0.67:
		return high
	case v <= 0.33:
		return low
	default:
		return neutral
	}
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(s)
}

func clamp01(v float64) float64 {
	switch {
	case v < numericLowerBound:
		return numericLowerBound
	case v > numericUpperBound:
		return numericUpperBound
	default:
		return v
	}
}

func clampSigned(v float64) float64 {
	switch {
	case v < toneBiasLowerBound:
		return toneBiasLowerBound
	case v > toneBiasUpperBound:
		return toneBiasUpperBound
	default:
		return v
	}
}

func sanitizeMoodHistory(in []float64) []float64 {
	if len(in) == 0 {
		return []float64{}
	}
	if len(in) > moodHistoryLimit {
		in = in[len(in)-moodHistoryLimit:]
	}
	out := make([]float64, 0, len(in))
	for _, v := range in {
		out = append(out, clampSigned(v))
	}
	return out
}

func sanitizeSubtextMarkers(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	allowed := map[string]bool{
		"sarcasm":       true,
		"vulnerability": true,
		"fatigue":       true,
		"excitement":    true,
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.ToLower(strings.TrimSpace(v))
		if !allowed[s] || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > 8 {
		out = out[len(out)-8:]
	}
	return out
}

func (m *Manager) addSubtextLocked(marker string) {
	marker = strings.ToLower(strings.TrimSpace(marker))
	if marker == "" {
		return
	}
	m.state.Subtext = append(m.state.Subtext, marker)
	m.state.Subtext = sanitizeSubtextMarkers(m.state.Subtext)
}

func (m *Manager) addMoodSampleLocked(score float64) {
	score = clampSigned(score)
	m.state.MoodHistory = append(m.state.MoodHistory, score)
	if len(m.state.MoodHistory) > moodHistoryLimit {
		m.state.MoodHistory = m.state.MoodHistory[len(m.state.MoodHistory)-moodHistoryLimit:]
	}

	carry := sentimentCarryover(m.state.MoodHistory)
	if carry < 0 {
		m.state.Frustration = clamp01(m.state.Frustration + (-carry * 0.18))
		m.state.Confidence = clamp01(m.state.Confidence + (carry * 0.12))
	} else {
		m.state.Frustration = clamp01(m.state.Frustration - (carry * 0.12))
		m.state.Confidence = clamp01(m.state.Confidence + (carry * 0.10))
	}
	m.state.ToneBias = clampSigned((m.state.ToneBias * 0.75) + (carry * 0.25))
	m.state.LastUpdate = time.Now().UTC()
}

func (m *Manager) updateMoodVectorLocked(sentiment float64) {
	// [valence, intensity, resilience]
	valence := numericBaseline + (sentiment * 0.30)
	intensity := numericBaseline + (math.Abs(sentiment) * 0.25)
	resilience := numericBaseline + (sentiment * 0.15)
	m.state.Mood[0] = clamp01((m.state.Mood[0] * 0.80) + (valence * 0.20))
	m.state.Mood[1] = clamp01((m.state.Mood[1] * 0.82) + (intensity * 0.18))
	m.state.Mood[2] = clamp01((m.state.Mood[2] * 0.85) + (resilience * 0.15))
}

func sentimentCarryover(history []float64) float64 {
	if len(history) == 0 {
		return 0
	}
	total := 0.0
	for i := len(history) - 1; i >= 0; i-- {
		age := float64((len(history) - 1) - i)
		weight := math.Pow(moodCarryDecay, age)
		total += history[i] * weight
	}
	return clampSigned(total)
}

func tonePostureLocked(s SessionState) string {
	if s.Frustration > 0.7 {
		return "De-escalation Posture"
	}
	if s.Confidence > 0.8 {
		return "Direct/Technical Posture"
	}
	switch {
	case s.ToneBias >= 0.35:
		return "Energetic Posture"
	case s.ToneBias <= -0.35:
		return "Supportive Posture"
	default:
		return "Balanced Posture"
	}
}

func toneBiasDescriptor(s SessionState) string {
	switch {
	case s.Frustration >= 0.70:
		return "Brief, Formal, High Precision"
	case s.Confidence >= 0.80 && s.AnalyticalMode >= 0.65:
		return "Direct, Technical, Dense"
	case s.ToneBias <= -0.35:
		return "Calm, Clarifying, Stepwise"
	case s.ToneBias >= 0.35:
		return "Concise, Energetic, Solution-Forward"
	default:
		return "Balanced, Technical, Clear"
	}
}

func pow2(x float64) float64 {
	// 2^x = e^(x ln2)
	return math.Exp(x * 0.6931471805599453)
}
