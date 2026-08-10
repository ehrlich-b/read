package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ehrlich-b/read/internal/embedding"
	"github.com/ehrlich-b/read/internal/llm"
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

const claudeModel = "claude-haiku-4-5-20251001"

// backend picks the compression/scoring backend: the claude CLI or an
// OpenAI-compatible chat-completions endpoint.
type backend struct {
	kind   string // "claude" or "openai"
	client *llm.Client
}

func newBackend(kind string) (*backend, error) {
	switch kind {
	case "claude":
		return &backend{kind: "claude"}, nil
	case "openai":
		client, err := newOpenAIClient()
		if err != nil {
			return nil, err
		}
		return &backend{kind: "openai", client: client}, nil
	default:
		return nil, fmt.Errorf("unknown llm backend %q (available: claude, openai)", kind)
	}
}

func newOpenAIClient() (*llm.Client, error) {
	apiKey := os.Getenv("READ_LLM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("READ_LLM_API_KEY not set")
	}
	if model := os.Getenv("READ_LLM_MODEL"); model == "" {
		return nil, fmt.Errorf("READ_LLM_MODEL not set")
	}
	return llm.New(os.Getenv("READ_LLM_BASE_URL"), os.Getenv("READ_LLM_MODEL"), apiKey), nil
}

// compressInstructions is the compression rule text shared by the single-article
// prompt and the batched (--batch N) system prompt, so both paths apply the same
// rules.
const compressInstructions = "Compress this article excerpt into a dense, informative summary of max 800 characters. Include the key insight or finding. Start with the article title and source in brackets. No preamble."

func compressPrompt(title, source string) string {
	return fmt.Sprintf("%s\n\nTitle: %s\nSource: %s", compressInstructions, title, source)
}

func scoreInput(title, source, text string) string {
	return fmt.Sprintf("Title: %s\nSource: %s\n\n%s", title, source, text)
}

func (b *backend) compress(title, source, text string) (string, error) {
	prompt := compressPrompt(title, source)
	if b.kind != "openai" {
		// claude CLI: prompt via -p, article text via stdin
		cmd := exec.Command("claude", "-p", prompt, "--model", claudeModel)
		cmd.Stdin = strings.NewReader(text)
		cmd.Env = stripClaudeCodeEnv()
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	// OpenAI-compatible: single user message carrying the same prompt + article text
	content, err := b.client.ChatSingle(prompt + "\n\n" + text)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}

func (b *backend) score(title, source, text, scorerPrompt string) (int, error) {
	input := scoreInput(title, source, text)
	var content string
	if b.kind != "openai" {
		// claude CLI: scorer prompt via -p, article via stdin
		cmd := exec.Command("claude", "-p", scorerPrompt, "--model", claudeModel)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = stripClaudeCodeEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 10, nil // default on failure
		}
		content = string(out)
	} else {
		out, err := b.client.ChatSingle(scorerPrompt + "\n\n" + input)
		if err != nil {
			return 10, nil // default on failure
		}
		content = out
	}
	return parseScore([]byte(content)), nil
}

// batchResult is one element of the JSON array returned by a batched
// compress+score request.
type batchResult struct {
	ID      int    `json:"id"`
	Summary string `json:"summary"`
	Score   int    `json:"score"`
}

// batchSystemPrompt states the batch task once: for every article the model
// must produce a compressed summary (same rules as the single-article compress
// prompt) AND a quality score (same rules/range as the scorer prompt), emitted
// as one JSON object per article.
func batchSystemPrompt(scorerPrompt string) string {
	return fmt.Sprintf(`For EACH article in the user message you must produce BOTH:

1. A compressed summary following these rules:
%s

2. A quality score following these rules and the same 1-1000 scoring range:
%s

Note: in the scoring rules above, ignore single-article output-format instructions such as "SCORE <number>" or "Article to score:" — the required output format is overridden below.

Respond with ONLY a JSON array containing one object per article, no other text:
[{"id": <article id>, "summary": "<compressed summary>", "score": <integer 1-1000>}]`, compressInstructions, scorerPrompt)
}

// batchUserContent packs up to N articles into one user message. Each article
// is headed by an unambiguous "=== ARTICLE <id> ===" marker and separated by a
// delimiter. Each article's text is sent the same way the single-article path
// sends it (the raw stored text).
func batchUserContent(articles []relay.PipelineArticle) string {
	var b strings.Builder
	for i, a := range articles {
		if i > 0 {
			b.WriteString("\n\n=====NEXT ARTICLE=====\n\n")
		}
		fmt.Fprintf(&b, "=== ARTICLE %d ===\n", a.ID)
		fmt.Fprintf(&b, "Title: %s\n", a.Title)
		fmt.Fprintf(&b, "Source: %s\n", a.Source)
		fmt.Fprintf(&b, "Text: %s", a.RawText)
	}
	return b.String()
}

