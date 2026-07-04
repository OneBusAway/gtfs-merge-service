package report

import (
	"fmt"
	"os"
	"time"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

// droppedDuplicatesLimit caps merge.droppedDuplicates (see
// docs/config-schema.md §3.1).
const droppedDuplicatesLimit = 500

// StageInput is the caller-supplied shape of one pipeline stage's timing
// data — a copy of cmd/gtfs-merge's StageResult, duplicated here (rather
// than imported) to avoid a cmd -> internal/report -> cmd import cycle.
// main.go converts its own []StageResult into []StageInput before calling
// Generate.
type StageInput struct {
	Key      string
	FeedID   string
	Status   string
	Duration time.Duration
}

// stageKeyToReport maps main.go's internal stage keys to report.json's
// documented stage key vocabulary (docs/config-schema.md §3.1). Only
// "download" differs (-> "watch"); every other key passes through
// unchanged.
var stageKeyToReport = map[string]string{
	"download": "watch",
	"pair":     "pair",
	"prepare":  "prepare",
	"combine":  "combine",
	"post":     "post",
}

// GenerateInput is everything Generate needs to build a report.json for one
// v2 merge run.
type GenerateInput struct {
	// Config is the job's v2 config (feeds, prefixes, mergeSettings, etc).
	Config *config.ConfigV2
	// FeedWorkingZip maps each feed's id to its final pre-combine working
	// zip path (post pair-merge, post transform) — what actually entered
	// the merge for that feed.
	FeedWorkingZip map[string]string
	// OutputZipPath is the final (post-inject) merged zip's path.
	OutputZipPath string
	// MergeOutput is the merge JAR's captured combined stdout+stderr (see
	// merge.Merger.CapturedOutput), scanned for dropped-duplicate lines.
	MergeOutput string
	// Stages are the pipeline's per-stage timings, in execution order, in
	// main.go's internal key vocabulary (mapped to report.json's via
	// stageKeyToReport). Generate appends the "report" stage itself.
	Stages []StageInput
	// Now returns the current time, for report.GeneratedAt; defaults to
	// time.Now when nil. Overridable so tests get a deterministic
	// timestamp.
	Now func() time.Time
}

// Generate builds a complete report.json document for one v2 merge run.
// It returns an error only when something essential couldn't be produced
// at all (e.g. the output zip can't be opened/stat'd) — per-input analysis
// failures and other soft issues are instead folded into the returned
// report's Warnings. Callers should treat a returned error as non-fatal to
// the overall merge (the merge itself already succeeded by the time
// Generate runs) — see cmd/gtfs-merge's runV2.
func Generate(in GenerateInput) (*Report, error) {
	start := time.Now()
	nowFn := in.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	var warnings []string

	inputs := make([]InputReport, 0, len(in.Config.Feeds))
	for _, feed := range in.Config.Feeds {
		ir := InputReport{
			FeedID:    feed.ID,
			URL:       feed.URL,
			Paired:    feed.PairedWith != nil,
			Files:     []string{},
			Agencies:  []Agency{},
			SampleIDs: SampleIDs{StopID: []string{}, RouteID: []string{}, TripID: []string{}},
		}

		zipPath := in.FeedWorkingZip[feed.ID]
		analysis, err := AnalyzeZip(zipPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("input %q: failed to analyze working zip: %v", feed.ID, err))
		} else {
			ir.Files = analysis.Files
			ir.Agencies = analysis.Agencies
			ir.Counts = analysis.Counts
			ir.ServiceRange = analysis.ServiceRange
			ir.BBox = analysis.BBox
			ir.SampleIDs = analysis.SampleIDs
			for _, w := range analysis.Warnings {
				warnings = append(warnings, fmt.Sprintf("input %q: %s", feed.ID, w))
			}
		}
		inputs = append(inputs, ir)
	}

	outputAnalysis, err := AnalyzeZip(in.OutputZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze output zip: %w", err)
	}
	for _, w := range outputAnalysis.Warnings {
		warnings = append(warnings, fmt.Sprintf("output: %s", w))
	}

	fi, err := os.Stat(in.OutputZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat output zip: %w", err)
	}

	idSets, histogram, err := computeOutputIDFacts(in.OutputZipPath, in.Config.Feeds)
	if err != nil {
		return nil, fmt.Errorf("failed to compute output ID facts: %w", err)
	}

	mappings := computeSampleIDMappings(in.Config.Feeds, inputs, idSets)

	renameCounts, renameWarnings, err := deriveRenameCounts(in.Config, inputs, in.OutputZipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to derive rename counts: %w", err)
	}
	warnings = append(warnings, renameWarnings...)

	dropped, truncated := parseDroppedDuplicates(in.MergeOutput, droppedDuplicatesLimit)

	stages := make([]StageReport, 0, len(in.Stages)+1)
	for _, s := range in.Stages {
		key := s.Key
		if mapped, ok := stageKeyToReport[s.Key]; ok {
			key = mapped
		}
		stages = append(stages, StageReport{
			Key:        key,
			FeedID:     s.FeedID,
			Status:     s.Status,
			DurationMs: s.Duration.Milliseconds(),
		})
	}
	stages = append(stages, StageReport{Key: "report", Status: "ok", DurationMs: time.Since(start).Milliseconds()})

	if warnings == nil {
		warnings = []string{}
	}

	return &Report{
		ReportVersion: ReportVersion,
		GeneratedAt:   nowFn().UTC().Format(time.RFC3339),
		Inputs:        inputs,
		Output: OutputReport{
			Files:            outputAnalysis.Files,
			Agencies:         outputAnalysis.Agencies,
			Counts:           outputAnalysis.Counts,
			ServiceRange:     outputAnalysis.ServiceRange,
			BBox:             outputAnalysis.BBox,
			SampleIDs:        outputAnalysis.SampleIDs,
			ByteSize:         fi.Size(),
			PrefixHistogram:  histogram,
			SampleIDMappings: mappings,
		},
		Merge: MergeReport{
			DroppedDuplicates:          dropped,
			DroppedDuplicatesTruncated: truncated,
			RenameCounts:               renameCounts,
		},
		Stages:   stages,
		Warnings: warnings,
	}, nil
}
