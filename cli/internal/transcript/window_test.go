package transcript

import (
	"testing"
	"time"
)

// TestInWindowCountsUnreadableTimestampsAsInside checks the window boundary and
// that an empty, unparseable or unfiltered entry is kept.
func TestInWindowCountsUnreadableTimestampsAsInside(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		timestamp string
		since     time.Time
		want      bool
	}{
		{"before", "2025-12-31T23:59:59Z", since, false},
		{"at", "2026-01-01T00:00:00Z", since, true},
		{"after", "2026-01-01T00:00:01Z", since, true},
		{"empty timestamp", "", since, true},
		{"unparseable", "yesterday", since, true},
		{"zero since", "2025-12-31T23:59:59Z", time.Time{}, true},
	}
	for _, c := range cases {
		if got := InWindow(c.timestamp, c.since); got != c.want {
			t.Errorf("%s: InWindow(%q) = %v, want %v", c.name, c.timestamp, got, c.want)
		}
	}
}
