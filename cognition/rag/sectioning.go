package rag

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var markdownHeadingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

type sectionMeta struct {
	ID       string
	Title    string
	Level    int
	Inferred bool
}

func inferChunkSections(chunks []string, sourceRef string) []sectionMeta {
	out := make([]sectionMeta, 0, len(chunks))
	currentTitle := "General"
	currentLevel := 0
	ordinal := 1
	for i, chunk := range chunks {
		foundTitle, foundLevel, explicit := headingFromChunk(chunk)
		if explicit {
			if i > 0 {
				ordinal++
			}
			currentTitle = foundTitle
			currentLevel = foundLevel
		}
		id := fmt.Sprintf("%s::section-%d", strings.TrimSpace(sourceRef), ordinal)
		out = append(out, sectionMeta{
			ID:       id,
			Title:    truncateSectionMeta(currentTitle, 120),
			Level:    currentLevel,
			Inferred: !explicit,
		})
	}
	return out
}

func headingFromChunk(chunk string) (string, int, bool) {
	lines := strings.Split(strings.TrimSpace(chunk), "\n")
	for _, line := range lines {
		clean := strings.TrimSpace(line)
		if clean == "" {
			continue
		}
		if m := markdownHeadingPattern.FindStringSubmatch(clean); len(m) == 3 {
			return truncateSectionMeta(strings.TrimSpace(m[2]), 120), len(m[1]), true
		}
		lower := strings.ToLower(clean)
		if strings.HasPrefix(lower, "section:") {
			title := strings.TrimSpace(clean[len("section:"):])
			if title != "" {
				return truncateSectionMeta(title, 120), 1, true
			}
		}
		break
	}
	return "", 0, false
}

func sectionMetaAsStrings(m sectionMeta) (id string, title string, level string, inferred string) {
	id = strings.TrimSpace(m.ID)
	title = strings.TrimSpace(m.Title)
	if title == "" {
		title = "General"
	}
	level = strconv.Itoa(m.Level)
	inferred = "true"
	if !m.Inferred {
		inferred = "false"
	}
	return id, title, level, inferred
}

func truncateSectionMeta(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
