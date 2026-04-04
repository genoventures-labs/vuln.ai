package taloscli

import (
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/router"
)

func renderResearchDryRunPlan(mode, query string, ctx researchExecutionContext) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	query = strings.TrimSpace(query)
	initialModel := "n/a"
	candidates := []string{}
	r, err := router.NewRouter()
	if err == nil {
		initialModel = r.ResolveModel(router.ResolveRequest{
			Query:  query,
			Stage:  "research",
			Models: r.Models,
		})
		candidates = buildModelCandidates(r.Models, initialModel)
	}

	var b strings.Builder
	b.WriteString("TALOS RESEARCH DRY RUN\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos research " + strings.ToLower(strings.TrimSpace(mode)) + "\n\n")
	b.WriteString("STATUS\n")
	b.WriteString("  SKIPPED\n\n")
	b.WriteString("MODE\n")
	b.WriteString("  " + strings.ToUpper(mode) + "\n\n")
	b.WriteString("QUERY\n")
	b.WriteString("  " + query + "\n\n")
	b.WriteString("PROFILE\n")
	b.WriteString("  selected: " + emptyAsNA(ctx.ProfileName) + "\n")
	b.WriteString("  categories: " + emptyAsNA(strings.Join(ctx.ProfileCategories, ", ")) + "\n")
	b.WriteString("  requested: " + emptyAsNA(researchProfileForRun) + "\n")
	b.WriteString("  requested_category: " + emptyAsNA(researchCategoryForRun) + "\n\n")
	b.WriteString("BUDGET\n")
	if mode == "deep" {
		b.WriteString(fmt.Sprintf("  max_pages: %d\n", researchDeepMaxPages))
		b.WriteString(fmt.Sprintf("  crawl_depth: %d\n", researchDeepCrawlDepth))
		b.WriteString(fmt.Sprintf("  max_plan_steps: %d\n", researchDeepMaxPlanSteps))
		b.WriteString(fmt.Sprintf("  max_research_loops: %d\n", researchDeepMaxResearchLoops))
		b.WriteString(fmt.Sprintf("  timeout: %s\n", researchDeepTimeout))
		b.WriteString(fmt.Sprintf("  verbose: %t\n", researchDeepVerbose))
		b.WriteString("  seed_urls: " + emptyAsNA(strings.Join(researchDeepSeedURLs, ", ")) + "\n")
	} else {
		b.WriteString(fmt.Sprintf("  max_pages: %d\n", researchRunMaxPages))
		b.WriteString(fmt.Sprintf("  crawl_depth: %d\n", researchRunCrawlDepth))
		b.WriteString(fmt.Sprintf("  max_research_loops: %d\n", researchRunMaxResearchLoops))
		b.WriteString(fmt.Sprintf("  timeout: %s\n", researchRunTimeout))
		b.WriteString(fmt.Sprintf("  verbose: %t\n", researchRunVerbose))
		b.WriteString("  seed_urls: " + emptyAsNA(strings.Join(researchRunSeedURLs, ", ")) + "\n")
	}
	b.WriteString("\nROUTING\n")
	b.WriteString("  initial_model: " + emptyAsNA(initialModel) + "\n")
	if len(candidates) == 0 {
		b.WriteString("  model_candidates: n/a\n")
	} else {
		b.WriteString("  model_candidates: " + strings.Join(candidates, ", ") + "\n")
	}
	b.WriteString("\nEXECUTION\n")
	b.WriteString("  skipped: true\n")
	b.WriteString("  reason: dry-run mode\n")
	return strings.TrimRight(b.String(), "\n")
}
