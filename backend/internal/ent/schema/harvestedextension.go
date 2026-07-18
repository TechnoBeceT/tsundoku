package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
)

// HarvestedExtension holds the schema definition for the HarvestedExtension
// entity: one extension (a Tachiyomi/Mihon source plugin) that Tsundoku has
// harvested from an extension repository into its own durable engine-topology
// store.
//
// `pkg_name` is the stable identity (an extension's Android package name) — a
// re-harvest upserts by it — so the table is a de-duplicated set of extensions.
//
// Every field is additive/optional/defaulted, so adding this entity is a
// zero-data migration: Ent auto-migrate creates the empty table and rows are
// created lazily as extensions are harvested.
type HarvestedExtension struct {
	ent.Schema
}

// Fields of the HarvestedExtension.
func (HarvestedExtension) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Unique(),
		// pkg_name is the extension's Android package name and the stable identity
		// of the row. Unique — the store holds each extension exactly once.
		field.String("pkg_name").Unique(),
		// repo_url is the extension-repository URL this extension was harvested from.
		field.String("repo_url").Default(""),
		// version_code is the extension's numeric version (monotonic; used to detect
		// an available update).
		field.Int("version_code").Default(0),
		// installed_version_code is the engine-INSTALLED version code at the time
		// these bytes were cached — the change-detector for re-caching. Distinct
		// from version_code, which is the repo-index version describing the cached
		// apk bytes.
		field.Int("installed_version_code").Default(0),
		// version_name is the extension's human-readable version string.
		field.String("version_name").Default(""),
		// source_ids are the Suwayomi source ids this extension provides (one
		// extension can expose several sources). Stored as a JSON array of 64-bit
		// ints (source ids are 64-bit on the wire).
		field.JSON("source_ids", []int64{}).Optional(),
		// apk_sha256 is the SHA-256 of the extension's cached .apk ("" until cached).
		field.String("apk_sha256").Default(""),
		// apk_cached marks whether the extension's .apk has been downloaded into the
		// local cache.
		field.Bool("apk_cached").Default(false),
		// cached_versions is the set of HELD (retained) .apk versions still on disk
		// for this extension — the durable record behind reversible updates: the
		// Extensions UI lists these so the owner can reinstall an older build, and a
		// harvest/update prunes the set to the newest N (extensions.retained_versions)
		// ∪ the installed version. Additive/optional ⇒ zero-data migration (existing
		// rows read as an empty held set until the next harvest populates it).
		field.JSON("cached_versions", []apkcache.CachedVersion{}).Optional(),
		// updated_at is refreshed on every write (harvest / cache update).
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the HarvestedExtension. None — a harvested extension is a standalone
// fact keyed by its package name, with no link to any other Tsundoku row.
func (HarvestedExtension) Edges() []ent.Edge {
	return nil
}
