package main

import (
	"bufio"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ehrlich-b/read/internal/embedding"
	"github.com/ehrlich-b/read/internal/relay"
)

// RSS/Atom types

type Feed struct {
	Channel struct {
		Items []RSSItem `xml:"item"`
	} `xml:"channel"`
	Entries []AtomEntry `xml:"entry"`
}

type RSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

type AtomEntry struct {
	Title     string     `xml:"title"`
	Links     []AtomLink `xml:"link"`
	Content   string     `xml:"content"`
	Summary   string     `xml:"summary"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

type AtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type FeedSource struct {
	URL     string
	Source  string
	Comment string
}

type Article struct {
	Title  string
	Link   string
	Text   string
	Date   string
	Source string
}

func parseFeedsMD(content string) []FeedSource {
	var feeds []FeedSource
	var currentSpace string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "### ") {
			currentSpace = strings.TrimPrefix(line, "### ")
		} else if strings.HasPrefix(line, "- http") {
			u := strings.TrimPrefix(line, "- ")
			comment := ""
			if idx := strings.Index(u, "  #"); idx > 0 {
				comment = strings.TrimSpace(u[idx+3:])
				u = strings.TrimSpace(u[:idx])
			}
			source := currentSpace
			if comment != "" {
				if dash := strings.Index(comment, " — "); dash > 0 {
					source = comment[:dash]
				} else {
					source = comment
				}
			}
			feeds = append(feeds, FeedSource{URL: u, Source: source, Comment: comment})
		}
	}
	return feeds
}

func parseDate(s string) string {
	if s == "" {
		return ""
	}
	formats := []string{
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 MST",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return ""
}

func fetchFeed(feedURL string) ([]Article, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var feed Feed
	xml.Unmarshal(data, &feed)

	var articles []Article

	for _, item := range feed.Channel.Items {
		if item.Title == "" || item.Link == "" {
			continue
		}
		desc := stripTags(item.Description)
		if len(desc) > 800 {
			desc = desc[:800]
		}
		articles = append(articles, Article{
			Title: item.Title, Link: item.Link, Text: desc,
			Date: parseDate(item.PubDate),
		})
	}

	for _, entry := range feed.Entries {
		if entry.Title == "" {
			continue
		}
		link := ""
		for _, l := range entry.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		if link == "" && len(entry.Links) > 0 {
			link = entry.Links[0].Href
		}
		text := entry.Content
		if text == "" {
			text = entry.Summary
		}
		text = stripTags(text)
		if len(text) > 800 {
			text = text[:800]
		}
		date := entry.Published
		if date == "" {
			date = entry.Updated
		}
		articles = append(articles, Article{
			Title: entry.Title, Link: link, Text: text,
			Date: parseDate(date),
		})
	}

	return articles, nil
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "&amp;", "&")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	out = strings.ReplaceAll(out, "&quot;", "\"")
	out = strings.ReplaceAll(out, "&#39;", "'")
	out = strings.ReplaceAll(out, "&#8217;", "'")
	out = strings.ReplaceAll(out, "&#8220;", "\"")
	out = strings.ReplaceAll(out, "&#8221;", "\"")
	out = strings.ReplaceAll(out, "&rsquo;", "'")
	out = strings.ReplaceAll(out, "&ldquo;", "\"")
	out = strings.ReplaceAll(out, "&rdquo;", "\"")
	out = strings.ReplaceAll(out, "&mdash;", "—")
	out = strings.ReplaceAll(out, "&ndash;", "–")
	out = strings.ReplaceAll(out, "\n", " ")
	out = strings.ReplaceAll(out, "\t", " ")
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	return strings.TrimSpace(out)
}

func urlSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host + u.Path
}

var paywallRe = regexp.MustCompile(`(?i)subscribe to read|sign in to continue|premium article|members-only|create a free account|register to read|exclusive to subscribers`)

var botRefusalRe = regexp.MustCompile(`(?i)I need (permission|to see|to fetch|the article|the full|more|your permission|approval)|I don't have access|unable to access|sign in to read|subscribe to continue|paywall|members only|premium content|login required|403 Forbidden|access denied|couldn't retrieve|I'd be happy to help.*(but|however)|Could you (provide|paste|share)|Since that needs approval|Let me do the compression|Actually, let me just`)

func isPaywall(text string) bool {
	return paywallRe.MatchString(text)
}

func isBotRefusal(text string) bool {
	return botRefusalRe.MatchString(text)
}

func compress(title, source, text string) (string, error) {
	prompt := fmt.Sprintf(`Compress this article excerpt into a dense, informative summary of max 800 characters. Include the key insight or finding. Start with the article title and source in brackets. No preamble.

Title: %s
Source: %s`, title, source)

	cmd := exec.Command("claude", "-p", prompt, "--model", "claude-haiku-4-5-20251001")
	cmd.Stdin = strings.NewReader(text)
	// Remove CLAUDECODE env var
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func score(title, source, text, scorerPrompt string) (int, error) {
	input := fmt.Sprintf("Title: %s\nSource: %s\n\n%s", title, source, text)

	cmd := exec.Command("claude", "-p", scorerPrompt, "--model", "claude-haiku-4-5-20251001")
	cmd.Stdin = strings.NewReader(input)
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	out, err := cmd.CombinedOutput()
	if err != nil {
		return 10, nil // default on failure
	}

	re := regexp.MustCompile(`SCORE (\d+)`)
	m := re.FindSubmatch(out)
	if m == nil {
		return 10, nil
	}
	mass, err := strconv.Atoi(string(m[1]))
	if err != nil || mass < 1 {
		return 10, nil
	}
	if mass > 1000 {
		mass = 1000
	}
	return mass, nil
}

func loadScorerPrompt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read scorer prompt: %v", err)
	}
	content := string(data)
	// Strip YAML frontmatter (between --- delimiters)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			content = strings.TrimSpace(parts[2])
		}
	}
	return content
}

