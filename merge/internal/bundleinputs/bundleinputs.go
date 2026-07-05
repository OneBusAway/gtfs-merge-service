// Package bundleinputs builds and uploads the "bundle inputs" artifact set:
// the prepared per-feed zips (post pair-merge, post transform) plus a
// bundle-inputs.json manifest describing them. These are what the OBA
// bundler ingests under multi-zip load (one defaultAgencyId per zip), as
// opposed to output.key's merged zip, which is the third-party/download
// artifact. See docs/config-schema.md §4 and the stop-consolidation design
// spec ("Merge service changes (Go)").
//
// Go emits per-build URLs for dry-run inspection and NEVER emits
// stopConsolidationUrl — only Rails knows the mapping's publish state, and
// it rewrites feed URLs to stable keys (and injects stopConsolidationUrl,
// when a mapping publication exists) at publish time.
package bundleinputs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

// ManifestVersion is the schema version written to bundle-inputs.json's
// "version" field.
const ManifestVersion = 1

// Manifest is the top-level bundle-inputs.json document. Feeds preserves
// config order (= roster order = OBA bundle load order).
type Manifest struct {
	Version int    `json:"version"`
	Feeds   []Feed `json:"feeds"`
}

// Feed describes one uploaded prepared zip.
type Feed struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DefaultAgencyID string `json:"defaultAgencyId"`
	Key             string `json:"key"`
	URL             string `json:"url"`
	ByteSize        int64  `json:"byteSize"`
	SHA256          string `json:"sha256"`
}

// Build assembles the manifest from the config and the prepared working
// zips (the same preparedPaths map that feeds the combine stage), computing
// each file's byteSize and sha256. objectURL maps an object key to its
// per-build URL (see upload.Uploader.ObjectURL).
func Build(cfg *config.ConfigV2, preparedPaths map[string]string, objectURL func(key string) string) (*Manifest, error) {
	feeds := make([]Feed, 0, len(cfg.Feeds))
	for _, feed := range cfg.Feeds {
		path, ok := preparedPaths[feed.ID]
		if !ok {
			return nil, fmt.Errorf("no prepared zip for feed '%s'", feed.ID)
		}
		size, sum, err := fileSizeAndSHA256(path)
		if err != nil {
			return nil, fmt.Errorf("hashing prepared zip for feed '%s': %w", feed.ID, err)
		}
		key := cfg.Output.FeedKeys[feed.ID]
		feeds = append(feeds, Feed{
			ID:              feed.ID,
			Name:            feed.Name,
			DefaultAgencyID: feed.DefaultAgencyID,
			Key:             key,
			URL:             objectURL(key),
			ByteSize:        size,
			SHA256:          sum,
		})
	}
	return &Manifest{Version: ManifestVersion, Feeds: feeds}, nil
}

// JSON renders the manifest as indented JSON, the exact bytes uploaded to
// output.bundleInputsKey.
func (m *Manifest) JSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

func fileSizeAndSHA256(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}
