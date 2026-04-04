package taloscli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/spf13/cobra"
)

var learnImagePaths []string
var learnImageAnchor string
var learnImageMeta []string
var learnImageNamespace string

var learnImageCmd = &cobra.Command{
	Use:   "learn-image [image-path ...]",
	Short: "Index screenshots/images into multimodal memory with cross-modal links.",
	Long: `Processes one or more images with a local vision model (llava/moondream),
extracts OCR/layout/technical descriptors, and stores them in the knowledge vector store.
Optionally links descriptors to an anchor text fragment for cross-modal retrieval.`,
	Run: func(cmd *cobra.Command, args []string) {
		paths := collectImagePaths(learnImagePaths, args)
		if len(paths) == 0 {
			fmt.Println("Please provide at least one image path via --image or positional args.")
			return
		}
		session := newLearnSession("LEARN_IMAGE", strings.Join(paths, ","), paths, map[string]string{
			"namespace": strings.TrimSpace(learnImageNamespace),
		})

		meta, err := parseMetadataPairs(learnImageMeta)
		if err != nil {
			fmt.Printf("Error parsing metadata: %v\n", err)
			session.finish("FAILED", "Image learning failed before processing.", err.Error(), map[string]int64{"images_indexed": 0, "errors": 1})
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			return
		}

		mm, err := memory.NewMemoryManager()
		if err != nil {
			fmt.Printf("Error initializing memory manager: %v\n", err)
			session.finish("FAILED", "Image learning failed during memory manager initialization.", err.Error(), map[string]int64{"images_indexed": 0, "errors": 1})
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			return
		}
		if ns := strings.TrimSpace(learnImageNamespace); ns != "" {
			mm.SetActiveNamespace(ns)
		}

		success := 0
		for _, p := range paths {
			fmt.Printf("Processing image: %s\n", p)
			res, err := mm.IngestImageKnowledge(p, learnImageAnchor, meta)
			if err != nil {
				fmt.Printf("  Error: %v\n", err)
				continue
			}
			success++
			fmt.Printf("  Model: %s\n", res.Model)
			fmt.Printf("  LinkID: %s\n", res.LinkID)
			if strings.TrimSpace(res.LinkedTextDocID) != "" {
				fmt.Printf("  Linked text doc: %s\n", res.LinkedTextDocID)
			}
		}

		if success == 0 {
			fmt.Println("No images were indexed.")
			session.finish("FAILED", "No images were indexed.", "all image ingests failed", map[string]int64{
				"images_total":   int64(len(paths)),
				"images_indexed": 0,
				"errors":         int64(len(paths)),
			})
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			return
		}
		status := "SUCCESS"
		if success < len(paths) {
			status = "PARTIAL"
		}
		session.finish(status, "Image learning run completed.", "", map[string]int64{
			"images_total":   int64(len(paths)),
			"images_indexed": int64(success),
			"errors":         int64(len(paths) - success),
		})
		if logErr := appendLearnSessionRecord(session); logErr != nil {
			fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
		}
		fmt.Printf("Indexed %d/%d image(s) into multimodal memory.\n", success, len(paths))
	},
}

func collectImagePaths(flagPaths []string, args []string) []string {
	var in []string
	in = append(in, flagPaths...)
	in = append(in, args...)

	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		expanded := expandGlobs(raw)
		if len(expanded) == 0 {
			expanded = []string{raw}
		}
		for _, p := range expanded {
			key := strings.ToLower(strings.TrimSpace(p))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	return out
}

func expandGlobs(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[]") {
		return nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	return matches
}

func parseMetadataPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string)
	for _, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		i := strings.Index(p, "=")
		if i <= 0 || i == len(p)-1 {
			return nil, fmt.Errorf("invalid metadata pair %q; expected key=value", p)
		}
		k := strings.TrimSpace(p[:i])
		v := strings.TrimSpace(p[i+1:])
		if k == "" {
			return nil, fmt.Errorf("invalid metadata key in pair %q", p)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func init() {
	learnImageCmd.Flags().StringArrayVarP(&learnImagePaths, "image", "i", nil, "Image path to index (repeatable, supports shell globs)")
	learnImageCmd.Flags().StringVarP(&learnImageAnchor, "anchor", "a", "", "Anchor text for cross-modal linking (e.g., 'VPS firewall config screenshot')")
	learnImageCmd.Flags().StringArrayVar(&learnImageMeta, "meta", nil, "Metadata key=value pairs to store (repeatable)")
	learnImageCmd.Flags().StringVar(&learnImageNamespace, "namespace", "", "Optional memory namespace for this ingest")
	rootCmd.AddCommand(learnImageCmd)
}
