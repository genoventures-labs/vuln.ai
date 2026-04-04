package output

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	baseCharDelay      = 7 * time.Millisecond
	punctuationPause   = 30 * time.Millisecond
	clausePause        = 55 * time.Millisecond
	paragraphPause     = 90 * time.Millisecond
	topicShiftPause    = 130 * time.Millisecond
	sarcasticLeadPause = 95 * time.Millisecond
	maxTotalCadenceLag = 8 * time.Second
)

var digitPattern = regexp.MustCompile(`[0-9]`)
var firstLineNumberRE = regexp.MustCompile(`(?i)(?:line\s+|:)(\d{1,6})(?::\d+)?`)

type StyleCadenceProfile struct {
	Density float64
	Tone    string
}

// PrintBreathAware streams text with a "breathing rhythm" unless raw mode is enabled.
func PrintBreathAware(text string, sm *state.Manager, raw bool) {
	PrintBreathAwareStyled(os.Stdout, text, AnalyzeSubtext(text), sm, raw)
}

// PrintBreathAwareTo streams text to a writer with cadence heuristics.
func PrintBreathAwareTo(w io.Writer, text string, sm *state.Manager, raw bool) {
	PrintBreathAwareStyled(w, text, AnalyzeSubtext(text), sm, raw)
}

// PrintReasoningMirrorLine streams one short "reasoning mirror" line.
func PrintReasoningMirrorLine(w io.Writer, line string, sm *state.Manager, raw bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	msg := line + "\n"
	if raw {
		_, _ = io.WriteString(w, msg)
		return
	}
	PrintBreathAwareTo(w, msg, sm, false)
}

// BuildLeadEngineerAuditNarrative returns a concise, code-review-style mirror line.
func BuildLeadEngineerAuditNarrative(stderr string, violations []string) string {
	problem := summarizeAuditProblem(stderr, violations)
	lineNo, hasLine := parseFirstLineNumber(stderr)
	lower := strings.ToLower(stderr + " " + strings.Join(violations, " "))

	switch {
	case strings.Contains(lower, "goroutine") && strings.Contains(lower, "leak"):
		if hasLine {
			return "Wait, we're leaking a goroutine on line " + strconv.Itoa(lineNo) + ". Pulling it back to add a proper waitgroup."
		}
		return "Wait, we're leaking a goroutine. Pulling it back to add a proper waitgroup."
	case strings.Contains(lower, "waitgroup"):
		if hasLine {
			return "Hold up, waitgroup handling is off around line " + strconv.Itoa(lineNo) + ". Reworking the sync flow before merge."
		}
		return "Hold up, waitgroup handling is off. Reworking the sync flow before merge."
	case strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "api key"):
		return "Stop there, we just exposed secret-shaped data. Rolling back that snippet and patching the leak path."
	default:
		if hasLine {
			return "Review catch: " + problem + " on line " + strconv.Itoa(lineNo) + ". Pulling it back and patching it cleanly."
		}
		return "Review catch: " + problem + ". Pulling it back and patching it cleanly."
	}
}

// BuildDebtAwarenessNarrative returns the lead-engineer tradeoff warning line.
func BuildDebtAwarenessNarrative(tradeoff, impact string) string {
	tradeoff = strings.TrimSpace(tradeoff)
	impact = strings.TrimSpace(impact)
	if tradeoff == "" {
		tradeoff = "skip the interface layer for speed"
	}
	if impact == "" {
		impact = "make later integration harder"
	}
	return "We can " + tradeoff + ", but the Architect warns this will " + impact + ". Your call, Boss."
}

// PrintBreathAwareStyled streams text using provided tonal segments.
func PrintBreathAwareStyled(w io.Writer, text string, segments []TonalSegment, sm *state.Manager, raw bool) {
	PrintBreathAwareStyledWithProfile(w, text, segments, sm, raw, nil)
}

