package relay

import (
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"html/template"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ehrlich-b/read/internal/embedding"
)

//go:embed templates/*.html
var templateFS embed.FS

type FeedEntry struct {
	URL         string
	Description string
}

type FeedGroup struct {
	Slug    string
	Entries []FeedEntry
}

type FeedSection struct {
	Name   string
	Groups []FeedGroup
}

type Server struct {
	Store    *RelayStore
	Embedder embedding.Embedder
	Feeds    []FeedSection
	mux      *http.ServeMux
	feedTmpl  *template.Template
	postTmpl  *template.Template
	aboutTmpl *template.Template

	anchorsMu    sync.Mutex
	anchorsCache []string
	anchorsTTL   time.Time
}

func NewServer(store *RelayStore) *Server {
	funcMap := template.FuncMap{
		"timeAgo":      timeAgo,
		"slugDisplay":  slugDisplay,
		"displayScore": displayScore,
	}

	feedTmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/base.html", "templates/feed.html"),
	)
	postTmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/base.html", "templates/post.html"),
	)
	aboutTmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/base.html", "templates/about.html"),
	)

	s := &Server{
		Store:     store,
		mux:       http.NewServeMux(),
		feedTmpl:  feedTmpl,
		postTmpl:  postTmpl,
		aboutTmpl: aboutTmpl,
	}

	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/w/all", http.StatusFound)
	})
	s.mux.HandleFunc("GET /about", s.handleAbout)
	s.mux.HandleFunc("GET /w/{slug}", s.handleFeed)
	s.mux.HandleFunc("GET /d/{domain}", s.handleDomainFeed)
	s.mux.HandleFunc("GET /p/{postID}", s.handlePostPage)
	s.mux.HandleFunc("POST /api/post", s.handleCreatePost)
	s.mux.HandleFunc("GET /feed.xml", s.handleRSSFeed)
	s.mux.HandleFunc("GET /w/{slug}/feed.xml", s.handleRSSFeed)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Template data types

type feedData struct {
	Slug        string
	SlugName    string
	Description string
	Sort        string
	PathPrefix  string // "/w/" or "/d/"
	Items       []feedItem
	AllAnchors  []string
	LoggedIn    bool
}

type feedItem struct {
	PostID  string
	Title   string
	Link    string
	Domain  string
	Age     time.Time
	Anchors []string
	Score   int
	Voted   bool
}

type postPageData struct {
	PostID   string
	Title    string
	Summary  string
	Link     string
	Domain   string
	Anchors  []string
	Age      time.Time
	Score    int
	Comments []postComment
	LoggedIn bool
	Voted    bool
}

type postComment struct {
	Content   string
	IsBot     bool
	CreatedAt time.Time
}

