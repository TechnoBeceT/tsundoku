package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

// libraryPrefsKey is the Settings row key under which the owner's library-list
// display preferences (sort field, direction, active toggle-filters) are
// persisted so they survive a refresh/restart and are shared cross-device
// (single-owner ⇒ no per-user key). Written DIRECTLY through the ent client —
// deliberately NOT the M12 tunable-settings allowlist (which is reserved for
// engine-tunable knobs with typed bounds and would reject this composite key),
// mirroring category.deletedDefaultsKey's use of the same table for bookkeeping.
const libraryPrefsKey = "ui.library_prefs"

// ErrInvalidPrefs is returned by SetPrefs when the submitted preferences carry
// an unknown sort key or a bad direction. Fail-closed: the store never persists
// an invalid value (mirrors settings.ErrInvalidSetting → 400).
var ErrInvalidPrefs = errors.New("invalid library preferences")

// validSortKeys is the CLOSED set of library sort fields the frontend kernel
// (librarySort.ts) understands. Kept in lockstep with it: added here means the
// FE must also handle the key, and vice-versa.
var validSortKeys = map[string]bool{
	"title":   true, // Alphabetical
	"added":   true, // createdAt
	"updated": true, // lastChapterDownloadedAt (latest chapter)
	"waiting": true, // latestChapterAt (longest-waiting / recently-released, QCAT-297)
	"unread":  true, // unread count
	"total":   true, // total chapters
	"random":  true, // shuffle
}

// LibraryFilters is the set of boolean toggle-filters the library grid can
// apply on top of the category tab + search. All default false (the whole
// library shows). needsSource pre-existed as a standalone toggle; it is folded
// in here so all four persist together.
type LibraryFilters struct {
	// Downloaded narrows to series with ≥1 downloaded chapter.
	Downloaded bool `json:"downloaded"`
	// Unread narrows to series with ≥1 unread downloaded chapter.
	Unread bool `json:"unread"`
	// Completed narrows to series the owner marked finished.
	Completed bool `json:"completed"`
	// NeedsSource narrows to series with no live download source.
	NeedsSource bool `json:"needsSource"`
	// Stalled narrows to series flagged stalled (QCAT-297): no new chapter from
	// ANY source within the stalled threshold while still monitored + not completed.
	Stalled bool `json:"stalled"`
}

// LibraryPrefs is the persisted library-list view state: the active sort field
// + direction and the active toggle-filters. It doubles as the GET/PUT
// /api/library/prefs wire shape (json tags are the API contract).
type LibraryPrefs struct {
	// SortKey is the active sort field (one of validSortKeys).
	SortKey string `json:"sortKey"`
	// SortDir is the sort direction: "asc" or "desc".
	SortDir string `json:"sortDir"`
	// Filters is the set of active toggle-filters.
	Filters LibraryFilters `json:"filters"`
}

// defaultPrefs is the view state a fresh owner sees (and the fallback for a
// missing/blank/corrupt row): Alphabetical ascending, no filters — matching the
// frontend's own initial refs so a never-saved library and a just-loaded one
// render identically.
func defaultPrefs() LibraryPrefs {
	return LibraryPrefs{SortKey: "title", SortDir: "asc"}
}

// validate rejects an unknown sort key or a bad direction (the only two fields
// with a closed domain — the filter booleans are unconstrained).
func (p LibraryPrefs) validate() error {
	if !validSortKeys[p.SortKey] {
		return fmt.Errorf("%w: unknown sortKey %q", ErrInvalidPrefs, p.SortKey)
	}
	if p.SortDir != "asc" && p.SortDir != "desc" {
		return fmt.Errorf("%w: sortDir must be asc or desc, got %q", ErrInvalidPrefs, p.SortDir)
	}
	return nil
}

// GetPrefs returns the persisted library-list preferences, or defaultPrefs when
// none are stored. A missing, blank, corrupt, or (defensively) invalid stored
// value all collapse to the defaults — reading the owner's view state must never
// fail the library load (§16-adjacent: this is a best-effort convenience read).
func (s *Service) GetPrefs(ctx context.Context) (LibraryPrefs, error) {
	row, err := s.db.Settings.Query().Where(entsettings.KeyEQ(libraryPrefsKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return defaultPrefs(), nil
	}
	if err != nil {
		return LibraryPrefs{}, fmt.Errorf("library: load prefs: %w", err)
	}
	if row.Value == "" {
		return defaultPrefs(), nil
	}
	var p LibraryPrefs
	if uErr := json.Unmarshal([]byte(row.Value), &p); uErr != nil {
		return defaultPrefs(), nil
	}
	if vErr := p.validate(); vErr != nil {
		return defaultPrefs(), nil
	}
	return p, nil
}

// SetPrefs validates then persists the library-list preferences, returning the
// stored value for a §16 round-trip. Fail-closed: an invalid payload is rejected
// (ErrInvalidPrefs) and nothing is written.
func (s *Service) SetPrefs(ctx context.Context, p LibraryPrefs) (LibraryPrefs, error) {
	if err := p.validate(); err != nil {
		return LibraryPrefs{}, err
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return LibraryPrefs{}, fmt.Errorf("library: encode prefs: %w", err)
	}
	if err := s.upsertPrefs(ctx, string(encoded)); err != nil {
		return LibraryPrefs{}, err
	}
	return p, nil
}

// upsertPrefs writes the JSON-encoded prefs into the Settings table, creating
// the row the first time and updating it thereafter (the key column is unique,
// so a query-then-write is used — mirrors category.upsertDeletedDefaults, incl.
// the constraint-race fall-through to update).
func (s *Service) upsertPrefs(ctx context.Context, value string) error {
	_, err := s.db.Settings.Query().Where(entsettings.KeyEQ(libraryPrefsKey)).Only(ctx)
	if ent.IsNotFound(err) {
		if cErr := s.db.Settings.Create().SetKey(libraryPrefsKey).SetValue(value).Exec(ctx); cErr != nil {
			if ent.IsConstraintError(cErr) {
				return s.updatePrefs(ctx, value)
			}
			return fmt.Errorf("library: create prefs: %w", cErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("library: query prefs: %w", err)
	}
	return s.updatePrefs(ctx, value)
}

// updatePrefs overwrites the existing prefs Settings row's value.
func (s *Service) updatePrefs(ctx context.Context, value string) error {
	if _, err := s.db.Settings.Update().
		Where(entsettings.KeyEQ(libraryPrefsKey)).
		SetValue(value).
		Save(ctx); err != nil {
		return fmt.Errorf("library: update prefs: %w", err)
	}
	return nil
}
