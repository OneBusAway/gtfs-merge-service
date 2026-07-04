package report

import (
	"reflect"
	"testing"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

func strPtr(s string) *string { return &s }

func TestLetterIndex(t *testing.T) {
	tests := []struct {
		i    int
		want string
	}{
		{0, "a"},
		{1, "b"},
		{25, "z"},
		{26, "aa"},
		{27, "ab"},
	}
	for _, tt := range tests {
		if got := letterIndex(tt.i); got != tt.want {
			t.Errorf("letterIndex(%d) = %q, want %q", tt.i, got, tt.want)
		}
	}
}

func TestBuildPrefixBuckets(t *testing.T) {
	feeds := []config.FeedV2{
		{ID: "everett", Prefix: "97-"},
		{ID: "middle"},                                      // no prefix declared: gets no bucket of its own
		{ID: "sound-transit", Prefix: "should-be-ignored-"}, // last feed: prefix ignored
	}

	got := buildPrefixBuckets(feeds)
	want := []prefixBucket{
		{prefix: strPtr("97-"), feedID: "everett"},
		{prefix: nil, feedID: "sound-transit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildPrefixBuckets() = %+v, want %+v", derefBuckets(got), derefBuckets(want))
	}
}

func derefBuckets(buckets []prefixBucket) []string {
	out := make([]string, len(buckets))
	for i, b := range buckets {
		if b.prefix == nil {
			out[i] = "feed=" + b.feedID + " prefix=<nil>"
		} else {
			out[i] = "feed=" + b.feedID + " prefix=" + *b.prefix
		}
	}
	return out
}

func TestClassifyPrefix(t *testing.T) {
	buckets := buildPrefixBuckets([]config.FeedV2{
		{ID: "everett", Prefix: "97-"},
		{ID: "sound-transit"},
	})

	tests := []struct {
		id   string
		want int // bucket index
	}{
		{"97-1001", 0},
		{"1001", 1},   // no matching prefix: falls to the last-feed bucket
		{"a-1001", 1}, // a letter-renamed id also falls to the remainder bucket
	}
	for _, tt := range tests {
		if got := classifyPrefix(buckets, tt.id); got != tt.want {
			t.Errorf("classifyPrefix(%q) = %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestResolveSampleAfter(t *testing.T) {
	sets := &idSets{
		StopID: map[string]bool{"1001": true, "97-1002": true, "a-1003": true},
	}

	tests := []struct {
		name         string
		before       string
		configPrefix string
		wantAfter    string
		wantOK       bool
	}{
		{"native survivor", "1001", "97-", "1001", true},
		{"prefix rename", "1002", "97-", "97-1002", true},
		{"letter-index fallback", "1003", "97-", "a-1003", true},
		{"omitted: no matching form anywhere", "9999", "97-", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after, ok := resolveSampleAfter(sets, "stop_id", tt.configPrefix, "a-", tt.before)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && after != tt.wantAfter {
				t.Errorf("after = %q, want %q", after, tt.wantAfter)
			}
		})
	}
}

// TestComputeSampleIDMappingsAllFourCases exercises
// computeSampleIDMappings end to end (feed order, per-type dispatch,
// omission) rather than just resolveSampleAfter in isolation.
func TestComputeSampleIDMappingsAllFourCases(t *testing.T) {
	feeds := []config.FeedV2{
		{ID: "alpha", Prefix: "97-"},
		{ID: "beta"},
	}
	inputs := []InputReport{
		{
			FeedID: "alpha",
			SampleIDs: SampleIDs{
				StopID:  []string{"SA1", "SA2", "SA3"}, // native, prefix-rename, omitted
				RouteID: []string{"RA1"},               // letter-index fallback
			},
		},
		{
			FeedID:    "beta",
			SampleIDs: SampleIDs{StopID: []string{"SB1"}}, // native (last feed, never renamed)
		},
	}
	sets := &idSets{
		StopID:  map[string]bool{"SA1": true, "97-SA2": true, "SB1": true},
		RouteID: map[string]bool{"a-RA1": true},
	}

	got := computeSampleIDMappings(feeds, inputs, sets)
	want := []SampleIDMapping{
		{FeedID: "alpha", Type: "stop_id", Before: "SA1", After: "SA1"},
		{FeedID: "alpha", Type: "stop_id", Before: "SA2", After: "97-SA2"},
		// SA3 is omitted: no native/prefix/letter form exists in sets.
		{FeedID: "alpha", Type: "route_id", Before: "RA1", After: "a-RA1"},
		{FeedID: "beta", Type: "stop_id", Before: "SB1", After: "SB1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("computeSampleIDMappings() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestDeriveRenameCountsContextStrategy(t *testing.T) {
	dir := t.TempDir()
	outputZip := dir + "/output.zip"
	writeGTFSZip(t, outputZip, map[string]string{
		"routes.txt": "route_id,route_short_name\na-RA1,1\nRA2,2\nRB1,3\n",
	})

	cfg := &config.ConfigV2{
		Feeds: []config.FeedV2{
			{ID: "alpha"},
			{ID: "beta"}, // last: never renamed
		},
		MergeSettings: config.MergeSettingsV2{
			Files: map[string]config.FileMergeSettingV2{
				"routes.txt": {Detection: "identity", Renaming: "context"},
			},
		},
	}
	inputs := []InputReport{{FeedID: "alpha"}, {FeedID: "beta"}}

	counts, warnings, err := deriveRenameCounts(cfg, inputs, outputZip)
	if err != nil {
		t.Fatalf("deriveRenameCounts() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if counts["routes.txt"] != 1 {
		t.Errorf("counts[routes.txt] = %d, want 1 (only a-RA1 matches the context prefix)", counts["routes.txt"])
	}
}

func TestDeriveRenameCountsAgencyStrategy(t *testing.T) {
	dir := t.TempDir()
	outputZip := dir + "/output.zip"
	writeGTFSZip(t, outputZip, map[string]string{
		"stops.txt": "stop_id,stop_name\n1-1001,Main St\n1002,Broadway\n",
	})

	cfg := &config.ConfigV2{
		Feeds: []config.FeedV2{
			{ID: "alpha"},
			{ID: "beta"},
		},
		MergeSettings: config.MergeSettingsV2{
			Files: map[string]config.FileMergeSettingV2{
				"stops.txt": {Detection: "identity", Renaming: "agency"},
			},
		},
	}
	inputs := []InputReport{
		{FeedID: "alpha", Agencies: []Agency{{AgencyID: "1", Name: "Alpha Transit"}}},
		{FeedID: "beta", Agencies: []Agency{{AgencyID: "2", Name: "Beta Transit"}}},
	}

	counts, _, err := deriveRenameCounts(cfg, inputs, outputZip)
	if err != nil {
		t.Fatalf("deriveRenameCounts() error: %v", err)
	}
	if counts["stops.txt"] != 1 {
		t.Errorf("counts[stops.txt] = %d, want 1 (only 1-1001 matches alpha's agency_id prefix)", counts["stops.txt"])
	}
}

func TestDeriveRenameCountsSkipsFilesWithoutSingleIDColumn(t *testing.T) {
	dir := t.TempDir()
	outputZip := dir + "/output.zip"
	writeGTFSZip(t, outputZip, map[string]string{
		"calendar.txt": "service_id,start_date,end_date\nWK,20260101,20261231\n",
	})

	cfg := &config.ConfigV2{
		Feeds: []config.FeedV2{{ID: "alpha"}, {ID: "beta"}},
		MergeSettings: config.MergeSettingsV2{
			Files: map[string]config.FileMergeSettingV2{
				"calendar.txt": {Detection: "identity", Renaming: "context"},
			},
		},
	}

	counts, warnings, err := deriveRenameCounts(cfg, nil, outputZip)
	if err != nil {
		t.Fatalf("deriveRenameCounts() error: %v", err)
	}
	if _, ok := counts["calendar.txt"]; ok {
		t.Errorf("counts[calendar.txt] present, want it omitted (no single-ID column)")
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly one explaining the skip", warnings)
	}
}

func TestDeriveRenameCountsNoFileSettings(t *testing.T) {
	cfg := &config.ConfigV2{Feeds: []config.FeedV2{{ID: "alpha"}}}
	counts, warnings, err := deriveRenameCounts(cfg, nil, "/does/not/matter.zip")
	if err != nil {
		t.Fatalf("deriveRenameCounts() error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("counts = %v, want empty", counts)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}
