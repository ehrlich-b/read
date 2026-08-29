package relay

import (
	"testing"
)

func TestInsertAndListPendingArticles(t *testing.T) {
	s := testStore(t)

	a1 := PipelineArticle{Link: "https://example.com/1", Title: "First", Source: "src-a", RawText: "raw-1", PublishedAt: "2026-01-01"}
	a2 := PipelineArticle{Link: "https://example.com/2", Title: "Second", Source: "src-b", RawText: "raw-2", PublishedAt: "2026-02-02"}
	for _, a := range []PipelineArticle{a1, a2} {
		if err := s.InsertPipelineArticle(a.Link, a.Title, a.Source, a.RawText, a.PublishedAt); err != nil {
			t.Fatalf("insert %q: %v", a.Link, err)
		}
	}

	got, err := s.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 pending articles, got %d", len(got))
	}
	wantFields := []struct{ link, title, source, raw string }{
		{a1.Link, a1.Title, a1.Source, a1.RawText},
		{a2.Link, a2.Title, a2.Source, a2.RawText},
	}
	for i, w := range wantFields {
		if got[i].Link != w.link || got[i].Title != w.title || got[i].Source != w.source || got[i].RawText != w.raw {
			t.Errorf("article %d mismatch: got %+v, want link=%q title=%q source=%q raw=%q",
				i, got[i], w.link, w.title, w.source, w.raw)
		}
	}
}

func TestInsertDuplicateLinkIsIgnoredAndOriginalSurvives(t *testing.T) {
	s := testStore(t)

	const link = "https://example.com/dup"
	firstTitle := "Original Title"
	if err := s.InsertPipelineArticle(link, firstTitle, "src", "raw", ""); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := s.InsertPipelineArticle(link, "Different Title", "other-src", "other-raw", "2026-03-03"); err != nil {
		t.Fatalf("duplicate insert should be a silent no-op, got error: %v", err)
	}

	got, err := s.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var forLink []PipelineArticle
	for _, a := range got {
		if a.Link == link {
			forLink = append(forLink, a)
		}
	}
	if len(forLink) != 1 {
		t.Fatalf("expected exactly 1 row for link %q, got %d", link, len(forLink))
	}
	if forLink[0].Title != firstTitle {
		t.Errorf("original row's title was clobbered: got %q, want %q", forLink[0].Title, firstTitle)
	}
}

// TestListPendingArticlesStatusIsEmpty pins the actual behavior of the
// Status field: ListPendingArticles SELECTs only
// id, link, title, source, raw_text, COALESCE(published_at, '') — six
// columns — and rows.Scan populates only those six struct fields, never
// Status. So a returned article's Status is ALWAYS "" even though its DB
// row has status='fetched'. If the SELECT/Scan pair is ever changed to
// include Status, this assertion will fail and force a decision about what
// the pinned value should become.
func TestListPendingArticlesStatusIsEmpty(t *testing.T) {
	s := testStore(t)

	if err := s.InsertPipelineArticle("https://example.com/status", "S", "src", "raw", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pending article, got %d", len(got))
	}
	if got[0].Status != "" {
		t.Errorf("Status should be empty string because the query never selects it, got %q", got[0].Status)
	}
}

func TestListPendingArticlesLimit(t *testing.T) {
	s := testStore(t)

	links := []string{
		"https://example.com/limit/a",
		"https://example.com/limit/b",
		"https://example.com/limit/c",
	}
	for _, link := range links {
		if err := s.InsertPipelineArticle(link, "Title", "src", "raw", ""); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := s.ListPendingArticles(1)
	if err != nil {
		t.Fatalf("list pending with limit: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 article, got %d", len(got))
	}
	if got[0].Link != "https://example.com/limit/a" {
		t.Errorf("expected the earliest-inserted (smallest id) article, got %q", got[0].Link)
	}
}

func TestUpdateArticleStatusMovesOutOfPending(t *testing.T) {
	s := testStore(t)

	if err := s.InsertPipelineArticle("https://example.com/move", "Title", "src", "raw", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	before, err := s.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 pending article, got %d", len(before))
	}
	id := before[0].ID

	if err := s.UpdateArticleStatus(id, "compressed", "", "some compressed text", 77); err != nil {
		t.Fatalf("update status: %v", err)
	}

	after, err := s.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending after update: %v", err)
	}
	for _, a := range after {
		if a.ID == id {
			t.Errorf("article id=%d still listed as pending after status update", id)
		}
	}
}

func TestPipelineStatsBuckets(t *testing.T) {
	s := testStore(t)

	links := []string{
		"https://example.com/compressed",
		"https://example.com/posted",
		"https://example.com/skipped",
		"https://example.com/fetched",
	}
	for _, l := range links {
		if err := s.InsertPipelineArticle(l, "Title", "src", "raw", ""); err != nil {
			t.Fatalf("insert %q: %v", l, err)
		}
	}

	// ids are 1..4 in insertion order
	if err := s.UpdateArticleStatus(1, "compressed", "", "", 0); err != nil {
		t.Fatalf("update compressed: %v", err)
	}
	if err := s.UpdateArticleStatus(2, "posted", "", "", 0); err != nil {
		t.Fatalf("update posted: %v", err)
	}
	if err := s.UpdateArticleStatus(3, "skipped", "some reason", "", 0); err != nil {
		t.Fatalf("update skipped: %v", err)
	}
	// id 4 left as 'fetched'

	total, fetched, compressed, posted, skipped, err := s.PipelineStats()
	if err != nil {
		t.Fatalf("pipeline stats: %v", err)
	}
	if total != 4 || fetched != 1 || compressed != 1 || posted != 1 || skipped != 1 {
		t.Errorf("PipelineStats unexpected: total=%d fetched=%d compressed=%d posted=%d skipped=%d",
			total, fetched, compressed, posted, skipped)
	}
}

func TestLinkExistsInPostsFalseForPipelineOnlyLink(t *testing.T) {
	s := testStore(t)

	const link = "https://example.com/never-posted"
	if err := s.InsertPipelineArticle(link, "Title", "src", "raw", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}

	exists, err := s.LinkExistsInPosts(link)
	if err != nil {
		t.Fatalf("LinkExistsInPosts: %v", err)
	}
	if exists {
		t.Errorf("LinkExistsInPosts(%q) = true, want false: the link is only in pipeline_articles, not social_embeddings", link)
	}
}
