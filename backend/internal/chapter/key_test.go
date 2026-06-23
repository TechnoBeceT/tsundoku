package chapter_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// ptr is a test helper that returns a pointer to the given float64.
func ptr(f float64) *float64 { return &f }

func TestFormatChapterNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    float64
		want string
	}{
		{"integer twelve via float literal", 12.0, "12"},
		{"integer twelve via bare literal", 12, "12"},
		{"one decimal place", 12.50, "12.5"},
		{"two decimal places", 12.05, "12.05"},
		{"zero", 0, "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := chapter.FormatChapterNumber(tc.n)
			if got != tc.want {
				t.Errorf("FormatChapterNumber(%v) = %q; want %q", tc.n, got, tc.want)
			}
		})
	}
}

func TestNormalizeChapterKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		number *float64
		title  string
		want   string
	}{
		// Numbered branch — various float representations of the same value.
		{"12.0 as float literal", ptr(12.0), "", "12"},
		{"12 as integer-like literal", ptr(12), "", "12"},
		{"12.5 shortest decimal", ptr(12.50), "", "12.5"},
		{"12.05 two significant decimals", ptr(12.05), "", "12.05"},
		{"zero chapter", ptr(0), "", "0"},
		// Same number via different literals must produce the same key.
		{"12.0 and 12 are the same key", ptr(12.0), "ignored", "12"},
		// Name is ignored when number is non-nil.
		{"name ignored when number present", ptr(12), "x", "12"},
		// Unnumbered branch.
		{"simple name lowercase", nil, "Extra", "name:extra"},
		{"name with double space collapses to single hyphen", nil, "Side  Story", "name:side-story"},
		// Slug strip/collapse edge cases — prove [a-z0-9.-] rule precisely.
		{"dot is preserved in slug", nil, "Vol.1", "name:vol.1"},
		{"pre-existing hyphen is kept by strip", nil, "Side-Story", "name:side-story"},
		{"punctuation outside allowed set is stripped", nil, "Extra!", "name:extra"},
		// Nil number AND empty name yields "name:".
		{"nil number and empty name", nil, "", "name:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := chapter.NormalizeChapterKey(tc.number, tc.title)
			if got != tc.want {
				t.Errorf("NormalizeChapterKey(%v, %q) = %q; want %q", tc.number, tc.title, got, tc.want)
			}
		})
	}
}
