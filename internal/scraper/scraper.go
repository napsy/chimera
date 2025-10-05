package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Config controls the scraper behaviour.
type Config struct {
	HTTPClient *http.Client
	Timeout    time.Duration
	MaxItems   int
}

// Scraper fetches documents and extracts structured content.
type Scraper struct {
	client   *http.Client
	maxItems int
}

// Result contains the structured data extracted from a page.
type Result struct {
	SourceURL   string
	Title       string
	Description string
	Headings    []Heading
	Paragraphs  []string
	Links       []Link
	FetchedAt   time.Time
}

// Heading captures a heading and its level.
type Heading struct {
	Level int
	Text  string
}

// Link represents a hyperlink discovered during scraping.
type Link struct {
	Text string
	Href string
}

// New creates a new Scraper instance with sensible defaults.
func New(cfg Config) *Scraper {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = 10
	}

	return &Scraper{
		client:   client,
		maxItems: maxItems,
	}
}

// Scrape downloads the specified URL and extracts structured content.
func (s *Scraper) Scrape(ctx context.Context, target string) (*Result, error) {
	if target == "" {
		return nil, errors.New("target URL is empty")
	}

	parsed, err := url.Parse(target)
	if err != nil || !parsed.IsAbs() {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", "ChimeraScraper/0.1 (+https://example.com)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse document: %w", err)
	}

	result := &Result{
		SourceURL: target,
		Title:     strings.TrimSpace(doc.Find("title").First().Text()),
		FetchedAt: time.Now(),
	}

	if metaDesc, ok := doc.Find("meta[name='description']").Attr("content"); ok {
		result.Description = strings.TrimSpace(metaDesc)
	}

	headings := collectHeadings(doc, s.maxItems)
	paragraphs := collectParagraphs(doc, s.maxItems)
	links := collectLinks(parsed, doc, s.maxItems)

	result.Headings = headings
	result.Paragraphs = paragraphs
	result.Links = links

	return result, nil
}

func collectHeadings(doc *goquery.Document, limit int) []Heading {
	var hs []Heading
	for level := 1; level <= 3; level++ {
		selector := fmt.Sprintf("h%d", level)
		doc.Find(selector).Each(func(_ int, sel *goquery.Selection) {
			text := strings.TrimSpace(sel.Text())
			if text == "" {
				return
			}
			hs = append(hs, Heading{Level: level, Text: text})
		})
	}

	if len(hs) > limit {
		hs = hs[:limit]
	}

	return hs
}

func collectParagraphs(doc *goquery.Document, limit int) []string {
	var paragraphs []string
	doc.Find("p").Each(func(_ int, sel *goquery.Selection) {
		text := strings.TrimSpace(sel.Text())
		if len(text) < 40 { // skip very short fragments
			return
		}
		paragraphs = append(paragraphs, text)
	})

	if len(paragraphs) > limit {
		paragraphs = paragraphs[:limit]
	}

	return paragraphs
}

func collectLinks(base *url.URL, doc *goquery.Document, limit int) []Link {
	seen := make(map[string]struct{})
	var links []Link

	doc.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
		href, exists := sel.Attr("href")
		if !exists {
			return
		}

		trimmed := strings.TrimSpace(href)
		if trimmed == "" {
			return
		}

		resolved := trimmed
		parsed, err := base.Parse(trimmed)
		if err == nil {
			resolved = parsed.String()
		}

		if _, ok := seen[resolved]; ok {
			return
		}
		seen[resolved] = struct{}{}

		text := strings.TrimSpace(sel.Text())
		if text == "" {
			text = resolved
		}

		links = append(links, Link{Text: text, Href: resolved})
	})

	if len(links) > limit {
		links = links[:limit]
	}

	sort.SliceStable(links, func(i, j int) bool {
		return links[i].Text < links[j].Text
	})

	return links
}
