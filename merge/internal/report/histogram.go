package report

import (
	"archive/zip"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

// idSets holds every stop_id/route_id/trip_id value found in the merged
// output feed. It backs sampleIdMappings existence checks; the counts
// underlying prefixHistogram are accumulated alongside it in
// computeOutputIDFacts, in the same streaming pass.
type idSets struct {
	StopID  map[string]bool
	RouteID map[string]bool
	TripID  map[string]bool
}

func (s *idSets) has(idType, id string) bool {
	switch idType {
	case "stop_id":
		return s.StopID[id]
	case "route_id":
		return s.RouteID[id]
	case "trip_id":
		return s.TripID[id]
	}
	return false
}

// prefixBucket is one candidate prefixHistogram bucket: either a specific
// feed's configured (informational) prefix, or the highest-priority
// (last) feed's catch-all bucket (prefix == nil).
type prefixBucket struct {
	prefix *string
	feedID string
}

// buildPrefixBuckets returns the ordered bucket list for prefixHistogram
// (docs/config-schema.md §3.1): every feed except the last that declared a
// `prefix` gets its own bucket, in feed order; the last (highest-priority)
// feed always gets the trailing "everything not claimed by another feed's
// prefix" bucket (prefix == nil) — regardless of whether it also declared
// its own `prefix` — because its own IDs are never actually renamed (see
// docs/config-schema.md §1.3), so bucketing it by a declared-but-unused
// prefix would misrepresent how the output was assembled. feeds must be
// non-empty (config validation guarantees at least one feed).
func buildPrefixBuckets(feeds []config.FeedV2) []prefixBucket {
	var buckets []prefixBucket
	for i, feed := range feeds {
		if i == len(feeds)-1 {
			continue
		}
		if feed.Prefix != "" {
			prefix := feed.Prefix
			buckets = append(buckets, prefixBucket{prefix: &prefix, feedID: feed.ID})
		}
	}
	last := feeds[len(feeds)-1]
	return append(buckets, prefixBucket{prefix: nil, feedID: last.ID})
}

// classifyPrefix returns the index into buckets that id belongs to: the
// first non-last bucket whose prefix id starts with, or the trailing
// catch-all bucket otherwise.
func classifyPrefix(buckets []prefixBucket, id string) int {
	for i := 0; i < len(buckets)-1; i++ {
		if strings.HasPrefix(id, *buckets[i].prefix) {
			return i
		}
	}
	return len(buckets) - 1
}

// letterIndex renders i (0-based) as a spreadsheet-column-style lowercase
// letter sequence: 0 -> "a", 1 -> "b", ..., 25 -> "z", 26 -> "aa", .... This
// mirrors the merge JAR's "context" duplicate-renaming convention (see
// docs/config-schema.md §1.5) for feed index i.
func letterIndex(i int) string {
	s := ""
	i++
	for i > 0 {
		i--
		s = string(rune('a'+i%26)) + s
		i /= 26
	}
	return s
}

// scanIDColumn streams filename's rows out of zi (if present) and invokes
// fn with each non-blank value of column, in row order. It is a no-op if
// the file or column is absent.
func scanIDColumn(zi *zipIndex, filename, column string, fn func(id string)) error {
	cr, rc, found, err := zi.openCSV(filename)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read %s header: %w", filename, err)
	}
	if !ok {
		return nil
	}
	idx := colIndex(header, column)
	if idx == -1 {
		return nil
	}

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}
		if id := col(row, idx); id != "" {
			fn(id)
		}
	}
	return nil
}

// computeOutputIDFacts streams the output zip's stops.txt/routes.txt/
// trips.txt once, building both the full stop_id/route_id/trip_id sets
// (for sampleIdMappings existence checks) and the prefixHistogram bucket
// counts (over stop_id + route_id + trip_id combined, per
// docs/config-schema.md §3.1) in a single pass.
func computeOutputIDFacts(outputZipPath string, feeds []config.FeedV2) (*idSets, []PrefixHistogramEntry, error) {
	r, err := zip.OpenReader(outputZipPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open output zip %s: %w", outputZipPath, err)
	}
	defer func() {
		_ = r.Close()
	}()

	return computeOutputIDFactsIndexed(indexZip(&r.Reader), feeds)
}