// PrintBreathAwareStyledWithProfile streams text using provided tonal segments.
func PrintBreathAwareStyledWithProfile(w io.Writer, text string, segments []TonalSegment, sm *state.Manager, raw bool, profile *StyleCadenceProfile) {
	if text == "" {
		return
	}
	if raw {
		_, _ = io.WriteString(w, text)
		return
	}

	load := semanticLoadFactor(text)
	if profile != nil && profile.Density > 0 {
		load *= clampRangeCadence(profile.Density, 0.60, 1.40)
	}
	emotion := emotionalFactor(sm)

	var totalSlept time.Duration
	var currentWord strings.Builder
	recentWords := make([]string, 0, 4)
	segIndex := 0
	enteredSegment := false
	if len(segments) == 0 {
		segments = AnalyzeSubtext(text)
	}

	for i, r := range text {
		for len(segments) > 0 && segIndex < len(segments)-1 && i >= segments[segIndex].End {
			segIndex++
			enteredSegment = false
		}

		_, _ = io.WriteString(w, string(r))

		toneMult := toneCadenceMultiplier(segments, segIndex)
		if profile != nil && strings.TrimSpace(profile.Tone) != "" {
			toneMult *= toneMultiplierFromProfile(profile.Tone)
		}
		delay := time.Duration(float64(baseCharDelay) * load * emotion * toneMult)
		if len(segments) > 0 && !enteredSegment {
			enteredSegment = true
			if segments[segIndex].Tone == ToneSarcastic {
				delay += sarcasticLeadPause
			}
		}

		switch r {
		case '.', '!', '?':
			delay += time.Duration(float64(punctuationPause) * load * emotion)
		case ',', ';', ':':
			delay += time.Duration(float64(clausePause) * emotion)
		case '\n':
			delay += time.Duration(float64(paragraphPause) * load * emotion)
		}

		if isWordRune(r) {
			currentWord.WriteRune(r)
		} else if currentWord.Len() > 0 {
			word := normalizeWord(currentWord.String())
			currentWord.Reset()
			if word != "" {
				recentWords = append(recentWords, word)
				if len(recentWords) > 4 {
					recentWords = recentWords[len(recentWords)-4:]
				}
				if isTopicShift(recentWords) {
					delay += time.Duration(float64(topicShiftPause) * load * emotion)
				}
			}
		}

		if totalSlept+delay > maxTotalCadenceLag {
			continue
		}
		time.Sleep(delay)
		totalSlept += delay
	}
}

func toneMultiplierFromProfile(tone string) float64 {
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "direct_technical":
		return 0.94
	case "calm_clarifying":
		return 1.06
	case "supportive":
		return 1.10
	case "energetic_solution":
		return 0.96
	default:
		return 1.0
	}
}

func clampRangeCadence(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func toneCadenceMultiplier(segments []TonalSegment, idx int) float64 {
	if len(segments) == 0 || idx < 0 || idx >= len(segments) {
		return 1.0
	}
	switch segments[idx].Tone {
	case ToneTechnical:
		return 0.95
	case ToneEmpathetic:
		return 1.12
	case ToneSarcastic:
		return 1.18
	case ToneConcise:
		return 0.88
	default:
		return 1.0
	}
}

func semanticLoadFactor(text string) float64 {
	f := 1.0
	n := len(text)
	switch {
	case n > 2000:
		f += 0.6
	case n > 1000:
		f += 0.4
	case n > 450:
		f += 0.2
	}

	lines := strings.Count(text, "\n")
	if lines > 8 {
		f += 0.25
	}
	if strings.Count(text, "|") >= 8 {
		f += 0.25
	}
	if strings.Contains(text, "```") {
		f += 0.15
	}

	digits := len(digitPattern.FindAllString(text, -1))
	if n > 0 && float64(digits)/float64(n) > 0.10 {
		f += 0.15
	}

	if f > 2.2 {
		return 2.2
	}
	return f
}

func emotionalFactor(sm *state.Manager) float64 {
	if sm == nil {
		return 1.0
	}
	s := sm.GetSnapshot()
	f := 1.0
	if s.Frustration > 0.7 {
		f += 0.85
	}
	if s.Confidence > 0.8 {
		f -= 0.15
	}
	posture := strings.ToLower(sm.GetActivePosture())
	if strings.Contains(posture, "de-escalation posture") {
		f += 0.5
	}
	if f < 0.75 {
		return 0.75
	}
	if f > 2.4 {
		return 2.4
	}
	return f
}

func normalizeWord(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, ".,!?;:\"'()[]{}")
	return s
}

func isTopicShift(words []string) bool {
	if len(words) == 0 {
		return false
	}
	last := words[len(words)-1]
	switch last {
	case "however", "additionally", "meanwhile":
		return true
	}

	if len(words) >= 2 {
		a, b := words[len(words)-2], words[len(words)-1]
		if (a == "moving" && b == "on") || (a == "that" && b == "said") {
			return true
		}
	}
	if len(words) >= 4 {
		a, b, c, d := words[len(words)-4], words[len(words)-3], words[len(words)-2], words[len(words)-1]
		if a == "on" && b == "the" && c == "other" && d == "hand" {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func parseFirstLineNumber(s string) (int, bool) {
	m := firstLineNumberRE.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func summarizeAuditProblem(stderr string, violations []string) string {
	if len(violations) > 0 {
		v := strings.TrimSpace(violations[0])
		if v != "" {
			return trimToSentence(v)
		}
	}
	s := trimToSentence(stderr)
	if s == "" {
		return "symbolic policy violation"
	}
	return s
}

func trimToSentence(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return ""
	}
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.Index(s, sep); idx > 0 {
			return strings.TrimSpace(s[:idx])
		}
	}
	if len(s) > 120 {
		return strings.TrimSpace(s[:120])
	}
	return s
}
