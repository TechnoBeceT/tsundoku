package notify

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// digestSeriesThreshold is the number of distinct series above which a
// per-series notification collapses into a single digest ("N new chapters
// across M series"). At or below it, the notification lists the series by name.
const digestSeriesThreshold = 3

// NewChapterGroup is one series' contribution to a new-chapter notification:
// the series' display title, how many genuinely-new readable chapters it gained
// this cycle, and the in-app deep-link to open it.
type NewChapterGroup struct {
	// SeriesID is the series these new chapters belong to.
	SeriesID uuid.UUID `json:"seriesId"`
	// Title is the series' display title (used in the notification body).
	Title string `json:"title"`
	// Count is the number of new readable chapters in this cycle for the series.
	Count int `json:"count"`
	// URL is the in-app deep-link to the series page ("/series/<id>").
	URL string `json:"url"`
}

// NewChapterNotification is the single payload dispatched over BOTH channels
// (the chapter.new SSE event for open clients and Web Push for closed ones) when
// one or more armed series gained new readable chapters in a cycle. Title/Body
// are pre-rendered server-side so the service worker can showNotification
// straight from the payload without fetching anything.
type NewChapterNotification struct {
	// Groups is the per-series breakdown (never empty when dispatched).
	Groups []NewChapterGroup `json:"groups"`
	// Total is the sum of all groups' Counts.
	Total int `json:"total"`
	// Digest is true when the notification collapsed to the cross-series summary
	// (more than digestSeriesThreshold series). The service worker deep-links to
	// the library ("/") for a digest, or to groups[0].URL otherwise.
	Digest bool `json:"digest"`
	// Title is the pre-rendered notification title.
	Title string `json:"title"`
	// Body is the pre-rendered notification body.
	Body string `json:"body"`
}

// render turns the surviving per-series groups into a single notification
// title/body and reports whether it collapsed to a digest. Below/at the digest
// threshold it names the series (one series → its title; a few → a comma list);
// above the threshold it summarises as "N new chapters across M series".
func render(groups []NewChapterGroup) (title, body string, digest bool) {
	total := 0
	for _, g := range groups {
		total += g.Count
	}

	if len(groups) > digestSeriesThreshold {
		return "New chapters",
			fmt.Sprintf("%d new %s across %d series", total, chapterWord(total), len(groups)),
			true
	}

	if len(groups) == 1 {
		g := groups[0]
		return g.Title, fmt.Sprintf("%d new %s", g.Count, chapterWord(g.Count)), false
	}

	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%s (%d)", g.Title, g.Count))
	}
	return fmt.Sprintf("%d new %s", total, chapterWord(total)), strings.Join(parts, ", "), false
}

// chapterWord returns the correctly-pluralised noun for a chapter count.
func chapterWord(n int) string {
	if n == 1 {
		return "chapter"
	}
	return "chapters"
}
