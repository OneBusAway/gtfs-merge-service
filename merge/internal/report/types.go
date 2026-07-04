// Package report generates report.json (see docs/config-schema.md §3) for a
// v2 merge run: per-input and merged-output GTFS feed analysis, dropped-
// duplicate/rename statistics from the merge JAR's log output, prefix-based
// ID bucketing, sample-ID before/after mappings, and pipeline stage timings.
package report

// ReportVersion is the schema version written to report.json's
// "reportVersion" field.
const ReportVersion = 1

// Report is the top-level report.json document (docs/config-schema.md §3).
type Report struct {
	ReportVersion int           `json:"reportVersion"`
	GeneratedAt   string        `json:"generatedAt"`
	Inputs        []InputReport `json:"inputs"`
	Output        OutputReport  `json:"output"`
	Merge         MergeReport   `json:"merge"`
	Stages        []StageReport `json:"stages"`
	Warnings      []string      `json:"warnings"`
}

// Agency is a single agency.txt row's identifying fields.
type Agency struct {
	AgencyID string `json:"agencyId"`
	Name     string `json:"name"`
}

// Counts is entity row counts for one feed (input or output).
type Counts struct {
	Stops     int `json:"stops"`
	Routes    int `json:"routes"`
	Trips     int `json:"trips"`
	Calendars int `json:"calendars"`
}

// ServiceRange is a feed's overall service date range, as GTFS YYYYMMDD
// strings.
type ServiceRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// BBox is a lat/lon bounding box over a feed's stops.txt.
type BBox struct {
	MinLat float64 `json:"minLat"`
	MaxLat float64 `json:"maxLat"`
	MinLon float64 `json:"minLon"`
	MaxLon float64 `json:"maxLon"`
}

// SampleIDs holds a handful of representative IDs per entity type, for
// spot-checking a feed.
type SampleIDs struct {
	StopID  []string `json:"stop_id"`
	RouteID []string `json:"route_id"`
	TripID  []string `json:"trip_id"`
}

// InputReport is one feeds[] entry's analysis, computed against that feed's
// final pre-combine working zip (post pair-merge, post transform) so counts
// reflect what actually entered the merge.
type InputReport struct {
	FeedID       string       `json:"feedId"`
	URL          string       `json:"url"`
	Paired       bool         `json:"paired"`
	Files        []string     `json:"files"`
	Agencies     []Agency     `json:"agencies"`
	Counts       Counts       `json:"counts"`
	ServiceRange ServiceRange `json:"serviceRange"`
	BBox         *BBox        `json:"bbox,omitempty"`
	SampleIDs    SampleIDs    `json:"sampleIds"`
}

// PrefixHistogramEntry is one bucket of OutputReport.PrefixHistogram: the
// count of output stop/route/trip IDs attributed to a given feed. Prefix is
// nil for the highest-priority (last) feed's bucket, which counts every ID
// not claimed by another feed's prefix (see computeOutputIDFacts).
type PrefixHistogramEntry struct {
	Prefix *string `json:"prefix"`
	FeedID string  `json:"feedId"`
	Count  int     `json:"count"`
}

// SampleIDMapping shows how one input sample ID was rewritten (or not) by
// the merge, for one of the input feeds' inputs[].sampleIds entries.
type SampleIDMapping struct {
	FeedID string `json:"feedId"`
	Type   string `json:"type"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// OutputReport is the merged feed's analysis (same shape as InputReport)
// plus fields only meaningful for the combined output.
type OutputReport struct {
	Files            []string               `json:"files"`
	Agencies         []Agency               `json:"agencies"`
	Counts           Counts                 `json:"counts"`
	ServiceRange     ServiceRange           `json:"serviceRange"`
	BBox             *BBox                  `json:"bbox,omitempty"`
	SampleIDs        SampleIDs              `json:"sampleIds"`
	ByteSize         int64                  `json:"byteSize"`
	PrefixHistogram  []PrefixHistogramEntry `json:"prefixHistogram"`
	SampleIDMappings []SampleIDMapping      `json:"sampleIdMappings"`
}

// DroppedDuplicateParsed is the best-effort structured extraction of a
// dropped-duplicate log line. Only "id" is extracted: the merge JAR's
// duplicate-entity log line names the entity type and the dropped entity's
// raw id, but never the id of the entity it was kept in favor of (see
// parseDroppedDuplicates's doc comment).
type DroppedDuplicateParsed struct {
	ID string `json:"id"`
}

// DroppedDuplicate is one dropped-duplicate entry from the merge JAR's
// stdout/stderr (only emitted when mergeSettings.duplicateHandling is
// "log"; see docs/config-schema.md §1.4).
type DroppedDuplicate struct {
	File   string                  `json:"file"`
	Raw    string                  `json:"raw"`
	Parsed *DroppedDuplicateParsed `json:"parsed,omitempty"`
}

// MergeReport is the merge stage's dropped-duplicate and rename statistics.
type MergeReport struct {
	DroppedDuplicates          []DroppedDuplicate `json:"droppedDuplicates"`
	DroppedDuplicatesTruncated bool               `json:"droppedDuplicatesTruncated"`
	RenameCounts               map[string]int     `json:"renameCounts"`
}

// StageReport is one pipeline stage's timing/status, as written to
// report.json's stages[] (see docs/config-schema.md §3.1). FeedID is
// omitted (via omitempty) for whole-job stages.
type StageReport struct {
	Key        string `json:"key"`
	FeedID     string `json:"feedId,omitempty"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
}