func fetchCmd(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	feeds := fs.String("feeds", "", "Path to feeds.md (default: embedded)")
	fs.Parse(args)

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	// Parse feeds
	var feedContent string
	if *feeds != "" {
		data, err := os.ReadFile(*feeds)
		if err != nil {
			log.Fatalf("read feeds: %v", err)
		}
		feedContent = string(data)
	} else {
		feedContent = feedsMD
	}
	sources := parseFeedsMD(feedContent)
	fmt.Fprintf(os.Stderr, "loaded %d feeds\n", len(sources))

	// Fetch concurrently
	type result struct {
		source   string
		articles []Article
	}
	results := make(chan result, len(sources))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup

	for _, f := range sources {
		wg.Add(1)
		go func(f FeedSource) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			articles, err := fetchFeed(f.URL)
			if err != nil {
				return
			}
			n := 10
			if n > len(articles) {
				n = len(articles)
			}
			for i := range articles[:n] {
				articles[i].Source = f.Source
			}
			results <- result{source: f.Source, articles: articles[:n]}
		}(f)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	cutoff := time.Now().AddDate(0, 0, -30)
	seen := make(map[string]bool)
	var inserted, dupes, old, short, alreadyPosted int

	for r := range results {
		for _, a := range r.articles {
			// Skip old
			if a.Date != "" {
				t, err := time.Parse(time.RFC3339, a.Date)
				if err == nil && t.Before(cutoff) {
					old++
					continue
				}
			}
			// Skip dupes within batch
			slug := urlSlug(a.Link)
			if slug != "" && seen[slug] {
				dupes++
				continue
			}
			if slug != "" {
				seen[slug] = true
			}
			// Skip too short
			if len(a.Text) < 100 {
				short++
				continue
			}
			// Skip already posted
			exists, err := store.LinkExistsInPosts(a.Link)
			if err == nil && exists {
				alreadyPosted++
				continue
			}
			// Insert (dupes silently ignored by INSERT OR IGNORE)
			err = store.InsertPipelineArticle(a.Link, a.Title, a.Source, a.Text, a.Date)
			if err != nil {
				fmt.Fprintf(os.Stderr, "insert error: %v\n", err)
				continue
			}
			inserted++
		}
	}

	fmt.Fprintf(os.Stderr, "fetched %d new from %d feeds (%d dupes, %d old, %d short, %d already posted)\n",
		inserted, len(sources), dupes, old, short, alreadyPosted)
}

