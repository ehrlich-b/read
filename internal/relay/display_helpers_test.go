package relay

import (
	"strings"
	"testing"
	"time"
)

func TestValidSort(t *testing.T) {
	for _, v := range []string{"new", "week", "month", "year", "hot"} {
		if got := validSort(v); got != v {
			t.Errorf("validSort(%q) = %q, want %q", v, got, v)
		}
	}
	for _, v := range []string{"bogus", ""} {
		if got := validSort(v); got != "hot" {
			t.Errorf("validSort(%q) = %q, want %q", v, got, "hot")
		}
	}
}

func TestStripHTMLTitle(t *testing.T) {
	got := stripHTMLTitle("<b>Hello</b> &amp; World  ")
	want := "Hello & World"
	if got != want {
		t.Errorf("stripHTMLTitle() = %q, want %q", got, want)
	}
}

func TestDisplayScore(t *testing.T) {
	// Hand-derived expected values (int() truncates toward zero):
	//   mass <= 0               -> floor case, returns 3 directly
	//   log2(12)  = 3.5849      -> int 3   (>= 3, < 10, no clamp)
	//   log2(42)  = 5.3923      -> int 5   (>= 3, < 10, no clamp)
	//   log2(3000) = 11.5507    -> int 11  (ONE past the upper clamp boundary -> 10)
	//   log2(10000) = 13.2877   -> int 13  -> clamped to 10
	//   log2(100000) = 16.6096  -> int 16  -> clamped to 10 (ceiling stays engaged)
	cases := []struct {
		mass int
		want int
	}{
		{-5, 3},
		{0, 3},
		{12, 3},
		{42, 5},
		{3000, 10},
		{10000, 10},
		{100000, 10},
	}
	for _, c := range cases {
		if got := displayScore(c.mass); got != c.want {
			t.Errorf("displayScore(%d) = %d, want %d", c.mass, got, c.want)
		}
	}
}

func TestSlugDisplay(t *testing.T) {
	got := slugDisplay("machine-learning-and-ai")
	want := "machine learning and ai"
	if got != want {
		t.Errorf("slugDisplay() = %q, want %q", got, want)
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"30s ago", now.Add(-30 * time.Second), "just now"},
		{"1 min ago", now.Add(-1 * time.Minute), "1 minute ago"},
		{"90 min ago", now.Add(-90 * time.Minute), "1 hour ago"},
		{"25h ago", now.Add(-25 * time.Hour), "1 day ago"},
		{"40d ago", now.Add(-40 * 24 * time.Hour), "1 month ago"},
	}
	for _, c := range cases {
		if got := timeAgo(c.t); got != c.want {
			t.Errorf("timeAgo(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTimeAgoFutureSilentlyJustNow(t *testing.T) {
	// VERIFIED FINDING: a time 1 hour in the FUTURE produces a NEGATIVE
	// time.Since(t) duration, which still satisfies `d < time.Minute`.
	// So timeAgo returns "just now" -- no error, no panic. Worth knowing:
	// if a caller ever feeds it a clock-skewed or bad timestamp, it fails
	// silently rather than surfacing anything unusual.
	if got := timeAgo(time.Now().Add(1 * time.Hour)); got != "just now" {
		t.Errorf("timeAgo(future) = %q, want %q", got, "just now")
	}
}

func TestExtractTitleSummaryWithTitle(t *testing.T) {
	title := "<b>Posted</b>"
	p := &SocialEmbedding{
		Title: &title,
		Text:  "[prefix]\nRest of the text",
	}
	gotTitle, gotSummary := extractTitleSummary(p)
	if gotTitle != "Posted" {
		t.Errorf("title = %q, want %q", gotTitle, "Posted")
	}
	if gotSummary != "Rest of the text" {
		t.Errorf("summary = %q, want %q", gotSummary, "Rest of the text")
	}
}

func TestExtractTitleSummaryPeriodSplit(t *testing.T) {
	text := "First sentence. More detail follows."
	p := &SocialEmbedding{Title: nil, Text: text}
	gotTitle, gotSummary := extractTitleSummary(p)
	if gotTitle != "First sentence." {
		t.Errorf("title = %q, want %q", gotTitle, "First sentence.")
	}
	// The easy-to-miss detail: summary is the ENTIRE original Text,
	// not just the part after the period.
	if gotSummary != text {
		t.Errorf("summary = %q, want the whole text %q", gotSummary, text)
	}
}

func TestExtractTitleSummaryLongTextNoPeriod(t *testing.T) {
	longText := strings.Repeat("a", 150)
	p := &SocialEmbedding{Title: nil, Text: longText}
	gotTitle, gotSummary := extractTitleSummary(p)
	wantTitle := strings.Repeat("a", 100) + "..."
	if len(gotTitle) != 103 {
		t.Errorf("title length = %d, want 103", len(gotTitle))
	}
	if gotTitle != wantTitle {
		t.Errorf("title = %q..., want %q...", gotTitle[:100], wantTitle[:100])
	}
	// VERIFIED FINDING: unlike the period-split branch, when there is no
	// ". " the summary is left as the EMPTY string, not the full text.
	if gotSummary != "" {
		t.Errorf("summary = %q, want empty string", gotSummary)
	}
}
