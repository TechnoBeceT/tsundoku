package provideraddress

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// PreserveKnown keeps current when it is known, otherwise adopts observed when
// observed is known. It is the in-memory counterpart of PersistResolved.
func PreserveKnown(current, observed sourceengine.AddressMode) sourceengine.AddressMode {
	if current.IsKnown() {
		return current
	}
	if observed.IsKnown() {
		return observed
	}
	return sourceengine.AddressModeUnknown
}

// FromStored converts the database enum to the engine wire type.
func FromStored(mode entseriesprovider.AddressMode) sourceengine.AddressMode {
	switch mode {
	case entseriesprovider.AddressModeDirect:
		return sourceengine.AddressModeDirect
	case entseriesprovider.AddressModeURLSearch:
		return sourceengine.AddressModeURLSearch
	default:
		return sourceengine.AddressModeUnknown
	}
}

// ToStored converts a validated engine mode to the database enum.
func ToStored(mode sourceengine.AddressMode) (entseriesprovider.AddressMode, error) {
	switch mode {
	case sourceengine.AddressModeUnknown:
		return entseriesprovider.AddressModeUnknown, nil
	case sourceengine.AddressModeDirect:
		return entseriesprovider.AddressModeDirect, nil
	case sourceengine.AddressModeURLSearch:
		return entseriesprovider.AddressModeURLSearch, nil
	default:
		return "", fmt.Errorf("invalid provider address mode %q", mode)
	}
}

// PersistResolved atomically promotes an unknown provider row to a known mode.
// The unknown predicate is the concurrency guard: stale unknown observations,
// duplicate resolutions, and racing known resolutions can never overwrite the
// first known value committed for the row.
func PersistResolved(ctx context.Context, client *ent.Client, providerID uuid.UUID, mode sourceengine.AddressMode) error {
	return persistResolved(ctx, client, providerID, mode)
}

// PersistResolvedForAddress promotes an unknown row only while its complete
// address pair still matches the observation that established mode. The exact
// predicates keep a concurrent address refresh from attaching provenance to a
// different opaque, relative, or absolute address.
func PersistResolvedForAddress(ctx context.Context, client *ent.Client, providerID uuid.UUID, url, webURL string, mode sourceengine.AddressMode) error {
	return persistResolved(
		ctx,
		client,
		providerID,
		mode,
		entseriesprovider.URLEQ(url),
		entseriesprovider.WebURLEQ(webURL),
	)
}

func persistResolved(ctx context.Context, client *ent.Client, providerID uuid.UUID, mode sourceengine.AddressMode, addressPredicates ...predicate.SeriesProvider) error {
	stored, err := ToStored(mode)
	if err != nil {
		return err
	}
	if stored == entseriesprovider.AddressModeUnknown {
		return nil
	}
	predicates := []predicate.SeriesProvider{
		entseriesprovider.IDEQ(providerID),
		entseriesprovider.AddressModeEQ(entseriesprovider.AddressModeUnknown),
	}
	predicates = append(predicates, addressPredicates...)
	_, err = client.SeriesProvider.Update().
		Where(predicates...).
		SetAddressMode(stored).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("persist resolved address mode for provider %s: %w", providerID, err)
	}
	return nil
}
