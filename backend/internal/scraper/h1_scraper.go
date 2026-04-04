package scraper

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"
	"github.com/robfig/cron/v3"
	"github.com/user/azimuthal-belt/backend/internal/learner"
)

var Config struct {
	MaxPulls int
	DataDir  string
}

func init() {
	Config.MaxPulls = 15
	Config.DataDir = "./data"
}

func StartH1ScraperCron() {
	c := cron.New()

	// Run once a week on Sunday at midnight
	_, err := c.AddFunc("@weekly", func() {
		log.Println("[H1 Scraper] Starting weekly scrape...")
		err := RunH1Scraper(Config.MaxPulls)
		if err != nil {
			log.Printf("[H1 Scraper] Error: %v\n", err)
		} else {
			log.Println("[H1 Scraper] Finished weekly scrape.")
		}
	})

	if err != nil {
		log.Fatalf("Failed to start cron: %v", err)
	}

	c.Start()
	log.Println("[H1 Scraper] Cron scheduler started.")
}

func RunH1Scraper(maxPulls int) error {
	store, err := learner.NewStore(Config.DataDir)
	if err != nil {
		return fmt.Errorf("failed to init learner store: %v", err)
	}

	u := launcher.New().
		Set("disable-blink-features", "AutomationControlled").
		Headless(true).MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()
	defer browser.MustClose()

	startIndex := 0
	pulled := 0

	for pulled < maxPulls {
		pageURL := fmt.Sprintf("http://h1.nobbd.de/index.php?start=%d", startIndex)
		log.Printf("[H1 Scraper] Fetching directory page %s\n", pageURL)

		res, err := http.Get(pageURL)
		if err != nil {
			log.Printf("[H1 Scraper] Failed to fetch directory page: %v", err)
			break
		}

		doc, err := goquery.NewDocumentFromReader(res.Body)
		res.Body.Close()
		if err != nil {
			log.Printf("[H1 Scraper] Failed to parse directory page: %v", err)
			break
		}

		var reportURLs []string
		doc.Find("div.report a.title").Each(func(i int, s *goquery.Selection) {
			href, exists := s.Attr("href")
			if exists && strings.HasPrefix(href, "https://hackerone.com/reports/") {
				reportURLs = append(reportURLs, href)
			}
		})

		if len(reportURLs) == 0 {
			log.Println("No more reports found on directory.")
			break
		}

		for _, reportURL := range reportURLs {
			if pulled >= maxPulls {
				break
			}

			// Extract ID from URL
			idRegex := regexp.MustCompile(`/reports/(\d+)`)
			match := idRegex.FindStringSubmatch(reportURL)
			if len(match) < 2 {
				continue
			}
			reportID := match[1]

			if !store.NeedsLearning(reportID) {
				log.Printf("[H1 Scraper] Already learned report %s, skipping.\n", reportID)
				continue
			}

			log.Printf("[H1 Scraper] [%d/%d] Scraping report: %s\n", pulled+1, maxPulls, reportURL)

			rPage := stealth.MustPage(browser)
			err = rPage.Timeout(15 * time.Second).Navigate(reportURL)
			if err != nil {
				log.Printf("[H1 Scraper] Timeout navigating to report %s", reportURL)
				rPage.MustClose()
				continue
			}

			// Simple extraction logic, H1 DOM can change often
			// Wait for the timeline to load
			err = rPage.Timeout(10*time.Second).WaitElementsMoreThan(".timeline-entry", 0)
			if err != nil {
				log.Printf("[H1 Scraper] Timeline didn't load for report %s", reportURL)
			}

			titleElem, err := rPage.Timeout(5 * time.Second).Element("h1")
			title := ""
			if err == nil {
				title = titleElem.MustText()
			}

			vulnDetails := ""
			// Just grab all markdown bodies as a rudimentary PoC/details extraction
			mdBodies, err := rPage.Timeout(5 * time.Second).Elements(".markdown-body")
			if err == nil {
				for _, md := range mdBodies {
					vulnDetails += md.MustText() + "\n\n"
				}
			}

			if title != "" && vulnDetails != "" {
				report := learner.VulnerabilityReport{
					ID:            reportID,
					Title:         strings.TrimSpace(title),
					BugType:       "Unknown", // Needs more complex DOM traversal
					Severity:      "Unknown",
					PoC:           strings.TrimSpace(vulnDetails),
					URL:           reportURL,
					LastUpdated:   time.Now().Format(time.RFC3339),
					Source:        "HackerOne",
					Version:       "latest",
					ParseWarnings: []string{},
				}
				_ = store.SaveReport(report)
				log.Printf("[H1 Scraper] Learned report %s.\n", reportID)
				pulled++
			} else {
				log.Printf("[H1 Scraper] Extracted empty title or details for %s, possibly blocked", reportID)
			}

			rPage.MustClose()
			time.Sleep(2 * time.Second) // Be polite to H1
		}

		startIndex += 20
	}

	return nil
}
