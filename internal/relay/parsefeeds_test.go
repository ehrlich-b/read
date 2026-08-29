package relay

import "testing"

func entry(es []FeedEntry, url string) *FeedEntry {
	for i := range es {
		if es[i].URL == url {
			return &es[i]
		}
	}
	return nil
}

func TestParseFeedsBasic(t *testing.T) {
	in := `## Technology
### Go
- https://go.dev/blog # Go blog
- https://pkg.go.dev  # Go packages
### Rust
- https://rust-lang.org/blog  # Rust blog
## News
### Hacker News
- https://news.ycombinator.com # top stories
### Lobsters
- https://lobste.rs  # lobster news
- https://lobste.rs/newest  # newest lobsters
`

	sections := ParseFeeds(in)

	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}

	if sections[0].Name != "Technology" {
		t.Errorf("sections[0].Name = %q, want %q", sections[0].Name, "Technology")
	}
	if sections[1].Name != "News" {
		t.Errorf("sections[1].Name = %q, want %q", sections[1].Name, "News")
	}

	tech := sections[0].Groups
	if len(tech) != 2 {
		t.Fatalf("Technology groups = %d, want 2", len(tech))
	}
	if tech[0].Slug != "Go" {
		t.Errorf("tech[0].Slug = %q, want %q", tech[0].Slug, "Go")
	}
	if len(tech[0].Entries) != 2 {
		t.Errorf("Go entries = %d, want 2", len(tech[0].Entries))
	}
	if e := tech[0].Entries[0]; e.URL != "https://go.dev/blog" || e.Description != "Go blog" {
		t.Errorf("Go entry0 = %+v, want {https://go.dev/blog Go blog}", e)
	}
	if e := tech[0].Entries[1]; e.URL != "https://pkg.go.dev" || e.Description != "Go packages" {
		t.Errorf("Go entry1 = %+v, want {https://pkg.go.dev Go packages}", e)
	}
	if tech[1].Slug != "Rust" {
		t.Errorf("tech[1].Slug = %q, want %q", tech[1].Slug, "Rust")
	}
	if len(tech[1].Entries) != 1 {
		t.Errorf("Rust entries = %d, want 1", len(tech[1].Entries))
	}
	if e := tech[1].Entries[0]; e.URL != "https://rust-lang.org/blog" || e.Description != "Rust blog" {
		t.Errorf("Rust entry0 = %+v, want {https://rust-lang.org/blog Rust blog}", e)
	}

	news := sections[1].Groups
	if len(news) != 2 {
		t.Fatalf("News groups = %d, want 2", len(news))
	}
	if news[0].Slug != "Hacker News" {
		t.Errorf("news[0].Slug = %q, want %q", news[0].Slug, "Hacker News")
	}
	if len(news[0].Entries) != 1 {
		t.Errorf("Hacker News entries = %d, want 1", len(news[0].Entries))
	}
	if e := news[0].Entries[0]; e.URL != "https://news.ycombinator.com" || e.Description != "top stories" {
		t.Errorf("HN entry0 = %+v, want {https://news.ycombinator.com top stories}", e)
	}
	if news[1].Slug != "Lobsters" {
		t.Errorf("news[1].Slug = %q, want %q", news[1].Slug, "Lobsters")
	}
	if len(news[1].Entries) != 2 {
		t.Errorf("Lobsters entries = %d, want 2", len(news[1].Entries))
	}
	if e := news[1].Entries[0]; e.URL != "https://lobste.rs" || e.Description != "lobster news" {
		t.Errorf("Lobsters entry0 = %+v, want {https://lobste.rs lobster news}", e)
	}
	if e := news[1].Entries[1]; e.URL != "https://lobste.rs/newest" || e.Description != "newest lobsters" {
		t.Errorf("Lobsters entry1 = %+v, want {https://lobste.rs/newest newest lobsters}", e)
	}
}

func TestParseFeedsEntryNoDescription(t *testing.T) {
	in := `## Sec
### Grp
- https://example.com/feed
`
	sections := ParseFeeds(in)

	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	groups := sections[0].Groups
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if len(groups[0].Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(groups[0].Entries))
	}
	e := groups[0].Entries[0]
	if e.URL != "https://example.com/feed" {
		t.Errorf("URL = %q, want %q", e.URL, "https://example.com/feed")
	}
	if e.Description != "" {
		t.Errorf("Description = %q, want %q", e.Description, "")
	}
}

