package scraper

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/robfig/cron/v3"
	"github.com/user/azimuthal-belt/backend/internal/learner"
)

var CWEConfig struct {
	MaxID   int
	DataDir string
}

func init() {
	CWEConfig.MaxID = 1450 // rough upper limit for MITRE CWEs
	CWEConfig.DataDir = "./data"
}

func StartCWEScraperCron() {
	c := cron.New()

	// Run once a week on Sunday at 1am
	_, err := c.AddFunc("0 1 * * 0", func() {
		log.Println("[CWE Scraper] Starting weekly scrape...")
		err := RunCWEScraper(CWEConfig.MaxID)
		if err != nil {
			log.Printf("[CWE Scraper] Error: %v\n", err)
		} else {
			log.Println("[CWE Scraper] Finished weekly scrape.")
		}
	})

	if err != nil {
		log.Fatalf("Failed to start CWE cron: %v", err)
	}

	c.Start()
	log.Println("[CWE Scraper] Cron scheduler started.")
}

func RunCWEScraper(maxID int) error {
	store, err := learner.NewStore(CWEConfig.DataDir)
	if err != nil {
		return fmt.Errorf("failed to init learner store: %v", err)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	pulled := 0

	for id := 1; id <= maxID; id++ {
		cweID := fmt.Sprintf("CWE-%d", id)

		// Check if we already have it
		if !store.NeedsLearningCWE(cweID) {
			continue
		}

		pageURL := fmt.Sprintf("https://cwe.mitre.org/data/definitions/%d.html", id)

		req, err := http.NewRequest("GET", pageURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")

		res, err := client.Do(req)
		if err != nil {
			log.Printf("[CWE Scraper] Error fetching %s: %v\n", cweID, err)
			continue
		}

		if res.StatusCode == 404 {
			res.Body.Close()
			continue // Unused or deprecated ID
		}

		if res.StatusCode != 200 {
			log.Printf("[CWE Scraper] Unusual status %d for %s\n", res.StatusCode, cweID)
			res.Body.Close()
			continue
		}

		doc, err := goquery.NewDocumentFromReader(res.Body)
		res.Body.Close()
		if err != nil {
			log.Printf("[CWE Scraper] Failed to parse page for %s: %v\n", cweID, err)
			continue
		}

		// Extract Title (usually in an h2 tag)
		titleText := doc.Find("h2").First().Text()
		parts := strings.SplitN(titleText, ":", 2)
		name := titleText
		if len(parts) == 2 {
			name = strings.TrimSpace(parts[1])
		} else {
			name = strings.TrimSpace(titleText)
		}

		// Cleanup common noise from title (e.g. CWE-79: Improper... (4.19.1))
		name = strings.Split(name, "(")[0]
		name = strings.TrimSpace(name)

		if name == "" {
			continue
		}

		// Extract Description
		// Often in a div with id="Description" or under a section
		desc := doc.Find("#Description .detail").Text()
		if desc == "" {
			desc = doc.Find(".detail").First().Text() // fallback
		}
		desc = strings.TrimSpace(desc)

		// Extract Mode of Intro / Class mapping
		// Usually under #Modes_Of_Introduction or #Related_Weaknesses
		classInfo := ""
		doc.Find("#Related_Weaknesses table tr").Each(func(i int, s *goquery.Selection) {
			nature := s.Find("td").Eq(0).Text()
			if strings.Contains(strings.ToLower(nature), "childof") || strings.Contains(strings.ToLower(nature), "memberof") {
				parentID := s.Find("td").Eq(1).Text()
				if classInfo == "" {
					classInfo = parentID
				} else {
					classInfo += ", " + parentID
				}
			}
		})
		classInfo = strings.TrimSpace(classInfo)

		// Extract Mitigations
		var mitigations []string
		doc.Find("#Potential_Mitigations .detail").Each(func(i int, s *goquery.Selection) {
			mit := strings.TrimSpace(s.Text())
			if mit != "" {
				mitigations = append(mitigations, mit)
			}
		})

		cweDef := learner.CWEDefinition{
			ID:            cweID,
			Name:          name,
			Description:   desc,
			Class:         classInfo,
			Mitigations:   mitigations,
			URL:           pageURL,
			LastUpdated:   time.Now().Format(time.RFC3339),
			Source:        "MITRE CWE",
			Version:       "latest",
			ParseWarnings: []string{},
		}

		if err := store.SaveCWE(cweDef); err == nil {
			log.Printf("[CWE Scraper] Learned %s: %s\n", cweID, name)
			pulled++
		}

		// Be polite
		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("[CWE Scraper] Routine finished. Added %d new definitions.\n", pulled)
	return nil
}