func processCmd(args []string) {
	fs := flag.NewFlagSet("process", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	limit := fs.Int("limit", 0, "Max articles to process (0 = all)")
	dryRun := fs.Bool("dry-run", false, "Preview without processing")
	scorer := fs.String("scorer", "skills/scorer.md", "Path to scorer prompt")
	fs.Parse(args)

	store, err := relay.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	articles, err := store.ListPendingArticles(*limit)
	if err != nil {
		log.Fatalf("list pending: %v", err)
	}

	if len(articles) == 0 {
		fmt.Fprintf(os.Stderr, "no pending articles\n")
		return
	}

	if *dryRun {
		for _, a := range articles {
			fmt.Printf("[%d] %s: %s (%s)\n", a.ID, a.Source, a.Title, a.Link)
		}
		fmt.Fprintf(os.Stderr, "%d pending articles\n", len(articles))
		return
	}

	scorerPrompt := loadScorerPrompt(*scorer)

	// Init embedder once
	emb, err := embedding.NewFromProvider("auto", "", "")
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}

	var compressed, posted, skipped int
	total := len(articles)

	for i, a := range articles {
		fmt.Fprintf(os.Stderr, "\r[%d/%d] compressed: %d posted: %d skipped: %d", i+1, total, compressed, posted, skipped)

		// Paywall check
		if isPaywall(a.RawText) {
			store.UpdateArticleStatus(a.ID, "skipped", "paywall", "", 0)
			skipped++
			continue
		}

		// Compress
		comp, err := compress(a.Title, a.Source, a.RawText)
		if err != nil || comp == "" {
			store.UpdateArticleStatus(a.ID, "skipped", "compression_failed", "", 0)
			skipped++
			continue
		}

		// Bot refusal check
		if isBotRefusal(comp) {
			store.UpdateArticleStatus(a.ID, "skipped", "refusal", "", 0)
			skipped++
			continue
		}

		// Truncate to 1024 chars
		if len(comp) > 1024 {
			comp = comp[:1024]
		}

		// Score
		mass, err := score(a.Title, a.Source, a.RawText, scorerPrompt)
		if err != nil {
			mass = 10
		}

		// Update row with compressed data
		store.UpdateArticleStatus(a.ID, "compressed", "", comp, mass)
		compressed++

		// Create post directly
		var pubAt *time.Time
		if a.PublishedAt != "" {
			t, err := time.Parse(time.RFC3339, a.PublishedAt)
			if err != nil {
				t, err = time.Parse("2006-01-02", a.PublishedAt)
			}
			if err == nil {
				pubAt = &t
			}
		}

		params := relay.PostParams{
			UserID:      "pipeline",
			Text:        comp,
			Title:       a.Title,
			Link:        a.Link,
			Mass:        mass,
			PublishedAt: pubAt,
		}

		post, err := relay.CreatePost(store, emb, params)
		if err != nil {
			store.UpdateArticleStatus(a.ID, "skipped", fmt.Sprintf("post_error: %v", err), comp, mass)
			skipped++
			continue
		}

		// Check if it was a dupe (CreatePost returns existing post on URL match)
		if post.Link != nil && *post.Link == a.Link {
			// Could be new or existing -- check if the post ID was just created
			// by seeing if the text matches what we just sent
			if post.Text != comp {
				store.UpdateArticleStatus(a.ID, "skipped", "already_posted", comp, mass)
				skipped++
				continue
			}
		}

		store.UpdateArticleStatus(a.ID, "posted", "", comp, mass)
		posted++
	}

	fmt.Fprintf(os.Stderr, "\r[%d/%d] compressed: %d posted: %d skipped: %d\n", total, total, compressed, posted, skipped)
}
