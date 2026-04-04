package scanner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"golang.org/x/net/html"

	"github.com/user/azimuthal-belt/backend/internal/ai"
	"github.com/user/azimuthal-belt/backend/internal/db"
	"github.com/user/azimuthal-belt/backend/internal/learner"
)

func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

type Indexer struct {
	AIClient *ai.AIClient
	DBClient *db.PocketbaseClient
	Learner  *learner.Store
}

func NewIndexer(aiClient *ai.AIClient, dbClient *db.PocketbaseClient, learnerStore *learner.Store) *Indexer {
	return &Indexer{
		AIClient: aiClient,
		DBClient: dbClient,
		Learner:  learnerStore,
	}
}

func (idx *Indexer) IndexDirectory(root string) error {
	var fileSummaries []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "node_modules" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		base := filepath.Base(path)
		if !isSupportedExtension(ext, base) {
			return nil
		}

		fileSummary, indexErr := idx.indexFile(path)
		if indexErr != nil {
			log.Printf("Indexer: Failed to index file %s: %v", path, indexErr)
		} else if fileSummary != "" {
			fileSummaries = append(fileSummaries, fmt.Sprintf("%s: %s", filepath.Base(path), fileSummary))
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Phase 7: Hierarchical Summarization (Package Level)
	if len(fileSummaries) > 0 {
		pkgName := filepath.Base(root)

		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("SummarizePackage", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		pkgSummary, err := idx.AIClient.SummarizePackage(pkgName, fileSummaries, feedbackCtx)
		if err != nil {
			log.Printf("Indexer: Failed to generate package summary for %s: %v", pkgName, err)
		} else {
			embedding, embErr := idx.AIClient.CreateEmbedding(pkgSummary)
			if embErr == nil {
				shard := db.CogShard{
					Embedding: embedding,
					Title:     pkgName,
					Content:   pkgSummary,
					Source:    root,
					Type:      "package_summary",
					Metadata: map[string]interface{}{
						"package_name": pkgName,
						"file_count":   len(fileSummaries),
					},
				}
				if idx.DBClient != nil && idx.DBClient.Token != "" {
					idx.DBClient.SaveShard(shard)
				}
				log.Printf("Indexer: Parsed package summary for %s (DB offline: %v)", pkgName, idx.DBClient == nil || idx.DBClient.Token == "")
			}
		}
	}

	return nil
}

func (idx *Indexer) LearnURL(url string) error {
	log.Printf("Indexer: Learning from URL: %s", url)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Indexer: http.Get failed for %s: %v", url, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("URL returned status: %d", resp.StatusCode)
	}

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	content := buf.String()

	chunks := chunkContent(content, 1200)
	log.Printf("Indexer: Ingesting %d chunks from %s", len(chunks), url)

	for i, chunk := range chunks {
		embedding, err := idx.AIClient.CreateEmbedding(chunk)
		if err != nil {
			log.Printf("Indexer: CreateEmbedding failed for chunk %d: %v", i, err)
			continue
		}

		shard := db.CogShard{
			Embedding: embedding,
			Title:     url,
			Content:   chunk,
			Source:    url,
			Type:      "external_reference",
			Metadata: map[string]interface{}{
				"chunk_index": i,
				"source_type": "external_reference",
			},
		}

		if err := idx.DBClient.SaveShard(shard); err != nil {
			log.Printf("Indexer: SaveShard failed for chunk %d: %v", i, err)
		} else {
			log.Printf("Indexer: Saved chunk %d/%d for %s", i+1, len(chunks), url)
		}
	}

	return nil
}

func (idx *Indexer) CrawlAndLearn(startURL string, maxPages int, headers, cookies map[string]interface{}) error {
	base, err := url.Parse(startURL)
	if err != nil {
		return err
	}

	visited := make(map[string]bool)
	var queue []string
	queue = append(queue, startURL)

	client := &http.Client{Timeout: 10 * time.Second}

	for len(queue) > 0 {
		if len(visited) >= maxPages {
			break
		}

		currentURL := queue[0]
		queue = queue[1:]

		if visited[currentURL] {
			continue
		}
		visited[currentURL] = true

		log.Printf("Indexer (Crawl): Fetching %s", currentURL)

		req, err := http.NewRequest("GET", currentURL, nil)
		if err != nil {
			log.Printf("Indexer (Crawl): Failed to create request for %s: %v", currentURL, err)
			continue
		}

		if headers != nil {
			for k, v := range headers {
				req.Header.Set(k, fmt.Sprintf("%v", v))
			}
		}

		if cookies != nil {
			for k, v := range cookies {
				req.AddCookie(&http.Cookie{Name: k, Value: fmt.Sprintf("%v", v)})
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Indexer (Crawl): Failed to fetch %s: %v", currentURL, err)
			continue
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			continue
		}

		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		content := buf.String()
		resp.Body.Close()

		chunks := chunkContent(content, 1200)
		for i, chunk := range chunks {
			embedding, err := idx.AIClient.CreateEmbedding(chunk)
			if err != nil {
				continue
			}
			shard := db.CogShard{
				Embedding: embedding,
				Title:     currentURL,
				Content:   chunk,
				Source:    currentURL,
				Type:      "crawled_content",
				Metadata: map[string]interface{}{
					"chunk_index": i,
					"source_type": "crawled_content",
				},
			}
			if idx.DBClient != nil && idx.DBClient.Token != "" {
				idx.DBClient.SaveShard(shard)
			}
		}

		newUrls := idx.extractTargetsFromHTML(content, currentURL, base)
		for _, link := range newUrls {
			queue = append(queue, link)
		}
	}

	log.Printf("Indexer: Crawling completed. Pages learned: %d", len(visited))
	return nil
}

func (idx *Indexer) CrawlWithRod(startURL string, maxPages int, headers, cookies map[string]interface{}) error {
	base, err := url.Parse(startURL)
	if err != nil {
		return err
	}

	visited := make(map[string]bool)
	var queue []string
	queue = append(queue, startURL)

	browser := rod.New().MustConnect()
	defer browser.MustClose()

	for len(queue) > 0 {
		if len(visited) >= maxPages {
			break
		}

		currentURL := queue[0]
		queue = queue[1:]

		if visited[currentURL] {
			continue
		}
		visited[currentURL] = true

		log.Printf("Indexer (Rod Crawl): Fetching %s", currentURL)

		page := stealth.MustPage(browser)

		// Set headers
		if headers != nil {
			var rodHeaders []string
			for k, v := range headers {
				rodHeaders = append(rodHeaders, k, fmt.Sprintf("%v", v))
			}
			_, _ = page.SetExtraHeaders(rodHeaders)
		}

		// Navigate to domain first to set non-empty URL cookies, or use SetCookies.
		// For simplicity, we'll navigate first, then inject cookies, then reload.
		err := page.Navigate(currentURL)
		if err != nil {
			log.Printf("Indexer (Rod Crawl): Failed to navigate %s: %v", currentURL, err)
			page.MustClose()
			continue
		}

		if cookies != nil {
			var rodCookies []*proto.NetworkCookieParam
			for k, v := range cookies {
				val := fmt.Sprintf("%v", v)
				rodCookies = append(rodCookies, &proto.NetworkCookieParam{
					Name:   k,
					Value:  val,
					Domain: base.Host,
					URL:    currentURL,
				})
			}
			_ = page.SetCookies(rodCookies)
			_ = page.Reload() // reload with the authenticated session
		}

		page.MustWaitIdle()
		time.Sleep(1 * time.Second) // wait for local SPA timers

		content, err := page.HTML()
		if err != nil {
			log.Printf("Indexer (Rod Crawl): Failed to get HTML %s: %v", currentURL, err)
			page.MustClose()
			continue
		}
		page.MustClose()

		chunks := chunkContent(content, 1200)
		for i, chunk := range chunks {
			embedding, err := idx.AIClient.CreateEmbedding(chunk)
			if err != nil {
				continue
			}
			shard := db.CogShard{
				Embedding: embedding,
				Title:     currentURL,
				Content:   chunk,
				Source:    currentURL,
				Type:      "crawled_content_js",
				Metadata: map[string]interface{}{
					"chunk_index": i,
					"source_type": "crawled_content_js",
				},
			}
			if idx.DBClient != nil && idx.DBClient.Token != "" {
				idx.DBClient.SaveShard(shard)
			}
		}

		newUrls := idx.extractTargetsFromHTML(content, currentURL, base)
		for _, link := range newUrls {
			queue = append(queue, link)
		}
	}

	log.Printf("Indexer: JS Crawling completed. Pages learned: %d", len(visited))
	return nil
}

func (idx *Indexer) extractTargetsFromHTML(content, currentURL string, base *url.URL) []string {
	var collectedURLs []string
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		log.Printf("Indexer (Parse): Failed to parse HTML from %s: %v", currentURL, err)
		return collectedURLs
	}

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					linkURL, err := base.Parse(a.Val)
					if err == nil && linkURL.Host == base.Host {
						linkURL.Fragment = ""
						collectedURLs = append(collectedURLs, linkURL.String())

						// Flag API Documentation
						linkLower := strings.ToLower(linkURL.Path)
						if strings.Contains(linkLower, "swagger") || strings.Contains(linkLower, "openapi") || strings.Contains(linkLower, "api-docs") {
							log.Printf("Indexer (API): Found potential API Documentation at %s", linkURL.String())
							go func() {
								// Attempt to fetch the swagger JSON
								resp, err := http.Get(linkURL.String())
								if err == nil {
									defer resp.Body.Close()
									bodyBytes, err := io.ReadAll(resp.Body)
									if err == nil && len(bodyBytes) > 0 {
										log.Printf("Indexer (API): Successfully fetched Swagger JSON from %s, sending to AI mapping...", linkURL.String())
										targets, err := idx.AIClient.MapSwaggerSchema(string(bodyBytes))
										if err == nil {
											log.Printf("Indexer (API): AI successfully mapped %d API endpoints from Swagger doc", len(targets))
											if idx.DBClient != nil && idx.DBClient.Token != "" {
												for _, t := range targets {
													t.SourceURL = linkURL.String() // Set origin
													_ = idx.DBClient.SaveProbingTarget(t)
												}
											}
										} else {
											log.Printf("Indexer (API): AI failed to map swagger doc: %v", err)
										}
									}
								}

								// Save the swagger root document itself as a target for manual testing
								if idx.DBClient != nil && idx.DBClient.Token != "" {
									_ = idx.DBClient.SaveProbingTarget(map[string]interface{}{
										"source_url": currentURL,
										"action":     linkURL.String(),
										"method":     "GET",
										"type":       "api_target",
										"metadata": map[string]interface{}{
											"discovery_type": "api_doc_path",
										},
									})
								}
							}()
						}
					}
					break
				}
			}
		}

		if n.Type == html.ElementNode && n.Data == "form" {
			target := learner.ProbingTarget{
				SourceURL: currentURL,
				Method:    "GET",
				Action:    currentURL,
			}
			for _, a := range n.Attr {
				if a.Key == "method" {
					target.Method = strings.ToUpper(a.Val)
				} else if a.Key == "action" {
					actionURL, err := base.Parse(a.Val)
					if err == nil {
						target.Action = actionURL.String()
					} else {
						target.Action = a.Val
					}
				}
			}

			var extractInputs func(*html.Node)
			extractInputs = func(inNode *html.Node) {
				if inNode.Type == html.ElementNode && (inNode.Data == "input" || inNode.Data == "textarea" || inNode.Data == "select") {
					field := learner.FormField{Type: "text"}
					if inNode.Data != "input" {
						field.Type = inNode.Data
					}
					for _, a := range inNode.Attr {
						if a.Key == "name" {
							field.Name = a.Val
						} else if a.Key == "type" {
							field.Type = a.Val
						} else if a.Key == "value" {
							field.Value = a.Val
						}
					}
					if field.Name != "" {
						target.Fields = append(target.Fields, field)
					}
				}
				for c := inNode.FirstChild; c != nil; c = c.NextSibling {
					extractInputs(c)
				}
			}
			extractInputs(n)

			if len(target.Fields) > 0 {
				log.Printf("Indexer (Extract): Found learner.ProbingTarget at %s -> Action: %s, Method: %s, Fields: %d", target.SourceURL, target.Action, target.Method, len(target.Fields))
				if idx.DBClient != nil && idx.DBClient.Token != "" {
					go func() {
						_ = idx.DBClient.SaveProbingTarget(target)
					}()
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return collectedURLs
}

func (idx *Indexer) LearnTopic(topic string) error {
	log.Printf("Indexer: Learning from Topic: %s", topic)
	urls, err := idx.AIClient.WebSearch(topic)
	if err != nil {
		log.Printf("Indexer: WebSearch failed for %s: %v", topic, err)
		return err
	}

	log.Printf("Indexer: Found %d URLs for topic: %s", len(urls), topic)

	// Learn from top 3 results
	maxResults := 3
	if len(urls) < maxResults {
		maxResults = len(urls)
	}

	for i := 0; i < maxResults; i++ {
		log.Printf("Indexer: Processing URL %d/%d: %s", i+1, maxResults, urls[i])
		_ = idx.LearnURL(urls[i])
	}

	return nil
}

func (idx *Indexer) parseGoCodeSemantically(path string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		log.Printf("Indexer: AST parse failed for %s, falling back to basic chunking: %v", path, err)
		return idx.indexFileBasic(path)
	}

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	contentStr := string(contentBytes)

	type ChunkData struct {
		Text     string
		FuncName string
		Calls    []string
	}
	var chunks []ChunkData
	var funcSummaries []string

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			start := fset.Position(x.Pos()).Offset
			end := fset.Position(x.End()).Offset
			if start >= 0 && end > start && end <= len(contentStr) {
				// Find calls inside this function
				var calls []string
				ast.Inspect(x.Body, func(bn ast.Node) bool {
					if call, ok := bn.(*ast.CallExpr); ok {
						if ident, ok := call.Fun.(*ast.Ident); ok {
							calls = append(calls, ident.Name)
						} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
							if pkgIdent, ok := sel.X.(*ast.Ident); ok {
								calls = append(calls, pkgIdent.Name+"."+sel.Sel.Name)
							}
						}
					}
					return true
				})

				chunks = append(chunks, ChunkData{
					Text:     contentStr[start:end],
					FuncName: x.Name.Name,
					Calls:    calls,
				})
			}
		case *ast.GenDecl:
			start := fset.Position(x.Pos()).Offset
			end := fset.Position(x.End()).Offset
			if start >= 0 && end > start && end <= len(contentStr) {
				chunks = append(chunks, ChunkData{
					Text: contentStr[start:end],
				})
			}
		}
		return true
	})

	if len(chunks) == 0 {
		return idx.indexFileBasic(path)
	}

	for i, chunk := range chunks {
		hashVal := hashString(chunk.Text)

		testCode := ""
		if chunk.FuncName != "" {
			testPath := strings.TrimSuffix(path, ".go") + "_test.go"
			if testContent, err := os.ReadFile(testPath); err == nil {
				tfset := token.NewFileSet()
				tf, terr := parser.ParseFile(tfset, testPath, nil, 0)
				if terr == nil {
					ast.Inspect(tf, func(tn ast.Node) bool {
						if tfd, ok := tn.(*ast.FuncDecl); ok {
							if tfd.Name.Name == "Test"+chunk.FuncName {
								tstart := tfset.Position(tfd.Pos()).Offset
								tend := tfset.Position(tfd.End()).Offset
								if tstart >= 0 && tend > tstart && tend <= len(string(testContent)) {
									testCode = string(testContent)[tstart:tend]
								}
							}
						}
						return true
					})
				}
			}
		}

		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("ExplainCode", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		explanation, expErr := idx.AIClient.ExplainCode(chunk.Text, testCode, feedbackCtx)
		if expErr != nil {
			log.Printf("Indexer: ExplainCode failed for chunk %d: %v", i, expErr)
			explanation = "Failed to generate explanation."
		} else {
			funcSummaries = append(funcSummaries, explanation)
		}

		combinedContent := fmt.Sprintf("Explanation:\n%s\n\nCode:\n%s", explanation, chunk.Text)

		embedding, err := idx.AIClient.CreateEmbedding(combinedContent)
		if err != nil {
			fmt.Printf("Error creating embedding for %s AST chunk %d: %v\n", path, i, err)
			continue
		}

		shard := db.CogShard{
			Embedding: embedding,
			Title:     filepath.Base(path),
			Content:   combinedContent,
			Source:    path,
			Type:      "semantic_code_chunk",
			Metadata: map[string]interface{}{
				"chunk_index":    i,
				"file_path":      path,
				"explanation_v1": explanation,
				"explanation_v2": "",
				"last_seen_hash": hashVal,
				"calls":          chunk.Calls,
			},
		}

		if idx.DBClient != nil && idx.DBClient.Token != "" {
			if err := idx.DBClient.SaveShard(shard); err != nil {
				fmt.Printf("Error saving shard for %s AST chunk %d: %v\n", path, i, err)
			} else {
				log.Printf("Indexer: Saved semantic chunk %d with hash %s and explanation length %d", i, hashVal, len(explanation))
			}
		} else {
			log.Printf("Indexer: Parsed semantic chunk %d with hash %s. Epistemic Explanation length %d. (DB offline)", i, hashVal, len(explanation))
		}
	}

	// Phase 7: Hierarchical Summarization (File Level)
	fileSummary := ""
	if len(funcSummaries) > 0 {
		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("SummarizeFile", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		summary, err := idx.AIClient.SummarizeFile(filepath.Base(path), funcSummaries, feedbackCtx)
		if err != nil {
			log.Printf("Indexer: Failed to generate file summary for %s: %v", filepath.Base(path), err)
		} else {
			fileSummary = summary
			embedding, embErr := idx.AIClient.CreateEmbedding(fileSummary)
			if embErr == nil {
				shard := db.CogShard{
					Embedding: embedding,
					Title:     filepath.Base(path),
					Content:   fileSummary,
					Source:    path,
					Type:      "file_summary",
					Metadata: map[string]interface{}{
						"file_path":   path,
						"chunk_count": len(chunks),
					},
				}
				if idx.DBClient != nil && idx.DBClient.Token != "" {
					idx.DBClient.SaveShard(shard)
				}
				log.Printf("Indexer: Parsed file summary for %s (DB offline: %v)", filepath.Base(path), idx.DBClient == nil || idx.DBClient.Token == "")
			}
		}
	}

	return fileSummary, nil
}

func (idx *Indexer) indexFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		return idx.parseGoCodeSemantically(path)
	} else if ext == ".md" {
		return idx.parseMarkdownSemantically(path)
	} else if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".py" || ext == ".java" {
		return idx.parseStructuralCode(path, ext)
	}
	return idx.indexFileBasic(path)
}

