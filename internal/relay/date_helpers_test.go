package relay

import (
	"strings"
	"testing"
	"time"
)

func TestBoolToInt(t *testing.T) {
	if got := boolToInt(true); got != 1 {
		t.Fatalf("boolToInt(true) = %d, want 1", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("boolToInt(false) = %d, want 0", got)
	}
}

func TestParsePublishedAtLayouts(t *testing.T) {
	cases := []struct {
		name                   string
		input                  string
		wantYear               int
		wantMonth              time.Month
		wantDay, wantHour      int
		wantMinute, wantSecond int
	}{
		{
			name:       "rfc3339 literal Z",
			input:      "2026-01-15T10:30:00Z",
			wantYear:   2026,
			wantMonth:  time.January,
			wantDay:    15,
			wantHour:   10,
			wantMinute: 30,
			wantSecond: 0,
		},
		{
			name:       "rfc3339 numeric offset",
			input:      "2026-01-15T10:30:00-05:00",
			wantYear:   2026,
			wantMonth:  time.January,
			wantDay:    15,
			wantHour:   10,
			wantMinute: 30,
			wantSecond: 0,
		},
		{
			name:       "space separated datetime",
			input:      "2026-01-15 10:30:00",
			wantYear:   2026,
			wantMonth:  time.January,
			wantDay:    15,
			wantHour:   10,
			wantMinute: 30,
			wantSecond: 0,
		},
		{
			name:       "date only midnight",
			input:      "2026-01-15",
			wantYear:   2026,
			wantMonth:  time.January,
			wantDay:    15,
			wantHour:   0,
			wantMinute: 0,
			wantSecond: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.input
			got := parsePublishedAt(&s)
			if got == nil {
				t.Fatalf("parsePublishedAt(%q) = nil, want non-nil *time.Time", c.input)
			}
			tm := *got
			if tm.Year() != c.wantYear {
				t.Errorf("parsePublishedAt(%q) Year = %d, want %d", c.input, tm.Year(), c.wantYear)
			}
			if tm.Month() != c.wantMonth {
				t.Errorf("parsePublishedAt(%q) Month = %v, want %v", c.input, tm.Month(), c.wantMonth)
			}
			if tm.Day() != c.wantDay {
				t.Errorf("parsePublishedAt(%q) Day = %d, want %d", c.input, tm.Day(), c.wantDay)
			}
			if tm.Hour() != c.wantHour {
				t.Errorf("parsePublishedAt(%q) Hour = %d, want %d", c.input, tm.Hour(), c.wantHour)
			}
			if tm.Minute() != c.wantMinute {
				t.Errorf("parsePublishedAt(%q) Minute = %d, want %d", c.input, tm.Minute(), c.wantMinute)
			}
			if tm.Second() != c.wantSecond {
				t.Errorf("parsePublishedAt(%q) Second = %d, want %d", c.input, tm.Second(), c.wantSecond)
			}
		})
	}
}

func TestParsePublishedAtNilAndEmpty(t *testing.T) {
	if got := parsePublishedAt(nil); got != nil {
		t.Fatalf("parsePublishedAt(nil) = %v, want nil", got)
	}
	s := ""
	if got := parsePublishedAt(&s); got != nil {
		t.Fatalf("parsePublishedAt(\"\") = %v, want nil", got)
	}
}

// TestParsePublishedAtRejectsRfc1123 pins a REAL gap in the RSS-ingest path.
// RSS 2.0's spec recommends exactly two date formats for <pubDate>:
// RFC 822 (e.g. "Mon, 15 Jan 2026 10:30:00 GMT") and RFC 3339. But
// parsePublishedAt only tries RFC 3339 plus three SQLite-style layouts,
// so the RFC 822/1123 style that real-world feeds overwhelmingly emit
// silently parses to nil. That fits this repo's whole purpose (ingesting
// RSS feeds), so it is worth pinning loud and loud here: an RFC1123 date
// returns nil.
func TestParsePublishedAtRejectsRfc1123(t *testing.T) {
	s := "Mon, 15 Jan 2026 10:30:00 GMT"
	if got := parsePublishedAt(&s); got != nil {
		t.Fatalf("parsePublishedAt(%q) = %v, want nil: RFC1123 is not among the 4 layouts this function tries", s, got)
	}
}

func TestParseTimeRoundTrip(t *testing.T) {
	space := "2026-01-15 10:30:00"
	tm, err := parseTime(space)
	if err != nil {
		t.Fatalf("parseTime(%q) unexpected error: %v", space, err)
	}
	wantSpace := time.Date(2026, time.January, 15, 10, 30, 0, 0, time.UTC)
	if !tm.Equal(wantSpace) {
		t.Errorf("parseTime(%q) = %v, want %v", space, tm, wantSpace)
	}

	rfctext := "2026-01-15T10:30:00Z"
	tm, err = parseTime(rfctext)
	if err != nil {
		t.Fatalf("parseTime(%q) unexpected error: %v", rfctext, err)
	}
	wantRFC := time.Date(2026, time.January, 15, 10, 30, 0, 0, time.UTC)
	if !tm.Equal(wantRFC) {
		t.Errorf("parseTime(%q) = %v, want %v", rfctext, tm, wantRFC)
	}
}

func TestParseTimeRejectsBareDate(t *testing.T) {
	if _, err := parseTime("2026-01-15"); err == nil {
		t.Fatal("parseTime(\"2026-01-15\") = nil error, want non-nil error (bare date has no time component)")
	}
}

func TestPeriodFilter(t *testing.T) {
	dayCounts := map[string]string{
		"week":  "-7 days",
		"month": "-30 days",
		"year":  "-365 days",
	}

	frags := periodFilter("p.")
	for period, wantDays := range dayCounts {
		t.Run(period, func(t *testing.T) {
			frag, ok := frags[period]
			if !ok {
				t.Fatalf("periodFilter(\"p.\") missing key %q", period)
			}
			if !strings.HasPrefix(frag, "AND p.created_at") {
				t.Errorf("periodFilter(\"p.\")[%q] = %q, want prefix %q", period, frag, "AND p.created_at")
			}
			if !strings.Contains(frag, wantDays) {
				t.Errorf("periodFilter(\"p.\")[%q] = %q, want it to contain %q", period, frag, wantDays)
			}
		})
	}

	noPrefix := periodFilter("")
	for period := range dayCounts {
		if !strings.HasPrefix(noPrefix[period], "AND created_at") {
			t.Errorf("periodFilter(\"\")[%q] = %q, want prefix %q (no dangling '.' or space)", period, noPrefix[period], "AND created_at")
		}
		if strings.Contains(noPrefix[period], ".created_at") {
			t.Errorf("periodFilter(\"\")[%q] = %q, dangling '.' from empty prefix", period, noPrefix[period])
		}
	}
}