// Handlers

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	sortBy := validSort(r.URL.Query().Get("sort"))

	var posts []*SocialEmbedding
	var err error
	var description string

	if slug == "all" {
		posts, err = s.Store.ListAllPosts(sortBy, 100)
		description = "AI-curated technical reading"
	} else {
		anchor, aerr := s.Store.GetSocialEmbeddingBySlug(slug)
		if aerr != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if anchor == nil {
			http.NotFound(w, r)
			return
		}
		posts, err = s.Store.ListPostsByAnchor(anchor.ID, sortBy, 100)
		description = anchor.Text
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	postIDs := make([]string, len(posts))
	for i, p := range posts {
		postIDs[i] = p.ID
	}
	anchorSlugs, _ := s.Store.AnchorSlugsForPosts(postIDs)

	items := make([]feedItem, len(posts))
	for i, p := range posts {
		title, _ := extractTitleSummary(p)
		link := ""
		domain := ""
		if p.Link != nil {
			link = *p.Link
			if u, err := url.Parse(link); err == nil {
				domain = strings.TrimPrefix(u.Hostname(), "www.")
			}
		}
		age := p.CreatedAt
		if p.PublishedAt != nil {
			age = *p.PublishedAt
		}
		items[i] = feedItem{
			PostID:  p.ID,
			Title:   title,
			Link:    link,
			Domain:  domain,
			Age:     age,
			Anchors: anchorSlugs[p.ID],
			Score:   p.Mass,
		}
	}

	data := feedData{
		Slug:        slug,
		SlugName:    slugDisplay(slug),
		Description: description,
		Sort:        sortBy,
		PathPrefix:  "/w/",
		Items:       items,
		AllAnchors:  s.sortedAnchors(),
	}

	if err := s.feedTmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (s *Server) handleDomainFeed(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		http.NotFound(w, r)
		return
	}

	sortBy := validSort(r.URL.Query().Get("sort"))

	posts, err := s.Store.ListPostsByDomain(domain, sortBy, 100)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	postIDs := make([]string, len(posts))
	for i, p := range posts {
		postIDs[i] = p.ID
	}
	anchorSlugs, _ := s.Store.AnchorSlugsForPosts(postIDs)

	items := make([]feedItem, len(posts))
	for i, p := range posts {
		title, _ := extractTitleSummary(p)
		link := ""
		linkDomain := ""
		if p.Link != nil {
			link = *p.Link
			if u, err := url.Parse(link); err == nil {
				linkDomain = strings.TrimPrefix(u.Hostname(), "www.")
			}
		}
		age := p.CreatedAt
		if p.PublishedAt != nil {
			age = *p.PublishedAt
		}
		items[i] = feedItem{
			PostID:  p.ID,
			Title:   title,
			Link:    link,
			Domain:  linkDomain,
			Age:     age,
			Anchors: anchorSlugs[p.ID],
			Score:   p.Mass,
		}
	}

	data := feedData{
		Slug:        domain,
		SlugName:    domain,
		Description: "posts from " + domain,
		Sort:        sortBy,
		PathPrefix:  "/d/",
		Items:       items,
		AllAnchors:  s.sortedAnchors(),
	}

	if err := s.feedTmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (s *Server) handlePostPage(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("postID")
	if postID == "" {
		http.NotFound(w, r)
		return
	}

	post, err := s.Store.GetSocialEmbedding(postID)
	if err != nil || post == nil {
		http.NotFound(w, r)
		return
	}

	title, summary := extractTitleSummary(post)
	link := ""
	domain := ""
	if post.Link != nil {
		link = *post.Link
		if u, err := url.Parse(link); err == nil {
			domain = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	age := post.CreatedAt
	if post.PublishedAt != nil {
		age = *post.PublishedAt
	}

	anchors, _ := s.Store.AnchorSlugsForPosts([]string{postID})
	comments, _ := s.Store.ListCommentsByPost(postID)

	var pcs []postComment
	for _, c := range comments {
		pcs = append(pcs, postComment{
			Content:   c.Content,
			IsBot:     c.IsBot,
			CreatedAt: c.CreatedAt,
		})
	}

	data := postPageData{
		PostID:   postID,
		Title:    title,
		Summary:  summary,
		Link:     link,
		Domain:   domain,
		Anchors:  anchors[postID],
		Age:      age,
		Score:    post.Mass,
		Comments: pcs,
	}

	if err := s.postTmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (s *Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	if s.Embedder == nil {
		http.Error(w, "embedder not configured", 500)
		return
	}

	var req struct {
		Text  string `json:"text"`
		Title string `json:"title"`
		Link  string `json:"link"`
		Mass  int    `json:"mass"`
		Date  string `json:"date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	params := PostParams{
		UserID: "api",
		Text:   req.Text,
		Title:  req.Title,
		Link:   req.Link,
		Mass:   req.Mass,
	}
	if req.Date != "" {
		if t, err := time.Parse(time.RFC3339, req.Date); err == nil {
			params.PublishedAt = &t
		}
	}

	post, err := CreatePost(s.Store, s.Embedder, params)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": post.ID})
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	totalFeeds := 0
	for _, sec := range s.Feeds {
		for _, g := range sec.Groups {
			totalFeeds += len(g.Entries)
		}
	}
	data := struct {
		Sections   []FeedSection
		TotalFeeds int
	}{s.Feeds, totalFeeds}
	if err := s.aboutTmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// RSS feed types

type rssChannel struct {
	XMLName     xml.Name  `xml:"channel"`
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Language    string    `xml:"language"`
	PubDate     string    `xml:"pubDate,omitempty"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate,omitempty"`
	GUID        string `xml:"guid"`
}

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

func (s *Server) handleRSSFeed(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		slug = "all"
	}

	var posts []*SocialEmbedding
	var err error
	var desc string

	if slug == "all" {
		posts, err = s.Store.ListAllPosts("new", 50)
		desc = "AI-curated technical reading from 1500+ RSS feeds"
	} else {
		anchor, aerr := s.Store.GetSocialEmbeddingBySlug(slug)
		if aerr != nil || anchor == nil {
			http.NotFound(w, r)
			return
		}
		posts, err = s.Store.ListPostsByAnchor(anchor.ID, "new", 50)
		desc = anchor.Text
	}
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	items := make([]rssItem, 0, len(posts))
	for _, p := range posts {
		title, summary := extractTitleSummary(p)
		link := "https://read.ehrlich.dev/p/" + p.ID
		if p.Link != nil && *p.Link != "" {
			link = *p.Link
		}
		pubDate := p.CreatedAt
		if p.PublishedAt != nil {
			pubDate = *p.PublishedAt
		}
		items = append(items, rssItem{
			Title:       title,
			Link:        link,
			Description: summary,
			PubDate:     pubDate.UTC().Format(time.RFC1123Z),
			GUID:        "https://read.ehrlich.dev/p/" + p.ID,
		})
	}

	feedTitle := "read.ehrlich.dev"
	feedLink := "https://read.ehrlich.dev/w/all"
	if slug != "all" {
		feedTitle = slugDisplay(slug) + " - read.ehrlich.dev"
		feedLink = "https://read.ehrlich.dev/w/" + slug
	}

	doc := rssDoc{
		Version: "2.0",
		Channel: rssChannel{
			Title:       feedTitle,
			Link:        feedLink,
			Description: desc,
			Language:    "en",
			Items:       items,
		},
	}
	if len(items) > 0 {
		doc.Channel.PubDate = items[0].PubDate
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	enc.Encode(doc)
}

// ParseFeeds parses a feeds.md file into structured sections.
func ParseFeeds(content string) []FeedSection {
	var sections []FeedSection
	var curSection *FeedSection
	var curGroup *FeedGroup

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			if curGroup != nil && curSection != nil {
				curSection.Groups = append(curSection.Groups, *curGroup)
				curGroup = nil
			}
			if curSection != nil {
				sections = append(sections, *curSection)
			}
			curSection = &FeedSection{Name: strings.TrimPrefix(line, "## ")}
			continue
		}
		if strings.HasPrefix(line, "### ") {
			if curGroup != nil && curSection != nil {
				curSection.Groups = append(curSection.Groups, *curGroup)
			}
			curGroup = &FeedGroup{Slug: strings.TrimPrefix(line, "### ")}
			continue
		}
		if strings.HasPrefix(line, "- http") && curGroup != nil {
			entry := strings.TrimPrefix(line, "- ")
			fe := FeedEntry{}
			if idx := strings.Index(entry, "  #"); idx != -1 {
				fe.URL = strings.TrimSpace(entry[:idx])
				fe.Description = strings.TrimSpace(entry[idx+3:])
			} else if idx := strings.Index(entry, " #"); idx != -1 {
				fe.URL = strings.TrimSpace(entry[:idx])
				fe.Description = strings.TrimSpace(entry[idx+2:])
			} else {
				fe.URL = strings.TrimSpace(entry)
			}
			curGroup.Entries = append(curGroup.Entries, fe)
		}
	}
	if curGroup != nil && curSection != nil {
		curSection.Groups = append(curSection.Groups, *curGroup)
	}
	if curSection != nil {
		sections = append(sections, *curSection)
	}
	return sections
}

func (s *Server) sortedAnchors() []string {
	s.anchorsMu.Lock()
	defer s.anchorsMu.Unlock()

	if time.Now().Before(s.anchorsTTL) && s.anchorsCache != nil {
		return s.anchorsCache
	}

	slugs, err := s.Store.AnchorSlugsByWeight()
	if err != nil {
		log.Printf("anchor slugs by weight: %v", err)
		return s.anchorsCache
	}
	s.anchorsCache = slugs
	s.anchorsTTL = time.Now().Add(5 * time.Minute)
	return slugs
}

// Helpers

func validSort(s string) string {
	switch s {
	case "new", "week", "month", "year":
		return s
	default:
		return "hot"
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTMLTitle(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

func extractTitleSummary(p *SocialEmbedding) (title, summary string) {
	if p.Title != nil && *p.Title != "" && len(*p.Title) < 300 {
		title = stripHTMLTitle(*p.Title)
		summary = p.Text
		if strings.HasPrefix(summary, "[") {
			if idx := strings.Index(summary, "]\n"); idx != -1 {
				summary = strings.TrimSpace(summary[idx+2:])
			} else if idx := strings.Index(summary, "] "); idx != -1 {
				summary = strings.TrimSpace(summary[idx+2:])
			}
		}
		return title, summary
	}

	if strings.HasPrefix(p.Text, "[") {
		if idx := strings.Index(p.Text, "]\n"); idx != -1 {
			title = p.Text[1:idx]
			summary = strings.TrimSpace(p.Text[idx+2:])
			return title, summary
		}
		if idx := strings.Index(p.Text, "] "); idx != -1 {
			title = p.Text[1:idx]
			summary = strings.TrimSpace(p.Text[idx+2:])
			return title, summary
		}
	}

	if idx := strings.Index(p.Text, ". "); idx != -1 && idx < 200 {
		title = p.Text[:idx+1]
		summary = p.Text
	} else {
		title = p.Text
		if len(title) > 100 {
			title = title[:100] + "..."
		}
	}
	return title, summary
}

func displayScore(mass int) int {
	if mass <= 0 {
		return 3
	}
	// log2 scale: 12→3, 42→5, 85→6, 312→8, 850→9, 10000→10
	s := int(math.Log2(float64(mass)))
	if s < 3 {
		s = 3
	}
	if s > 10 {
		s = 10
	}
	return s
}

func slugDisplay(slug string) string {
	return strings.ReplaceAll(slug, "-", " ")
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		months := int(d.Hours() / 24 / 30)
		if months <= 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	}
}