// computeOutputIDFactsIndexed is computeOutputIDFacts's indexed-reader
// variant (see analyzeZipIndexed's doc comment for why report.Generate uses
// this instead).
func computeOutputIDFactsIndexed(zi *zipIndex, feeds []config.FeedV2) (*idSets, []PrefixHistogramEntry, error) {
	buckets := buildPrefixBuckets(feeds)
	counts := make([]int, len(buckets))

	sets := &idSets{StopID: map[string]bool{}, RouteID: map[string]bool{}, TripID: map[string]bool{}}
	columns := []struct {
		file, column string
		target       map[string]bool
	}{
		{"stops.txt", "stop_id", sets.StopID},
		{"routes.txt", "route_id", sets.RouteID},
		{"trips.txt", "trip_id", sets.TripID},
	}

	for _, spec := range columns {
		target := spec.target
		if err := scanIDColumn(zi, spec.file, spec.column, func(id string) {
			target[id] = true
			counts[classifyPrefix(buckets, id)]++
		}); err != nil {
			return nil, nil, err
		}
	}

	histogram := make([]PrefixHistogramEntry, len(buckets))
	for i, b := range buckets {
		histogram[i] = PrefixHistogramEntry{Prefix: b.prefix, FeedID: b.feedID, Count: counts[i]}
	}
	return sets, histogram, nil
}

// computeSampleIDMappings resolves each input feed's sample IDs to their
// corresponding output ID, per docs/config-schema.md §3.1: an exact match
// in the output (native, unrenamed survivor) maps after=before; otherwise
// the feed's configured `prefix` (if present) or an index-derived letter
// prefix ("a-" for feed 0, etc. — the merge JAR's "context" renaming
// convention) is tried; a sample with no match under any of these is
// omitted rather than guessed at.
func computeSampleIDMappings(feeds []config.FeedV2, inputs []InputReport, sets *idSets) []SampleIDMapping {
	mappings := []SampleIDMapping{}

	for i, feed := range feeds {
		letterPrefix := letterIndex(i) + "-"
		samplesByType := []struct {
			typeName string
			samples  []string
		}{
			{"stop_id", inputs[i].SampleIDs.StopID},
			{"route_id", inputs[i].SampleIDs.RouteID},
			{"trip_id", inputs[i].SampleIDs.TripID},
		}

		for _, s := range samplesByType {
			for _, before := range s.samples {
				after, ok := resolveSampleAfter(sets, s.typeName, feed.Prefix, letterPrefix, before)
				if !ok {
					continue
				}
				mappings = append(mappings, SampleIDMapping{FeedID: feed.ID, Type: s.typeName, Before: before, After: after})
			}
		}
	}

	return mappings
}

// resolveSampleAfter tries, in order: the id unchanged (native survivor),
// the feed's configured prefix + id, then the letter-index prefix + id.
// ok is false if none of those exist in the output's id set.
func resolveSampleAfter(sets *idSets, idType, configPrefix, letterPrefix, before string) (string, bool) {
	if sets.has(idType, before) {
		return before, true
	}
	if configPrefix != "" {
		if candidate := configPrefix + before; sets.has(idType, candidate) {
			return candidate, true
		}
	}
	if candidate := letterPrefix + before; sets.has(idType, candidate) {
		return candidate, true
	}
	return "", false
}

