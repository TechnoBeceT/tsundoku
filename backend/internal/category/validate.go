package category

import (
	"fmt"
	"strings"
)

// maxNameLen caps a category name length. It becomes a filesystem folder name,
// so it must stay well under common path-component limits (255 bytes) with room
// for the storage-root + series-title path components beneath it.
const maxNameLen = 64

// ValidateName trims and validates a category name for use as BOTH a display
// label and a verbatim on-disk folder name. It returns the cleaned (trimmed)
// name on success, or ErrInvalidCategoryName wrapped with the specific reason.
//
// Rules (filesystem-safety — the name is the folder, never slugified):
//   - non-blank after trimming surrounding whitespace;
//   - length ≤ maxNameLen;
//   - no path separators ('/' or '\\') — they would escape the category dir;
//   - not "." or ".." — the current/parent directory aliases;
//   - no NUL or other control characters (< 0x20) — illegal in paths;
//   - no leading/trailing dot or space — Windows/Komga-hostile and easy to
//     confuse (the trim already removes outer spaces; a name that is only dots
//     or has interior structure is still checked).
func ValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: name must not be blank", ErrInvalidCategoryName)
	}
	if len(name) > maxNameLen {
		return "", fmt.Errorf("%w: name must be at most %d characters", ErrInvalidCategoryName, maxNameLen)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("%w: name must not be %q", ErrInvalidCategoryName, name)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: name must not contain path separators", ErrInvalidCategoryName)
	}
	if hasControlChar(name) {
		return "", fmt.Errorf("%w: name must not contain control characters", ErrInvalidCategoryName)
	}
	// A leading dot is filesystem-hidden / Komga-hostile; a trailing dot is
	// stripped by some filesystems (silent rename). The TrimSpace above already
	// removed outer spaces, so only the dot cases remain to reject explicitly.
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("%w: name must not start or end with a dot", ErrInvalidCategoryName)
	}
	return name, nil
}

// hasControlChar reports whether s contains any ASCII control character (NUL
// through US, 0x00–0x1F) — none of which are legal, safe path characters.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 {
			return true
		}
	}
	return false
}
