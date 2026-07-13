package mangadex

import (
	"context"
	"net/url"
	"strconv"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// Covers returns one metadata.CoverCandidate per cover MangaDex has on
// file for remoteID — MangaDex, uniquely among the Phase-1 providers,
// keeps a full per-volume cover gallery rather than one thumbnail, so
// this is the richest source the cover picker (QCAT-228) has to offer.
// Every candidate is tagged SourceKind "metadata" / SourceRef Key(), per
// the shared CoverCandidate contract in provider.go.
func (c *Client) Covers(ctx context.Context, remoteID string) ([]metadata.CoverCandidate, error) {
	reqURL := apiBaseURL + "/cover?" + url.Values{
		"manga[]": {remoteID},
		"limit":   {strconv.Itoa(coverGalleryLimit)},
	}.Encode()

	var page coverCollectionResponse
	if err := c.doGet(ctx, reqURL, &page); err != nil {
		return nil, err
	}

	out := make([]metadata.CoverCandidate, 0, len(page.Data))
	for _, cov := range page.Data {
		if cov.Attributes.FileName == "" {
			continue
		}
		out = append(out, metadata.CoverCandidate{
			SourceKind: "metadata",
			SourceRef:  Key,
			CoverURL:   coverURL(remoteID, cov.Attributes.FileName),
			Label:      coverLabel(cov.Attributes.Volume, cov.Attributes.Locale),
		})
	}
	return out, nil
}
