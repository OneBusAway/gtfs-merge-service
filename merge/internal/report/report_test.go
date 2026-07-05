package report

import (
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

// TestGenerateEndToEnd builds a two-feed scenario ("alpha", prefix "97-";
// "beta", the last/highest-priority feed) entirely from in-test fixture
// zips — both feeds' pre-combine working zips and the already-"merged"
// output zip a real merge-cli.jar run would have produced — and checks
// every section of the resulting report.json against hand-computed
// expectations. This is the same scenario used to justify the
// prefixHistogram/sampleIdMappings/renameCounts heuristics in
// docs/config-schema.md: SA3 is a genuine dropped duplicate (it never
// appears in the output under any form), which is also reflected in the
// synthetic merge JAR log line fed in as MergeOutput.
func TestGenerateEndToEnd(t *testing.T) {
	dir := t.TempDir()

	alphaZip := filepath.Join(dir, "alpha.zip")
	writeGTFSZip(t, alphaZip, map[string]string{
		"agency.txt": "agency_id,agency_name\n1,Alpha Transit\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"SA1,Stop A1,47.80,-122.10\n" +
			"SA2,Stop A2,47.81,-122.11\n" +
			"SA3,Stop A3,47.82,-122.12\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_type\n" +
			"RA1,1,1,3\n" +
			"RA2,1,2,3\n",
		"trips.txt":    "route_id,service_id,trip_id\nRA2,WK,TA1\n",
		"calendar.txt": "service_id,start_date,end_date\nWK,20260101,20261231\n",
	})

	betaZip := filepath.Join(dir, "beta.zip")
	writeGTFSZip(t, betaZip, map[string]string{
		"agency.txt":   "agency_id,agency_name\n2,Beta Transit\n",
		"stops.txt":    "stop_id,stop_name,stop_lat,stop_lon\nSB1,Stop B1,47.70,-122.00\n",
		"routes.txt":   "route_id,agency_id,route_short_name,route_type\nRB1,2,3,3\n",
		"trips.txt":    "route_id,service_id,trip_id\nRB1,WK,TB1\n",
		"calendar.txt": "service_id,start_date,end_date\nWK,20260101,20261231\n",
	})

	// The "merged" output: SA1 survived natively (no collision), SA2 was
	// renamed under alpha's own configured prefix ("97-"), SA3 was dropped
	// entirely (a genuine duplicate — see mergeOutput below), RA1 was
	// renamed via the JAR's index-derived "context" convention ("a-", feed
	// index 0), and everything else survived natively.
	outputZip := filepath.Join(dir, "gtfs.zip")
	writeGTFSZip(t, outputZip, map[string]string{
		"agency.txt": "agency_id,agency_name\n1,Alpha Transit\n2,Beta Transit\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"SA1,Stop A1,47.80,-122.10\n" +
			"97-SA2,Stop A2,47.81,-122.11\n" +
			"SB1,Stop B1,47.70,-122.00\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_type\n" +
			"a-RA1,1,1,3\n" +
			"RA2,1,2,3\n" +
			"RB1,2,3,3\n",
		"trips.txt":    "route_id,service_id,trip_id\nRA2,WK,TA1\nRB1,WK,TB1\n",
		"calendar.txt": "service_id,start_date,end_date\nWK,20260101,20261231\n",
	})

	cfg := &config.ConfigV2{
		Version: 2,
		Output:  config.OutputV2{Key: "out/gtfs.zip", ReportKey: "out/report.json"},
		Feeds: []config.FeedV2{
			{ID: "alpha", URL: "https://example.com/alpha.zip", Prefix: "97-"},
			{ID: "beta", URL: "https://example.com/beta.zip"},
		},
		MergeSettings: config.MergeSettingsV2{
			Files: map[string]config.FileMergeSettingV2{
				"routes.txt": {Detection: "identity", Renaming: "context"},
			},
		},
	}

	mergeOutput := "12:00:00.000 [main] WARN AbstractSingleEntityMergeStrategy - duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_SA3"

	stages := []StageInput{
		{Key: "download", Duration: 1 * time.Second},
		{Key: "pair", FeedID: "alpha", Duration: 2 * time.Second},
		{Key: "prepare", FeedID: "alpha", Duration: 3 * time.Second},
		{Key: "prepare", FeedID: "beta", Duration: 1 * time.Second},
		{Key: "combine", Duration: 4 * time.Second},
		{Key: "post", Duration: 5 * time.Second},
	}

	fixedNow := time.Date(2026, 7, 4, 18, 0, 0, 0, time.UTC)

	rpt, err := Generate(GenerateInput{
		Config: cfg,
		FeedWorkingZip: map[string]string{
			"alpha": alphaZip,
			"beta":  betaZip,
		},
		OutputZipPath: outputZip,
		MergeOutput:   mergeOutput,
		Stages:        stages,
		Now:           func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if rpt.ReportVersion != ReportVersion {
		t.Errorf("ReportVersion = %d, want %d", rpt.ReportVersion, ReportVersion)
	}
	if rpt.GeneratedAt != "2026-07-04T18:00:00Z" {
		t.Errorf("GeneratedAt = %q, want 2026-07-04T18:00:00Z", rpt.GeneratedAt)
	}

	// --- inputs[] ---
	if len(rpt.Inputs) != 2 {
		t.Fatalf("len(Inputs) = %d, want 2", len(rpt.Inputs))
	}
	alpha, beta := rpt.Inputs[0], rpt.Inputs[1]

	if alpha.FeedID != "alpha" || alpha.URL != "https://example.com/alpha.zip" || alpha.Paired {
		t.Errorf("alpha input = %+v, unexpected", alpha)
	}
	wantAlphaCounts := Counts{Stops: 3, Routes: 2, Trips: 1, Calendars: 1}
	if alpha.Counts != wantAlphaCounts {
		t.Errorf("alpha.Counts = %+v, want %+v", alpha.Counts, wantAlphaCounts)
	}
	if !reflect.DeepEqual(alpha.SampleIDs.StopID, []string{"SA1", "SA2", "SA3"}) {
		t.Errorf("alpha.SampleIDs.StopID = %v", alpha.SampleIDs.StopID)
	}
	if !reflect.DeepEqual(alpha.SampleIDs.RouteID, []string{"RA1", "RA2"}) {
		t.Errorf("alpha.SampleIDs.RouteID = %v", alpha.SampleIDs.RouteID)
	}

	if beta.FeedID != "beta" || beta.Paired {
		t.Errorf("beta input = %+v, unexpected", beta)
	}
	wantBetaCounts := Counts{Stops: 1, Routes: 1, Trips: 1, Calendars: 1}
	if beta.Counts != wantBetaCounts {
		t.Errorf("beta.Counts = %+v, want %+v", beta.Counts, wantBetaCounts)
	}

	// --- output ---
	wantOutputCounts := Counts{Stops: 3, Routes: 3, Trips: 2, Calendars: 1}
	if rpt.Output.Counts != wantOutputCounts {
		t.Errorf("Output.Counts = %+v, want %+v", rpt.Output.Counts, wantOutputCounts)
	}
	if rpt.Output.ByteSize <= 0 {
		t.Errorf("Output.ByteSize = %d, want > 0", rpt.Output.ByteSize)
	}

	wantHistogram := []PrefixHistogramEntry{
		{Prefix: strPtr("97-"), FeedID: "alpha", Count: 1},
		{Prefix: nil, FeedID: "beta", Count: 7},
	}
	if !reflect.DeepEqual(rpt.Output.PrefixHistogram, wantHistogram) {
		t.Errorf("PrefixHistogram = %+v, want %+v", describeHistogram(rpt.Output.PrefixHistogram), describeHistogram(wantHistogram))
	}

	wantMappings := []SampleIDMapping{
		{FeedID: "alpha", Type: "stop_id", Before: "SA1", After: "SA1"},
		{FeedID: "alpha", Type: "stop_id", Before: "SA2", After: "97-SA2"},
		{FeedID: "alpha", Type: "route_id", Before: "RA1", After: "a-RA1"},
		{FeedID: "alpha", Type: "route_id", Before: "RA2", After: "RA2"},
		{FeedID: "alpha", Type: "trip_id", Before: "TA1", After: "TA1"},
		{FeedID: "beta", Type: "stop_id", Before: "SB1", After: "SB1"},
		{FeedID: "beta", Type: "route_id", Before: "RB1", After: "RB1"},
		{FeedID: "beta", Type: "trip_id", Before: "TB1", After: "TB1"},
	}
	if !reflect.DeepEqual(rpt.Output.SampleIDMappings, wantMappings) {
		t.Errorf("SampleIDMappings =\n  %+v\nwant\n  %+v", rpt.Output.SampleIDMappings, wantMappings)
	}

	// --- merge ---
	if len(rpt.Merge.DroppedDuplicates) != 1 {
		t.Fatalf("len(DroppedDuplicates) = %d, want 1", len(rpt.Merge.DroppedDuplicates))
	}
	dd := rpt.Merge.DroppedDuplicates[0]
	if dd.File != "stops.txt" || dd.Parsed == nil || dd.Parsed.ID != "1_SA3" {
		t.Errorf("DroppedDuplicates[0] = %+v, unexpected", dd)
	}
	if rpt.Merge.DroppedDuplicatesTruncated {
		t.Errorf("DroppedDuplicatesTruncated = true, want false")
	}
	wantRenameCounts := map[string]int{"routes.txt": 1}
	if !reflect.DeepEqual(rpt.Merge.RenameCounts, wantRenameCounts) {
		t.Errorf("RenameCounts = %v, want %v", rpt.Merge.RenameCounts, wantRenameCounts)
	}

	// --- stages ---
	wantStageKeys := []string{"watch", "pair", "prepare", "prepare", "combine", "post", "report"}
	if len(rpt.Stages) != len(wantStageKeys) {
		t.Fatalf("len(Stages) = %d, want %d", len(rpt.Stages), len(wantStageKeys))
	}
	for i, want := range wantStageKeys {
		if rpt.Stages[i].Key != want {
			t.Errorf("Stages[%d].Key = %q, want %q", i, rpt.Stages[i].Key, want)
		}
	}
	if rpt.Stages[0].DurationMs != 1000 {
		t.Errorf("Stages[0] (watch) DurationMs = %d, want 1000", rpt.Stages[0].DurationMs)
	}
	last := rpt.Stages[len(rpt.Stages)-1]
	if last.Status != "ok" || last.DurationMs < 0 {
		t.Errorf("report stage = %+v, want status ok and a non-negative duration", last)
	}

	if len(rpt.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", rpt.Warnings)
	}
}

func describeHistogram(entries []PrefixHistogramEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		prefix := "<nil>"
		if e.Prefix != nil {
			prefix = *e.Prefix
		}
		out[i] = e.FeedID + " prefix=" + prefix + " count=" + strconv.Itoa(e.Count)
	}
	return out
}

func TestGeneratePairedFlag(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "feed.zip")
	writeGTFSZip(t, zipPath, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n1,S,1.0,1.0\n",
	})
	outputZip := filepath.Join(dir, "gtfs.zip")
	writeGTFSZip(t, outputZip, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n1,S,1.0,1.0\n",
	})

	cfg := &config.ConfigV2{
		Feeds: []config.FeedV2{
			{ID: "everett", URL: "https://example.com/everett.zip", PairedWith: &config.PairedFeedV2{URL: "https://example.com/upcoming.zip"}},
		},
	}

	rpt, err := Generate(GenerateInput{
		Config:         cfg,
		FeedWorkingZip: map[string]string{"everett": zipPath},
		OutputZipPath:  outputZip,
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if !rpt.Inputs[0].Paired {
		t.Errorf("Inputs[0].Paired = false, want true")
	}
}

// TestGenerateHonorsMergeOutputTruncatedFlag verifies that Generate reports
// merge.droppedDuplicatesTruncated from the caller-supplied
// GenerateInput.MergeOutputTruncated, not from re-deriving it against
// MergeOutput's own length. In the real v2 pipeline, MergeOutput has
// already been capped upstream by merge.Merger's bounded capture (see
// merge.DroppedDuplicatesLimit), so by the time Generate sees it, there's
// no way to tell from the text alone whether more lines existed — only the
// caller (which saw the uncapped count) knows that.
func TestGenerateHonorsMergeOutputTruncatedFlag(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "feed.zip")
	writeGTFSZip(t, zipPath, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n1,S,1.0,1.0\n",
	})
	outputZip := filepath.Join(dir, "gtfs.zip")
	writeGTFSZip(t, outputZip, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n1,S,1.0,1.0\n",
	})

	cfg := &config.ConfigV2{
		Feeds: []config.FeedV2{{ID: "everett", URL: "https://example.com/everett.zip"}},
	}

	// Only one dropped-duplicate line is present in MergeOutput (well under
	// any limit), but MergeOutputTruncated is explicitly set true, as the
	// merge package's capture would if it had seen more than
	// merge.DroppedDuplicatesLimit matching lines overall before capping
	// what it handed to Generate.
	rpt, err := Generate(GenerateInput{
		Config:               cfg,
		FeedWorkingZip:       map[string]string{"everett": zipPath},
		OutputZipPath:        outputZip,
		MergeOutput:          "duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234",
		MergeOutputTruncated: true,
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if !rpt.Merge.DroppedDuplicatesTruncated {
		t.Errorf("DroppedDuplicatesTruncated = false, want true (from GenerateInput.MergeOutputTruncated)")
	}
	if len(rpt.Merge.DroppedDuplicates) != 1 {
		t.Errorf("len(DroppedDuplicates) = %d, want 1", len(rpt.Merge.DroppedDuplicates))
	}
}

func TestGenerateFailsWhenOutputZipMissing(t *testing.T) {
	cfg := &config.ConfigV2{Feeds: []config.FeedV2{{ID: "everett", URL: "https://example.com/everett.zip"}}}

	_, err := Generate(GenerateInput{
		Config:         cfg,
		FeedWorkingZip: map[string]string{"everett": "/does/not/exist.zip"},
		OutputZipPath:  "/does/not/exist-either.zip",
	})
	if err == nil {
		t.Fatal("expected an error when the output zip can't be analyzed, got none")
	}
}

func TestGenerateFoldsInputAnalysisFailureIntoWarnings(t *testing.T) {
	dir := t.TempDir()
	outputZip := filepath.Join(dir, "gtfs.zip")
	writeGTFSZip(t, outputZip, map[string]string{
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n1,S,1.0,1.0\n",
	})

	cfg := &config.ConfigV2{Feeds: []config.FeedV2{{ID: "everett", URL: "https://example.com/everett.zip"}}}

	rpt, err := Generate(GenerateInput{
		Config:         cfg,
		FeedWorkingZip: map[string]string{"everett": "/does/not/exist.zip"},
		OutputZipPath:  outputZip,
	})
	if err != nil {
		t.Fatalf("Generate() error: %v, want a warning instead of a hard failure", err)
	}
	if len(rpt.Warnings) == 0 {
		t.Errorf("Warnings is empty, want a warning about the unreadable input zip")
	}
	// The input still gets an entry (feedId/url/paired), just with zeroed
	// analysis fields.
	if rpt.Inputs[0].FeedID != "everett" {
		t.Errorf("Inputs[0].FeedID = %q, want \"everett\"", rpt.Inputs[0].FeedID)
	}
}

// StageKeyBundleInputs must exist and pass through to report.json unchanged
// (unlike download, which maps to "watch").
func TestStageKeyBundleInputsPassthrough(t *testing.T) {
	if StageKeyBundleInputs != "bundleInputs" {
		t.Errorf("StageKeyBundleInputs = %q, want %q", StageKeyBundleInputs, "bundleInputs")
	}
	if got := stageKeyToReport[StageKeyBundleInputs]; got != "bundleInputs" {
		t.Errorf("stageKeyToReport[%q] = %q, want %q", StageKeyBundleInputs, got, "bundleInputs")
	}
}
