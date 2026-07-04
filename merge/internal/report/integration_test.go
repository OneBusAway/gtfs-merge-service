//go:build integration

package report

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
	"github.com/onebusaway/gtfs-merge-service/internal/merge"
	"github.com/onebusaway/gtfs-merge-service/internal/transform"
)

// TestIntegrationFullPipeline runs the real merge-cli.jar and
// transformer-cli.jar against two tiny 3-stop fixture feeds ("alpha" and
// "beta"), then generates a report.json from the real merged output and
// checks its counts/prefixHistogram/sampleIdMappings/droppedDuplicates
// against hand-verified expectations.
//
// It needs two environment variables naming the jars on disk:
//
//	GTFS_MERGE_CLI_JAR=/path/to/merge-cli.jar
//	GTFS_TRANSFORMER_CLI_JAR=/path/to/transformer-cli.jar
//
// If either is unset, or doesn't point at an existing file, the test is
// skipped (not failed) — it needs a JVM plus these two jars available
// locally, which CI's default `go test ./...` run doesn't provide. Run it
// explicitly with:
//
//	GTFS_MERGE_CLI_JAR=/path/to/merge-cli.jar \
//	GTFS_TRANSFORMER_CLI_JAR=/path/to/transformer-cli.jar \
//	go test -tags integration ./internal/report/... -run TestIntegrationFullPipeline -v
//
// IMPORTANT: as of this writing, the officially released
// onebusaway-gtfs-merge-cli (including the 11.2.2 build the Dockerfile
// downloads) does NOT support --duplicateRenaming at all — that flag was
// only just added, unreleased, in gtfs-modules (see
// GtfsMergerMain.java/AbstractIdentifiableSingleEntityMergeStrategy.java's
// git history). A merge-cli.jar built from that source (or a future
// release containing it) is required for this test — and for
// mergeSettings.files[...].renaming to work at all in production; see the
// note added to docs/config-schema.md.
func TestIntegrationFullPipeline(t *testing.T) {
	mergeJar := jarFromEnv(t, "GTFS_MERGE_CLI_JAR")
	transformerJar := jarFromEnv(t, "GTFS_TRANSFORMER_CLI_JAR")

	dir := t.TempDir()

	// alpha and beta each have 3 stops. "SHARED" collides between them:
	// StopMergeStrategy never overrides rejectDuplicateOverDifferences, so
	// an identity-detected stop collision is always treated as a genuine
	// duplicate and the earlier (non-last) feed's copy is dropped — never
	// renamed. "TSAME" is a colliding trip_id whose stop_times sequences
	// deliberately differ in length between the two feeds, which makes
	// TripMergeStrategy.rejectDuplicateOverDifferences return true: that
	// takes the *rename* path instead of the drop path, exercising the
	// merge JAR's "context" duplicate-renaming convention this milestone's
	// renameCounts/sampleIdMappings/prefixHistogram logic depends on.
	alphaZip := filepath.Join(dir, "alpha.zip")
	writeGTFSZip(t, alphaZip, map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"1,Alpha Transit,https://alpha.example.com,America/Los_Angeles\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"SHARED,Shared Stop,47.60,-122.30\n" +
			"SA2,Alpha Stop 2,47.61,-122.31\n" +
			"SA3,Alpha Stop 3,47.62,-122.32\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"RA1,1,A1,Alpha One,3\n",
		"trips.txt": "route_id,service_id,trip_id\nRA1,WKA,TSAME\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"TSAME,08:00:00,08:00:00,SHARED,1\n" +
			"TSAME,08:05:00,08:05:00,SA2,2\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"WKA,1,1,1,1,1,0,0,20260101,20261231\n",
	})

	betaZip := filepath.Join(dir, "beta.zip")
	writeGTFSZip(t, betaZip, map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"2,Beta Transit,https://beta.example.com,America/Los_Angeles\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"SHARED,Shared Stop (beta),47.60,-122.30\n" +
			"SB2,Beta Stop 2,47.63,-122.33\n" +
			"SB3,Beta Stop 3,47.64,-122.34\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"RB1,2,B1,Beta One,3\n",
		"trips.txt": "route_id,service_id,trip_id\nRB1,WKB,TSAME\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"TSAME,09:00:00,09:00:00,SHARED,1\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"WKB,1,1,1,1,1,0,0,20260101,20261231\n",
	})

	// "prepare" (transform) stage: no transform rules configured, so this
	// is a documented no-op that returns the input path unchanged — still
	// exercised here to match the real v2 pipeline's shape.
	transformer := transform.New(transformerJar, dir)
	alphaPrepared, err := transformer.Transform("alpha", alphaZip, nil)
	if err != nil {
		t.Fatalf("Transform(alpha) error: %v", err)
	}
	betaPrepared, err := transformer.Transform("beta", betaZip, nil)
	if err != nil {
		t.Fatalf("Transform(beta) error: %v", err)
	}

	fileSettings := map[string]config.FileMergeSettingV2{
		"stops.txt": {Detection: "identity", Renaming: "context"},
		"trips.txt": {Detection: "identity", Renaming: "context"},
	}
	mergerFileSettings := make(map[string]merge.FileSetting, len(fileSettings))
	for file, s := range fileSettings {
		mergerFileSettings[file] = merge.FileSetting{Detection: s.Detection, Renaming: s.Renaming}
	}

	merger := merge.New(mergeJar, dir)
	if err := merger.MergeFeedsV2([]string{alphaPrepared, betaPrepared}, mergerFileSettings, "log", "gtfs.zip"); err != nil {
		t.Fatalf("MergeFeedsV2() error: %v", err)
	}
	outputZip := merger.GetOutputPath("gtfs.zip")

	cfg := &config.ConfigV2{
		Version: 2,
		Output:  config.OutputV2{Key: "out/gtfs.zip", ReportKey: "out/report.json"},
		Feeds: []config.FeedV2{
			// Prefix is purely informational/config-declared here (per
			// docs/config-schema.md §1.2) — it's never passed to the JAR,
			// so it does not correspond to the "context" renaming actually
			// applied above. It exists in this test only to exercise a
			// real, non-trivial (2-bucket) prefixHistogram.
			{ID: "alpha", URL: "https://example.com/alpha.zip", Prefix: "97-"},
			{ID: "beta", URL: "https://example.com/beta.zip"},
		},
		MergeSettings: config.MergeSettingsV2{Files: fileSettings},
	}

	rpt, err := Generate(GenerateInput{
		Config: cfg,
		FeedWorkingZip: map[string]string{
			"alpha": alphaPrepared,
			"beta":  betaPrepared,
		},
		OutputZipPath: outputZip,
		MergeOutput:   merger.CapturedOutput(),
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// --- inputs: straightforward, since these come from our own zip
	// analysis rather than anything merge-JAR-dependent. ---
	if len(rpt.Inputs) != 2 {
		t.Fatalf("len(Inputs) = %d, want 2", len(rpt.Inputs))
	}
	wantInputCounts := Counts{Stops: 3, Routes: 1, Trips: 1, Calendars: 1}
	if rpt.Inputs[0].Counts != wantInputCounts {
		t.Errorf("Inputs[0] (alpha) Counts = %+v, want %+v", rpt.Inputs[0].Counts, wantInputCounts)
	}
	if rpt.Inputs[1].Counts != wantInputCounts {
		t.Errorf("Inputs[1] (beta) Counts = %+v, want %+v", rpt.Inputs[1].Counts, wantInputCounts)
	}

	// --- output counts: alpha's SHARED stop was dropped (see comment
	// above), everything else survived (either natively or renamed), so
	// 5 stops / 2 routes / 2 trips. ---
	wantOutputCounts := Counts{Stops: 5, Routes: 2, Trips: 2, Calendars: 2}
	if rpt.Output.Counts != wantOutputCounts {
		t.Errorf("Output.Counts = %+v, want %+v (verify against `unzip -p gtfs.zip stops.txt` etc if this fails)", rpt.Output.Counts, wantOutputCounts)
	}
	if rpt.Output.ByteSize <= 0 {
		t.Errorf("Output.ByteSize = %d, want > 0", rpt.Output.ByteSize)
	}

	// --- droppedDuplicates: exactly the SHARED stop collision, logged by
	// the real JAR at WARN as "duplicate entity: type=class
	// org.onebusaway.gtfs.model.Stop id=1_SHARED" (agency_id "1" is
	// alpha's own, confirming it's alpha's copy that was dropped, per
	// docs/config-schema.md §1.3's "last feed wins" rule). ---
	if len(rpt.Merge.DroppedDuplicates) != 1 {
		t.Fatalf("len(DroppedDuplicates) = %d, want 1 (got %+v)", len(rpt.Merge.DroppedDuplicates), rpt.Merge.DroppedDuplicates)
	}
	dd := rpt.Merge.DroppedDuplicates[0]
	if dd.File != "stops.txt" {
		t.Errorf("DroppedDuplicates[0].File = %q, want %q", dd.File, "stops.txt")
	}
	if dd.Parsed == nil || dd.Parsed.ID != "1_SHARED" {
		t.Errorf("DroppedDuplicates[0].Parsed = %+v, want ID \"1_SHARED\"", dd.Parsed)
	}

	// --- renameCounts: stops.txt never renames on collision (Stop always
	// drops instead — see StopMergeStrategy, no
	// rejectDuplicateOverDifferences override), so its derived count is 0;
	// trips.txt's colliding TSAME took the rename path (differing
	// stop_times length), producing "a-TSAME" in the output — 1 renamed
	// id. ---
	wantRenameCounts := map[string]int{"stops.txt": 0, "trips.txt": 1}
	if !reflect.DeepEqual(rpt.Merge.RenameCounts, wantRenameCounts) {
		t.Errorf("RenameCounts = %v, want %v", rpt.Merge.RenameCounts, wantRenameCounts)
	}

	// --- prefixHistogram: alpha declared prefix "97-", which was never
	// actually applied (context renaming was used instead), so its bucket
	// is legitimately 0; every real output id falls into beta's (the last
	// feed's) catch-all bucket. ---
	wantHistogram := []PrefixHistogramEntry{
		{Prefix: strPtr("97-"), FeedID: "alpha", Count: 0},
		{Prefix: nil, FeedID: "beta", Count: 9}, // 5 stops + 2 routes + 2 trips
	}
	if !reflect.DeepEqual(rpt.Output.PrefixHistogram, wantHistogram) {
		t.Errorf("PrefixHistogram = %+v, want %+v", rpt.Output.PrefixHistogram, wantHistogram)
	}

	// --- sampleIdMappings ---
	//
	// IMPORTANT, empirically-verified characteristic of this heuristic
	// against the real JAR: alpha's TSAME sample resolves to "native"
	// (after == before == "TSAME"), *not* to the letter-renamed
	// "a-TSAME" that alpha's own trip actually became. Here's why: the
	// merge JAR only ever renames an entity in response to a genuine raw-
	// id collision (AbstractIdentifiableSingleEntityMergeStrategy.save),
	// and whichever feed's entity is processed *first* for a given raw id
	// always keeps it unrenamed (the "last feed wins" rule —
	// docs/config-schema.md §1.3) — so a bare/native survivor for that id
	// is *always* present in the output whenever a same-named collision
	// occurred at all. Since resolveSampleAfter checks the exact
	// (native) form before ever trying the prefix/letter forms, it always
	// finds beta's surviving "TSAME" first and reports that, even though
	// it's alpha's own entity being asked about. In other words: for the
	// merge JAR's actual renaming semantics, the prefix/letterIndex
	// fallback branches can only ever fire for an id that a *transform*
	// rule (not the merge itself) rewrote before the merge ran — they are
	// effectively unreachable via merge-driven collisions alone. The
	// same reasoning applies to alpha's SHARED stop sample (dropped, not
	// renamed, but "SHARED" is still native-matched via beta's surviving
	// stop).
	wantMappings := []SampleIDMapping{
		{FeedID: "alpha", Type: "stop_id", Before: "SHARED", After: "SHARED"},
		{FeedID: "alpha", Type: "stop_id", Before: "SA2", After: "SA2"},
		{FeedID: "alpha", Type: "stop_id", Before: "SA3", After: "SA3"},
		{FeedID: "alpha", Type: "route_id", Before: "RA1", After: "RA1"},
		{FeedID: "alpha", Type: "trip_id", Before: "TSAME", After: "TSAME"},
		{FeedID: "beta", Type: "stop_id", Before: "SHARED", After: "SHARED"},
		{FeedID: "beta", Type: "stop_id", Before: "SB2", After: "SB2"},
		{FeedID: "beta", Type: "stop_id", Before: "SB3", After: "SB3"},
		{FeedID: "beta", Type: "route_id", Before: "RB1", After: "RB1"},
		{FeedID: "beta", Type: "trip_id", Before: "TSAME", After: "TSAME"},
	}
	if !reflect.DeepEqual(rpt.Output.SampleIDMappings, wantMappings) {
		t.Errorf("SampleIDMappings =\n  %+v\nwant\n  %+v", rpt.Output.SampleIDMappings, wantMappings)
	}
}

// jarFromEnv returns the jar path named by envVar, skipping the test if the
// variable is unset or doesn't point at an existing file.
func jarFromEnv(t *testing.T, envVar string) string {
	t.Helper()
	path := os.Getenv(envVar)
	if path == "" {
		t.Skipf("%s not set; skipping integration test (see this test's doc comment)", envVar)
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s=%q does not exist (%v); skipping integration test", envVar, path, err)
	}
	return path
}
