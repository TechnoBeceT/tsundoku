package category_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/category"
)

// TestValidateNameAccepts verifies that valid, human-readable category names
// (including spaces and mixed case) are accepted and returned trimmed.
func TestValidateNameAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Manga":           "Manga",
		"Korean Webtoons": "Korean Webtoons",
		"  Trimmed  ":     "Trimmed",
		"A":               "A",
	}
	for in, want := range cases {
		got, err := category.ValidateName(in)
		if err != nil {
			t.Errorf("ValidateName(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidateNameRejects verifies that blank, oversized, and filesystem-unsafe
// names are rejected with ErrInvalidCategoryName.
func TestValidateNameRejects(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"blank":        "",
		"whitespace":   "   ",
		"too long":     strings.Repeat("a", 65),
		"slash":        "a/b",
		"backslash":    `a\b`,
		"dot":          ".",
		"dotdot":       "..",
		"leading dot":  ".hidden",
		"trailing dot": "name.",
		"nul":          "a\x00b",
		"control char": "a\tb",
	}
	for name, in := range cases {
		if _, err := category.ValidateName(in); !errors.Is(err, category.ErrInvalidCategoryName) {
			t.Errorf("ValidateName(%s=%q): want ErrInvalidCategoryName, got %v", name, in, err)
		}
	}
}