func TestParseFeedsDoubleHashResidue(t *testing.T) {
	// CHARACTERIZED FINDING: the split only consumes the first matched
	// separator, "  #" (two spaces + one hash = 3 characters). When the
	// description itself starts with a second adjacent hash, that second
	// hash is untouched and remains part of the kept description substring.
	in := "## sec\n### x\n- https://x.com/feed  ## double-hash note"
	sections := ParseFeeds(in)

	if len(sections) != 1 || len(sections[0].Groups) != 1 || len(sections[0].Groups[0].Entries) != 1 {
		t.Fatalf("unexpected shape: %+v", sections)
	}
	e := sections[0].Groups[0].Entries[0]
	if e.URL != "https://x.com/feed" {
		t.Errorf("URL = %q, want %q", e.URL, "https://x.com/feed")
	}
	// One leftover '#' survives; the raw line was "...  ## double-hash note"
	// and "  #" consumed only two spaces + the first hash.
	if e.Description != "# double-hash note" {
		t.Errorf("Description = %q, want %q", e.Description, "# double-hash note")
	}
}

func TestParseFeedsEntryBeforeGroupDropped(t *testing.T) {
	in := `## Sec
- https://example.com/early  # should vanish
### Grp
- https://real.example/feed  # kept
`
	sections := ParseFeeds(in)

	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	groups := sections[0].Groups
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Slug != "Grp" {
		t.Errorf("Slug = %q, want %q", groups[0].Slug, "Grp")
	}
	if len(groups[0].Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(groups[0].Entries))
	}
	if e := groups[0].Entries[0]; e.URL != "https://real.example/feed" {
		t.Errorf("only entry URL = %q, want %q", e.URL, "https://real.example/feed")
	}

	for _, s := range sections {
		for _, g := range s.Groups {
			if entry(g.Entries, "https://example.com/early") != nil {
				t.Error("entry before any group header was not dropped")
			}
		}
	}
}

func TestParseFeedsOrphanGroupMigrates(t *testing.T) {
	// CHARACTERIZED FINDING: an orphan "### " group that appears BEFORE any
	// "## " section header does NOT vanish. curGroup is only ever REASSIGNED
	// when a new "### " line is seen; it is never reset to nil when a new
	// "## " section starts. So the orphan group stays alive in curGroup right
	// through the first "## Sec" line (whose own flush check fails because
	// curSection is still nil) and is only flushed when the FOLLOWING "### "
	// line triggers a flush -- into whichever section comes next. The orphan
	// group is therefore silently MIS-ATTACHED to a section it never appeared
	// under in the source file. ROOT CAUSE: the missing `curGroup = nil` reset.
	// This behavior is intentionally left unfixed -- characterization only.
	in := "### orphan-grp\n- https://orphan.example/feed  # orphan note\n## Sec\n### grp\n- https://grp.example/feed  # grp note"
	sections := ParseFeeds(in)

	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	if sections[0].Name != "Sec" {
		t.Errorf("section name = %q, want %q", sections[0].Name, "Sec")
	}
	if len(sections[0].Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (orphan mis-attached + grp)", len(sections[0].Groups))
	}
	o := sections[0].Groups[0]
	if o.Slug != "orphan-grp" {
		t.Errorf("groups[0].Slug = %q, want %q (mis-attached orphan)", o.Slug, "orphan-grp")
	}
	if len(o.Entries) != 1 {
		t.Fatalf("orphan entries = %d, want 1", len(o.Entries))
	}
	if e := o.Entries[0]; e.URL != "https://orphan.example/feed" || e.Description != "orphan note" {
		t.Errorf("orphan entry = %+v, want {https://orphan.example/feed orphan note}", e)
	}
	g := sections[0].Groups[1]
	if g.Slug != "grp" {
		t.Errorf("groups[1].Slug = %q, want %q", g.Slug, "grp")
	}
	if len(g.Entries) != 1 || g.Entries[0].URL != "https://grp.example/feed" {
		t.Errorf("grp entries = %+v, want [{https://grp.example/feed grp note}]", g.Entries)
	}
}

func TestParseFeedsEmpty(t *testing.T) {
	sections := ParseFeeds("")
	if sections == nil {
		return
	}
	if len(sections) != 0 {
		t.Errorf("sections = %d (%+v), want 0 / nil, no phantom section", len(sections), sections)
	}
}

func TestParseFeedsTrailingFlush(t *testing.T) {
	// Input ends immediately after an entry line, with no trailing blank line
	// and no closing header. The parser must flush its in-progress
	// section/group at end-of-input, not only when a header line triggers a
	// flush (easy off-by-one to get wrong).
	in := "## Sec\n### Grp\n- https://example.com/trailing  # last"
	sections := ParseFeeds(in)

	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	if sections[0].Name != "Sec" {
		t.Errorf("name = %q, want %q", sections[0].Name, "Sec")
	}
	if len(sections[0].Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(sections[0].Groups))
	}
	if sections[0].Groups[0].Slug != "Grp" {
		t.Errorf("slug = %q, want %q", sections[0].Groups[0].Slug, "Grp")
	}
	if len(sections[0].Groups[0].Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(sections[0].Groups[0].Entries))
	}
	e := sections[0].Groups[0].Entries[0]
	if e.URL != "https://example.com/trailing" || e.Description != "last" {
		t.Errorf("entry = %+v, want {https://example.com/trailing last}", e)
	}
}