// stripJSONFences removes a surrounding markdown code fence (``` or ```json)
// from an LLM reply, if present.
func stripJSONFences(content []byte) []byte {
	s := strings.TrimSpace(string(content))
	if !strings.HasPrefix(s, "```") {
		return []byte(s)
	}
	// Drop the opening fence line.
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[idx+1:]
	} else {
		s = ""
	}
	// Drop a trailing fence line if present.
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return []byte(strings.TrimSpace(s))
}

// parseBatchResponse strictly parses the JSON array returned for a batch. Pairing
// is by article id: results with an unknown id are ignored (logged), duplicate
// ids keep only the first occurrence (logged), and the score is clamped/defaulted
// with the same semantics as parseScore. A batchResult is only produced for ids
// that were in the request; the caller decides what to do for ids it expected but
// did not receive (they are left pending).
func parseBatchResponse(content []byte, articles []relay.PipelineArticle) (map[int]*batchResult, error) {
	var results []batchResult
	if err := json.Unmarshal(stripJSONFences(content), &results); err != nil {
		return nil, fmt.Errorf("parse batch response: %w", err)
	}
	if results == nil {
		return nil, fmt.Errorf("parse batch response: empty JSON array")
	}

	expected := make(map[int]bool, len(articles))
	for _, a := range articles {
		expected[a.ID] = true
	}

	out := make(map[int]*batchResult, len(articles))
	for i := range results {
		r := &results[i]
		if !expected[r.ID] {
			log.Printf("batch: ignoring result for unknown article id %d", r.ID)
			continue
		}
		if _, dup := out[r.ID]; dup {
			log.Printf("batch: ignoring duplicate result for article id %d", r.ID)
			continue
		}
		// Same clamp/default semantics as parseScore.
		if r.Score < 1 {
			r.Score = 10
		}
		if r.Score > 1000 {
			r.Score = 1000
		}
		out[r.ID] = r
	}
	return out, nil
}

// batchCompressScore sends the whole batch as a single request. If the reply is
// malformed JSON, the request is retried once; if it is still malformed an error
// is returned so the caller leaves every article in the batch pending.
func (b *backend) batchCompressScore(articles []relay.PipelineArticle, scorerPrompt string) (map[int]*batchResult, error) {
	if b.kind != "openai" {
		return nil, fmt.Errorf("batch processing requires the openai backend")
	}
	system := batchSystemPrompt(scorerPrompt)
	user := batchUserContent(articles)

	var (
		content string
		err     error
	)
	for attempt := 1; attempt <= 2; attempt++ {
		content, err = b.client.ChatJSON(system, user)
		if err != nil {
			// Request-level failure (network/API): leave the whole batch pending.
			return nil, fmt.Errorf("batch request failed: %w", err)
		}
		res, perr := parseBatchResponse([]byte(content), articles)
		if perr == nil {
			return res, nil
		}
		if attempt == 2 {
			return nil, fmt.Errorf("batch response malformed after retry: %v", perr)
		}
		log.Printf("batch: malformed response, retrying whole batch: %v", perr)
	}
	return nil, fmt.Errorf("batch unreachable")
}

func stripClaudeCodeEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func parseScore(out []byte) int {
	re := regexp.MustCompile(`SCORE (\d+)`)
	m := re.FindSubmatch(out)
	if m == nil {
		return 10
	}
	mass, err := strconv.Atoi(string(m[1]))
	if err != nil || mass < 1 {
		return 10
	}
	if mass > 1000 {
		mass = 1000
	}
	return mass
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

func processLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "read", "process.log")
}

func processCmd(args []string) {
	fs := flag.NewFlagSet("process", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	limit := fs.Int("limit", 0, "Max articles to process (0 = all)")
	dryRun := fs.Bool("dry-run", false, "Preview without processing")
	scorer := fs.String("scorer", "skills/scorer.md", "Path to scorer prompt")
	llmFlag := fs.String("llm", "claude", "LLM backend for compress/score: claude or openai")
	batchSize := fs.Int("batch", 1, "Articles per LLM request (1 = one request per article)")
	fs.Parse(args)

	backend, err := newBackend(*llmFlag)
	if err != nil {
		log.Fatalf("llm backend: %v", err)
	}

	// Log to ~/Library/Logs/read/process.log
	logPath := processLogPath()
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("open log: %v", err)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags)
	logger.Printf("process started")
	fmt.Fprintf(os.Stderr, "logging to %s\n", logPath)

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

	if *batchSize > 1 && backend.kind != "openai" {
		fmt.Fprintf(os.Stderr, "warning: --batch is only supported with --llm openai; processing one article per request\n")
	}

	compressed, posted, skipped = processArticles(store, emb, backend, logger, articles, scorerPrompt, *batchSize)

	fmt.Fprintf(os.Stderr, "\r[%d/%d] compressed: %d posted: %d skipped: %d\n", total, total, compressed, posted, skipped)
	logger.Printf("process done: compressed=%d posted=%d skipped=%d", compressed, posted, skipped)
}

