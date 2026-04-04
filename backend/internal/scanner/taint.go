package scanner

import (
	"fmt"
	"log"
	"strings"

	"github.com/user/azimuthal-belt/backend/internal/db"
)

type TaintSource struct {
	Name    string
	Pattern string
}

type TaintSink struct {
	Name    string
	Pattern string
}

type TaintAnalyzer struct {
	Sources []TaintSource
	Sinks   []TaintSink
	DB      *db.PocketbaseClient
}

func NewTaintAnalyzer(pb *db.PocketbaseClient) *TaintAnalyzer {
	return &TaintAnalyzer{
		DB: pb,
		Sources: []TaintSource{
			{Name: "HTTP FormValue", Pattern: "FormValue"},
			{Name: "HTTP Body", Pattern: "json.NewDecoder"},
			{Name: "OS Args", Pattern: "os.Args"},
		},
		Sinks: []TaintSink{
			{Name: "Command Exec", Pattern: "exec.Command"},
			{Name: "SQL Query", Pattern: "db.Exec"},
			{Name: "File Write", Pattern: "os.WriteFile"},
		},
	}
}

// TraceTaint checks if an untrusted source in one shard can REACH a sink in another
func (ta *TaintAnalyzer) TraceTaint(finding Finding, shards []db.CogShard) (bool, string) {
	// 1. Identify if the finding's snippet contains a known source
	hasSource := false
	sourceType := ""
	for _, src := range ta.Sources {
		if strings.Contains(finding.Snippet, src.Pattern) {
			hasSource = true
			sourceType = src.Name
			break
		}
	}

	if !hasSource {
		log.Printf("Taint: No immediate untrusted source detected in snippet for %s", finding.File)
		return false, "No immediate untrusted source detected in snippet"
	}

	log.Printf("Taint: Source detected in %s: %s", finding.File, sourceType)

	// 2. Identify outgoing calls from this snippet
	// We need to find the shard that corresponds to this finding
	var currentShard *db.CogShard
	for _, s := range shards {
		if s.Source == finding.File && strings.Contains(s.Content, finding.Snippet) {
			currentShard = &s
			break
		}
	}

	if currentShard == nil {
		return false, "Could not locate shard for finding"
	}

	calls, _ := currentShard.Metadata["calls"].([]interface{})
	if len(calls) == 0 {
		return false, "No downstream calls to trace"
	}

	// 3. Recursive trace (simplified to 1 level for now)
	for _, callObj := range calls {
		callName := callObj.(string)

		// Search for shard defining this function
		for _, s := range shards {
			// Check if this shard represents the function declaration
			if strings.Contains(s.Content, "func "+callName) || strings.HasSuffix(s.Source, callName+".go") {
				// Check for Sinks in this child shard
				for _, sink := range ta.Sinks {
					if strings.Contains(s.Content, sink.Pattern) {
						res := fmt.Sprintf("CRITICAL: Taint flows from %s into %s via call to %s", sourceType, sink.Name, callName)
						log.Printf("Taint: SUCCESS: %s", res)
						return true, res
					}
				}
			}
		}
	}

	log.Printf("Taint: Source detected, but no path to sensitive sink found in immediate call graph for %s", finding.File)
	return false, "Source detected, but no path to sensitive sink found in immediate call graph"
}