func (idx *Indexer) parseStructuralCode(path string, ext string) (string, error) {
	chunks, err := GetStructuralChunks(path, ext)
	if err != nil {
		log.Printf("Indexer: Tree-Sitter failed for %s, falling back to basic: %v", path, err)
		return idx.indexFileBasic(path)
	}

	if len(chunks) == 0 {
		return idx.indexFileBasic(path)
	}

	var funcSummaries []string
	for i, chunk := range chunks {
		hashVal := hashString(chunk.Content)

		// For now, we don't have a standardized way to find "testCode" for TS/Python/Java as easily as Go
		// We will pass empty testCode for now, but in Phase 14 we will expand this.
		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("ExplainCode", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		explanation, expErr := idx.AIClient.ExplainCode(chunk.Content, "", feedbackCtx)
		if expErr != nil {
			log.Printf("Indexer: ExplainCode failed for structural chunk %d: %v", i, expErr)
			explanation = "Failed to generate explanation."
		} else {
			funcSummaries = append(funcSummaries, explanation)
		}

		embedding, err := idx.AIClient.CreateEmbedding(chunk.Content)
		if err != nil {
			log.Printf("Indexer: CreateEmbedding failed for structural chunk %d: %v", i, err)
			continue
		}

		shard := db.CogShard{
			Embedding: embedding,
			Title:     chunk.Name,
			Content:   fmt.Sprintf("Explanation:\n%s\n\nCode:\n%s", explanation, chunk.Content),
			Source:    path,
			Type:      "structural_chunk",
			Metadata: map[string]interface{}{
				"chunk_index":    i,
				"file_path":      path,
				"last_seen_hash": hashVal,
				"explanation_v1": explanation,
				"chunk_type":     chunk.Type,
				"start_row":      chunk.StartRow,
				"end_row":        chunk.EndRow,
			},
		}

		if idx.DBClient != nil && idx.DBClient.Token != "" {
			if err := idx.DBClient.SaveShard(shard); err != nil {
				log.Printf("Error saving structural shard for %s chunk %d: %v", path, i, err)
			}
		}
	}

	fileSummary := ""
	if len(funcSummaries) > 0 {
		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("SummarizeFile", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		summary, err := idx.AIClient.SummarizeFile(filepath.Base(path), funcSummaries, feedbackCtx)
		if err == nil {
			fileSummary = summary
			embedding, embErr := idx.AIClient.CreateEmbedding(fileSummary)
			if embErr == nil {
				shard := db.CogShard{
					Embedding: embedding,
					Title:     filepath.Base(path),
					Content:   fileSummary,
					Source:    path,
					Type:      "file_summary",
					Metadata: map[string]interface{}{
						"file_path": path,
					},
				}
				if idx.DBClient != nil && idx.DBClient.Token != "" {
					idx.DBClient.SaveShard(shard)
				}
			}
		}
	}

	return fileSummary, nil
}

func (idx *Indexer) parseMarkdownSemantically(path string) (string, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	contentStr := string(contentBytes)
	lines := strings.Split(contentStr, "\n")

	var chunks []string
	var currentChunk strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, "#") && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}
		currentChunk.WriteString(line + "\n")
	}
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	if len(chunks) == 0 {
		return idx.indexFileBasic(path)
	}

	var sectionSummaries []string

	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		embedding, err := idx.AIClient.CreateEmbedding(chunk)
		if err != nil {
			log.Printf("Indexer: CreateEmbedding failed for MD chunk %d: %v", i, err)
			continue
		}

		shard := db.CogShard{
			Embedding: embedding,
			Title:     filepath.Base(path),
			Content:   chunk,
			Source:    path,
			Type:      "markdown_chunk",
			Metadata: map[string]interface{}{
				"chunk_index": i,
				"file_path":   path,
			},
		}

		if idx.DBClient != nil && idx.DBClient.Token != "" {
			if err := idx.DBClient.SaveShard(shard); err != nil {
				log.Printf("Error saving shard for %s chunk %d: %v", path, i, err)
			}
		}

		summaryLen := len(chunk)
		if summaryLen > 200 {
			summaryLen = 200
		}
		sectionSummaries = append(sectionSummaries, chunk[:summaryLen])
	}

	fileSummary := ""
	if len(sectionSummaries) > 0 {
		var feedbackCtx []string
		if idx.Learner != nil {
			fb := idx.Learner.GetRelevantFeedback("SummarizeFile", "")
			for _, f := range fb {
				feedbackCtx = append(feedbackCtx, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		summary, err := idx.AIClient.SummarizeFile(filepath.Base(path), sectionSummaries, feedbackCtx)
		if err == nil {
			fileSummary = summary
			embedding, embErr := idx.AIClient.CreateEmbedding(fileSummary)
			if embErr == nil {
				shard := db.CogShard{
					Embedding: embedding,
					Title:     filepath.Base(path),
					Content:   fileSummary,
					Source:    path,
					Type:      "file_summary",
					Metadata: map[string]interface{}{
						"file_path": path,
					},
				}
				if idx.DBClient != nil && idx.DBClient.Token != "" {
					idx.DBClient.SaveShard(shard)
				}
			}
		}
	}

	return fileSummary, nil
}

func (idx *Indexer) indexFileBasic(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	chunks := chunkContent(string(content), 1000) // Chunk by 1000 characters for now

	for i, chunk := range chunks {
		embedding, err := idx.AIClient.CreateEmbedding(chunk)
		if err != nil {
			fmt.Printf("Error creating embedding for %s chunk %d: %v\n", path, i, err)
			continue
		}

		shard := db.CogShard{
			Embedding: embedding,
			Title:     filepath.Base(path),
			Content:   chunk,
			Source:    path,
			Type:      "code_chunk",
			Metadata: map[string]interface{}{
				"chunk_index": i,
				"file_path":   path,
			},
		}

		if idx.DBClient != nil && idx.DBClient.Token != "" {
			if err := idx.DBClient.SaveShard(shard); err != nil {
				fmt.Printf("Error saving shard for %s chunk %d: %v\n", path, i, err)
			}
		} else {
			log.Printf("Indexer: Parsed basic chunk %d. (DB offline)", i)
		}
	}

	return "", nil
}

func chunkContent(content string, chunkSize int) []string {
	var chunks []string
	runes := []rune(content)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}