// processArticles runs the compress+score+post pipeline over the given articles.
// With batchSize > 1 and an openai backend it packs up to batchSize articles into
// a single request; otherwise it uses the original one-request-per-article path.
func processArticles(store *relay.RelayStore, emb embedding.Embedder, backend *backend, logger *log.Logger, articles []relay.PipelineArticle, scorerPrompt string, batchSize int) (compressed, posted, skipped int) {
	total := len(articles)

	if batchSize > 1 && backend.kind == "openai" {
		for i := 0; i < len(articles); {
			end := i + batchSize
			if end > len(articles) {
				end = len(articles)
			}
			group := articles[i:end]
			i = end
			fmt.Fprintf(os.Stderr, "\r[%d/%d] compressed: %d posted: %d skipped: %d", i, total, compressed, posted, skipped)

			// Per-article paywall filtering stays BEFORE batching.
			var batch []relay.PipelineArticle
			for _, a := range group {
				if isPaywall(a.RawText) {
					store.UpdateArticleStatus(a.ID, "skipped", "paywall", "", 0)
					logger.Printf("SKIP paywall: %s", a.Title)
					skipped++
					continue
				}
				batch = append(batch, a)
			}
			if len(batch) == 0 {
				continue
			}

			results, err := backend.batchCompressScore(batch, scorerPrompt)
			if err != nil {
				logger.Printf("BATCH ERROR (%d articles left pending): %v", len(batch), err)
				continue
			}

			for _, a := range batch {
				res, ok := results[a.ID]
				if !ok {
					// Missing from response: never guess or reassign — leave pending so it retries next run.
					logger.Printf("PENDING missing from batch response: %s", a.Title)
					continue
				}

				comp := strings.TrimSpace(res.Summary)
				if comp == "" {
					store.UpdateArticleStatus(a.ID, "skipped", "compression_failed", "", 0)
					logger.Printf("SKIP compression_failed: %s", a.Title)
					skipped++
					continue
				}

				// Bot refusal check
				if isBotRefusal(comp) {
					store.UpdateArticleStatus(a.ID, "skipped", "refusal", "", 0)
					logger.Printf("SKIP refusal: %s", a.Title)
					skipped++
					continue
				}

				// Truncate to 1024 chars
				if len(comp) > 1024 {
					comp = comp[:1024]
				}

				compressed++
				if finishArticle(store, emb, logger, a, comp, res.Score) {
					posted++
				} else {
					skipped++
				}
			}
		}
		return
	}

	for i, a := range articles {
		fmt.Fprintf(os.Stderr, "\r[%d/%d] compressed: %d posted: %d skipped: %d", i+1, total, compressed, posted, skipped)

		// Paywall check
		if isPaywall(a.RawText) {
			store.UpdateArticleStatus(a.ID, "skipped", "paywall", "", 0)
			logger.Printf("SKIP paywall: %s", a.Title)
			skipped++
			continue
		}

		// Compress
		comp, err := backend.compress(a.Title, a.Source, a.RawText)
		if err != nil || comp == "" {
			store.UpdateArticleStatus(a.ID, "skipped", "compression_failed", "", 0)
			logger.Printf("SKIP compression_failed: %s", a.Title)
			skipped++
			continue
		}

		// Bot refusal check
		if isBotRefusal(comp) {
			store.UpdateArticleStatus(a.ID, "skipped", "refusal", "", 0)
			logger.Printf("SKIP refusal: %s", a.Title)
			skipped++
			continue
		}

		// Truncate to 1024 chars
		if len(comp) > 1024 {
			comp = comp[:1024]
		}

		// Score
		mass, err := backend.score(a.Title, a.Source, a.RawText, scorerPrompt)
		if err != nil {
			mass = 10
		}

		compressed++
		if finishArticle(store, emb, logger, a, comp, mass) {
			posted++
		} else {
			skipped++
		}
	}
	return
}

// finishArticle records the compressed article and creates the post. It returns
// true if the article ended up posted, false if it was skipped downstream.
func finishArticle(store *relay.RelayStore, emb embedding.Embedder, logger *log.Logger, a relay.PipelineArticle, comp string, mass int) bool {
	// Update row with compressed data
	store.UpdateArticleStatus(a.ID, "compressed", "", comp, mass)

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
		logger.Printf("SKIP post_error: %s: %v", a.Title, err)
		return false
	}

	// Check if it was a dupe (CreatePost returns existing post on URL match)
	if post.Link != nil && *post.Link == a.Link {
		if post.Text != comp {
			store.UpdateArticleStatus(a.ID, "skipped", "already_posted", comp, mass)
			logger.Printf("SKIP already_posted: %s", a.Title)
			return false
		}
	}

	store.UpdateArticleStatus(a.ID, "posted", "", comp, mass)
	logger.Printf("POSTED [score=%d] %s: %s (%s) date=%s compressed=%d chars", mass, a.Source, a.Title, a.Link, a.PublishedAt, len(comp))
	return true
}