// renameableFileIDColumn maps GTFS files with a single, well-known
// identifiable-entity ID column to that column's name, for renameCounts
// derivation (see deriveRenameCounts). It covers exactly the files backed
// by AbstractIdentifiableSingleEntityMergeStrategy in onebusaway-gtfs-merge
// (Agency, Area, FareAttribute, FeedInfo, Route, Stop, Trip); it
// deliberately excludes calendar.txt/shapes.txt (a different,
// key-based AbstractCollectionEntityMergeStrategy) and
// frequencies.txt/transfers.txt/fare_rules.txt (non-identifiable — no
// single string ID field to prefix at all).
var renameableFileIDColumn = map[string]string{
	"agency.txt":          "agency_id",
	"stops.txt":           "stop_id",
	"routes.txt":          "route_id",
	"trips.txt":           "trip_id",
	"fare_attributes.txt": "fare_id",
	"feed_info.txt":       "feed_id",
	"areas.txt":           "area_id",
}

// deriveRenameCounts derives, per mergeSettings.files entry, a best-effort
// count of output IDs that appear to have been renamed by the merge's
// duplicate-renaming strategy.
//
// The merge JAR only logs renames at DEBUG level
// (AbstractIdentifiableSingleEntityMergeStrategy.rename's
// `_log.debug(... + " renamed(1) to " + ...)` / "renamed(2) to " calls, in
// gtfs-modules) — not the WARN level this service's default logging
// verbosity captures — so instead of parsing a log line that doesn't
// appear in practice, this counts output IDs whose prefix matches the
// renaming convention that would have been applied: "context" renaming
// uses an index-derived letter prefix (a-, b-, ...; see letterIndex and
// docs/config-schema.md §1.5), "agency" renaming uses the owning feed's
// own agency_id as a prefix. Only feeds other than the last
// (highest-priority) one can have been renamed at all (see
// docs/config-schema.md §1.3), so only their prefixes are considered.
func deriveRenameCounts(cfg *config.ConfigV2, inputs []InputReport, outputZipPath string) (map[string]int, []string, error) {
	if len(cfg.MergeSettings.Files) == 0 {
		return map[string]int{}, nil, nil
	}

	r, err := zip.OpenReader(outputZipPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open output zip %s: %w", outputZipPath, err)
	}
	defer func() {
		_ = r.Close()
	}()

	return deriveRenameCountsIndexed(indexZip(&r.Reader), cfg, inputs)
}

// deriveRenameCountsIndexed is deriveRenameCounts's indexed-reader variant
// (see analyzeZipIndexed's doc comment for why report.Generate uses this
// instead), operating on a zipIndex the caller already opened rather than
// opening outputZipPath itself.
func deriveRenameCountsIndexed(zi *zipIndex, cfg *config.ConfigV2, inputs []InputReport) (map[string]int, []string, error) {
	counts := map[string]int{}
	if len(cfg.MergeSettings.Files) == 0 {
		return counts, nil, nil
	}

	var warnings []string
	for _, file := range slices.Sorted(maps.Keys(cfg.MergeSettings.Files)) {
		setting := cfg.MergeSettings.Files[file]
		column, ok := renameableFileIDColumn[file]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("renameCounts: skipped %s (no single-ID column to derive renames from)", file))
			continue
		}

		prefixes := renamePrefixesForFile(cfg.Feeds, inputs, setting.Renaming)
		if len(prefixes) == 0 {
			continue
		}

		total := 0
		if err := scanIDColumn(zi, file, column, func(id string) {
			for _, p := range prefixes {
				if strings.HasPrefix(id, p) {
					total++
					return
				}
			}
		}); err != nil {
			return nil, nil, err
		}
		counts[file] = total
	}

	return counts, warnings, nil
}

// renamePrefixesForFile returns the candidate rename prefixes for a file
// configured with the given renaming strategy ("context" or "agency"),
// covering every feed except the last (highest-priority) one.
func renamePrefixesForFile(feeds []config.FeedV2, inputs []InputReport, renaming string) []string {
	var prefixes []string
	for i := 0; i < len(feeds)-1; i++ {
		switch renaming {
		case config.RenamingContext:
			prefixes = append(prefixes, letterIndex(i)+"-")
		case config.RenamingAgency:
			for _, ag := range inputs[i].Agencies {
				if ag.AgencyID != "" {
					prefixes = append(prefixes, ag.AgencyID+"-")
				}
			}
		}
	}
	return prefixes
}
