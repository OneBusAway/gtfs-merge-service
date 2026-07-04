package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
	"github.com/onebusaway/gtfs-merge-service/internal/download"
	"github.com/onebusaway/gtfs-merge-service/internal/inject"
	"github.com/onebusaway/gtfs-merge-service/internal/merge"
	"github.com/onebusaway/gtfs-merge-service/internal/pairmerge"
	"github.com/onebusaway/gtfs-merge-service/internal/report"
	"github.com/onebusaway/gtfs-merge-service/internal/transform"
	"github.com/onebusaway/gtfs-merge-service/internal/upload"
	"github.com/onebusaway/gtfs-merge-service/internal/validate"
)

// loadConfiguration loads and normalizes the configuration from either a URL
// or a file path. It sniffs the config's schema version; both v1 and v2
// configs are parsed and validated, and the caller branches on
// normalized.IsV2() to pick the right execution path.
func loadConfiguration(configURL, configPath string, allowedDomains []string) (*config.NormalizedConfig, error) {
	if configURL != "" {
		fmt.Println("\nStep 2: Loading configuration from URL...")
		normalized, err := config.LoadNormalizedConfigFromURL(configURL, allowedDomains)
		if err != nil {
			return nil, fmt.Errorf("failed to load config from URL: %w", err)
		}
		return normalized, nil
	}

	fmt.Println("\nStep 2: Loading configuration from file...")
	normalized, err := config.LoadNormalizedConfigFromFile(configPath, allowedDomains)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from file: %w", err)
	}
	return normalized, nil
}

// downloadFeeds handles the downloading of GTFS feeds (v1 path).
func downloadFeeds(cfg *config.Config, tempDir string) ([]string, error) {
	fmt.Println("\nStep 3: Downloading GTFS feeds...")
	downloader := download.New(tempDir)
	feedFiles, err := downloader.DownloadFeeds(cfg.Feeds)
	if err != nil {
		return nil, fmt.Errorf("failed to download feeds: %w", err)
	}
	fmt.Printf("✓ Downloaded %d feeds\n", len(feedFiles))
	return feedFiles, nil
}

// findJar locates a jar file, preferring primaryPath (the location baked
// into the Docker image) and falling back to fallbackName (a relative path,
// useful when running outside the image).
func findJar(primaryPath, fallbackName string) (string, error) {
	if _, err := os.Stat(primaryPath); err == nil {
		return primaryPath, nil
	}
	if _, err := os.Stat(fallbackName); err == nil {
		return fallbackName, nil
	}
	return "", fmt.Errorf("%s not found in expected locations", fallbackName)
}

// StageResult records the outcome and duration of one v2 pipeline stage,
// for console logging and for serialization into report.json's stages[]
// (see docs/config-schema.md §3, and internal/report.Generate, which
// consumes a converted copy of these as []report.StageInput). feedID is set
// for per-feed stages (download, prepare) and empty for whole-job stages
// (combine, post).
//
// Note: report.json's documented stage key for downloading is "watch", not
// "download" — this pipeline uses "download" internally since that's what
// this stage actually does; internal/report.Generate maps it to "watch"
// when writing report.json (see stageKeyToReport there).
type StageResult struct {
	Key      string
	FeedID   string
	Status   string
	Duration time.Duration
}

func appendStage(stages []StageResult, key, feedID string, start time.Time, err error) []StageResult {
	status := "ok"
	if err != nil {
		status = "error"
	}
	return append(stages, StageResult{Key: key, FeedID: feedID, Status: status, Duration: time.Since(start)})
}

func logStages(stages []StageResult) {
	fmt.Println("\nStage timings:")
	for _, s := range stages {
		if s.FeedID != "" {
			fmt.Printf("  %-10s feed=%-20s %-5s %s\n", s.Key, s.FeedID, s.Status, s.Duration)
		} else {
			fmt.Printf("  %-10s %-5s %s\n", s.Key, s.Status, s.Duration)
		}
	}
}

// downloadV2Feeds downloads each feed's primary URL, keyed by feed ID.
// Feeds are downloaded in cfg.Feeds order (sequentially, so that order is
// trivially preserved regardless of map iteration later).
func downloadV2Feeds(cfg *config.ConfigV2, downloader *download.Downloader) (map[string]string, error) {
	paths := make(map[string]string, len(cfg.Feeds))
	for _, feed := range cfg.Feeds {
		path, err := downloader.DownloadFile(feed.URL, fmt.Sprintf("feed_%s.zip", feed.ID))
		if err != nil {
			return nil, fmt.Errorf("failed to download feed %s: %w", feed.ID, err)
		}
		paths[feed.ID] = path
	}
	return paths, nil
}

// pairMergeFeeds pre-merges each feed with a pairedWith URL: downloads the
// paired ("upcoming") zip and runs pairmerge.Merge, replacing that feed's
// working path in feedPaths with the pair-merged output. Feeds without a
// pairedWith are left untouched. Order follows cfg.Feeds (see
// pairmerge.Merge's doc comment for why current-then-upcoming argument
// order matters).
func pairMergeFeeds(cfg *config.ConfigV2, downloader *download.Downloader, pairMerger *pairmerge.PairMerger, feedPaths map[string]string, stages []StageResult) (map[string]string, []StageResult, error) {
	for _, feed := range cfg.Feeds {
		if feed.PairedWith == nil {
			continue
		}

		start := time.Now()
		upcomingPath, err := downloader.DownloadFile(feed.PairedWith.URL, fmt.Sprintf("feed_%s_upcoming.zip", feed.ID))
		if err == nil {
			var pairedPath string
			pairedPath, err = pairMerger.Merge(feed.ID, feedPaths[feed.ID], upcomingPath)
			if err == nil {
				feedPaths[feed.ID] = pairedPath
			}
		}
		stages = appendStage(stages, "pair", feed.ID, start, err)
		if err != nil {
			return nil, stages, fmt.Errorf("failed to pair-merge feed %s: %w", feed.ID, err)
		}
	}
	return feedPaths, stages, nil
}

// combinedRules concatenates sharedTransformRules and a feed's own
// transformRules, in that order (see docs/config-schema.md §1.2).
func combinedRules(shared, own []json.RawMessage) []json.RawMessage {
	combined := make([]json.RawMessage, 0, len(shared)+len(own))
	combined = append(combined, shared...)
	combined = append(combined, own...)
	return combined
}

// prepareFeeds runs the transformer over each feed's working zip (the
// combined sharedTransformRules ++ feed.transformRules), in cfg.Feeds order.
func prepareFeeds(cfg *config.ConfigV2, transformer *transform.Transformer, feedPaths map[string]string, stages []StageResult) (map[string]string, []StageResult, error) {
	prepared := make(map[string]string, len(cfg.Feeds))
	for _, feed := range cfg.Feeds {
		start := time.Now()
		rules := combinedRules(cfg.SharedTransformRules, feed.TransformRules)
		outPath, err := transformer.Transform(feed.ID, feedPaths[feed.ID], rules)
		stages = appendStage(stages, "prepare", feed.ID, start, err)
		if err != nil {
			return nil, stages, fmt.Errorf("failed to prepare feed %s: %w", feed.ID, err)
		}
		prepared[feed.ID] = outPath
	}
	return prepared, stages, nil
}

// combineFeeds runs the main multi-feed merge, in cfg.Feeds order (merge
// order — see docs/config-schema.md §1.3), using cfg.MergeSettings.
func combineFeeds(cfg *config.ConfigV2, merger *merge.Merger, preparedPaths map[string]string, outputFile string) (string, error) {
	orderedPaths := make([]string, len(cfg.Feeds))
	for i, feed := range cfg.Feeds {
		orderedPaths[i] = preparedPaths[feed.ID]
	}

	fileSettings := make(map[string]merge.FileSetting, len(cfg.MergeSettings.Files))
	for file, setting := range cfg.MergeSettings.Files {
		fileSettings[file] = merge.FileSetting{Detection: setting.Detection, Renaming: setting.Renaming}
	}

	if err := merger.MergeFeedsV2(orderedPaths, fileSettings, cfg.MergeSettings.DuplicateHandling, outputFile); err != nil {
		return "", fmt.Errorf("failed to merge feeds: %w", err)
	}

	return merger.GetOutputPath(outputFile), nil
}

// injectAdditionalFiles downloads each configured additional file and
// rewrites mergedPath to add or replace those entries, returning the path
// to the resulting zip. It is a no-op (returns mergedPath unchanged) when
// no additionalFiles are configured.
func injectAdditionalFiles(cfg *config.ConfigV2, downloader *download.Downloader, mergedPath, tempDir string) (string, error) {
	if len(cfg.AdditionalFiles) == 0 {
		return mergedPath, nil
	}

	additional := make(map[string]string, len(cfg.AdditionalFiles))
	for _, af := range cfg.AdditionalFiles {
		localPath, err := downloader.DownloadFile(af.URL, fmt.Sprintf("additional_%s", af.Filename))
		if err != nil {
			return "", fmt.Errorf("failed to download additional file %s: %w", af.Filename, err)
		}
		additional[af.Filename] = localPath
	}

	outputPath := filepath.Join(tempDir, "gtfs-with-additional-files.zip")
	if err := inject.Inject(mergedPath, additional, outputPath); err != nil {
		return "", fmt.Errorf("failed to inject additional files: %w", err)
	}

	return outputPath, nil
}

// generateAndUploadReport builds report.json for this run (see
// internal/report) and uploads it to cfg.Output.ReportKey. Report
// generation is non-fatal to the overall merge: by the time this runs, the
// merged bundle has already uploaded successfully, so a failure here
// (including a panic from report generation itself) is recovered and
// returned as a plain error for the caller to log as a warning, not fail
// the run over.
func generateAndUploadReport(cfg *config.ConfigV2, feedWorkingZip map[string]string, outputZipPath, mergeOutput string, mergeOutputTruncated bool, stages []StageResult, uploader *upload.Uploader) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic while generating report: %v", r)
		}
	}()

	reportStages := make([]report.StageInput, len(stages))
	for i, s := range stages {
		reportStages[i] = report.StageInput{Key: s.Key, FeedID: s.FeedID, Status: s.Status, Duration: s.Duration}
	}

	rpt, err := report.Generate(report.GenerateInput{
		Config:               cfg,
		FeedWorkingZip:       feedWorkingZip,
		OutputZipPath:        outputZipPath,
		MergeOutput:          mergeOutput,
		MergeOutputTruncated: mergeOutputTruncated,
		Stages:               reportStages,
	})
	if err != nil {
		return fmt.Errorf("failed to generate report: %w", err)
	}

	data, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	if err := uploader.UploadBytes(data, cfg.Output.ReportKey, "application/json"); err != nil {
		return fmt.Errorf("failed to upload report: %w", err)
	}

	return nil
}

// runV2 executes the v2 pipeline: download -> pair-merge -> prepare ->
// combine -> inject additional files -> validate -> upload.
func runV2(cfg *config.ConfigV2, envConfig *config.EnvConfig, tempDir string) error {
	var stages []StageResult

	mergeJarPath, err := findJar("/app/merge-cli.jar", "merge-cli.jar")
	if err != nil {
		return err
	}
	transformerJarPath, err := findJar(transform.DefaultJarPath, "transformer-cli.jar")
	if err != nil {
		return err
	}

	downloader := download.New(tempDir)

	fmt.Println("\nStep 3: Downloading GTFS feeds...")
	start := time.Now()
	feedPaths, err := downloadV2Feeds(cfg, downloader)
	stages = appendStage(stages, "download", "", start, err)
	if err != nil {
		logStages(stages)
		return fmt.Errorf("failed to download feeds: %w", err)
	}
	fmt.Printf("✓ Downloaded %d feeds\n", len(feedPaths))

	fmt.Println("\nStep 4: Pair-merging feeds with an upcoming signup...")
	pairMerger := pairmerge.New(mergeJarPath, tempDir)
	feedPaths, stages, err = pairMergeFeeds(cfg, downloader, pairMerger, feedPaths, stages)
	if err != nil {
		logStages(stages)
		return err
	}
	fmt.Println("✓ Pair-merges complete")

	fmt.Println("\nStep 5: Preparing feeds (transform)...")
	transformer := transform.New(transformerJarPath, tempDir)
	preparedPaths, stages, err := prepareFeeds(cfg, transformer, feedPaths, stages)
	if err != nil {
		logStages(stages)
		return err
	}
	fmt.Println("✓ Feeds prepared")

	fmt.Println("\nStep 6: Combining feeds (merge)...")
	combineStart := time.Now()
	merger := merge.New(mergeJarPath, tempDir)
	mergedPath, err := combineFeeds(cfg, merger, preparedPaths, "gtfs.zip")
	stages = appendStage(stages, "combine", "", combineStart, err)
	if err != nil {
		logStages(stages)
		return err
	}
	fmt.Println("✓ Feeds merged successfully")

	// "post" brackets all post-combine work — additional-file injection,
	// validation, and upload — as a single whole-job stage entry (there is
	// no dedicated report.json stage key for validate/upload; see
	// docs/config-schema.md §3.1).
	postStart := time.Now()

	fmt.Println("\nStep 7: Injecting additional files...")
	finalPath, err := injectAdditionalFiles(cfg, downloader, mergedPath, tempDir)
	if err != nil {
		stages = appendStage(stages, "post", "", postStart, err)
		logStages(stages)
		return err
	}
	fmt.Println("✓ Additional files injected")

	fmt.Println("\nStep 8: Validating merged feed...")
	validator := validate.New()
	err = validator.ValidateFeed(finalPath)
	if err != nil {
		stages = appendStage(stages, "post", "", postStart, err)
		logStages(stages)
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("✓ Merged feed validated")

	fmt.Println("\nStep 9: Uploading to S3/R2...")
	uploader, err := upload.New(
		envConfig.AwsAccessKeyID,
		envConfig.AwsSecretAccessKey,
		envConfig.AwsEndpointURL,
		envConfig.S3Bucket,
	)
	if err != nil {
		stages = appendStage(stages, "post", "", postStart, err)
		logStages(stages)
		return fmt.Errorf("failed to create uploader: %w", err)
	}

	err = uploader.UploadFile(finalPath, cfg.Output.Key)
	stages = appendStage(stages, "post", "", postStart, err)
	if err != nil {
		logStages(stages)
		return fmt.Errorf("failed to upload: %w", err)
	}
	fmt.Println("✓ Upload complete")

	// report.json generation is intentionally excluded from the "post"
	// stage bracket above and given its own stage entry: unlike
	// inject/validate/upload, a failure here must never fail the overall
	// run (see generateAndUploadReport) — the merge has already succeeded.
	fmt.Println("\nStep 10: Generating report.json...")
	reportStart := time.Now()
	mergeLines, mergeLinesTruncated := merger.CapturedDuplicateLines()
	if err := generateAndUploadReport(cfg, preparedPaths, finalPath, mergeLines, mergeLinesTruncated, stages, uploader); err != nil {
		stages = appendStage(stages, "report", "", reportStart, err)
		fmt.Printf("WARNING: report.json generation failed (merge succeeded regardless): %v\n", err)
	} else {
		stages = appendStage(stages, "report", "", reportStart, nil)
		fmt.Println("✓ report.json generated and uploaded")
	}

	logStages(stages)
	return nil
}

func main() {
	var configURL string
	var configPath string
	flag.StringVar(&configURL, "config-url", "", "URL to JSON config file")
	flag.StringVar(&configPath, "config-path", "", "Path to JSON config file")
	flag.Parse()

	if configURL == "" && configPath == "" {
		log.Fatal("Either -config-url or -config-path must be provided")
	} else if configURL != "" && configPath != "" {
		log.Fatal("Only one of -config-url or -config-path should be provided")
	}

	fmt.Println("=== GTFS Merge Service ===")
	if configURL != "" {
		fmt.Printf("Config URL: %s\n\n", configURL)
	} else {
		fmt.Printf("Config Path: %s\n\n", configPath)
	}

	fmt.Println("Step 1: Validating environment variables...")
	envConfig, err := config.LoadEnvConfig()
	if err != nil {
		log.Fatalf("Environment validation failed: %v", err)
	}
	fmt.Printf("✓ Environment variables validated (allowed domains: %v)\n", envConfig.AllowedDomains)

	// Load configuration from URL or file
	normalized, err := loadConfiguration(configURL, configPath, envConfig.AllowedDomains)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("gtfs-merge-%d", os.Getpid()))
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove temp directory %s: %v\n", tempDir, err)
		}
	}()

	if normalized.IsV2() {
		fmt.Printf("✓ Loaded config with %d feeds\n", len(normalized.V2.Feeds))
		if err := runV2(normalized.V2, envConfig, tempDir); err != nil {
			log.Fatalf("v2 pipeline failed: %v", err)
		}
		fmt.Println("\n=== GTFS Merge Complete ===")
		return
	}

	cfg := normalized.V1
	fmt.Printf("✓ Loaded config with %d feeds\n", len(cfg.Feeds))

	// Download GTFS feeds
	feedFiles, err := downloadFeeds(cfg, tempDir)
	if err != nil {
		log.Fatalf("Failed to download feeds: %v", err)
	}

	fmt.Println("\nStep 4: Merging GTFS feeds...")
	jarPath, err := findJar("/app/merge-cli.jar", "merge-cli.jar")
	if err != nil {
		log.Fatal(err)
	}

	merger := merge.New(jarPath, tempDir)
	err = merger.MergeFeeds(feedFiles, cfg.MergeStrategies, cfg.OutputName)
	if err != nil {
		log.Fatalf("Failed to merge feeds: %v", err)
	}
	fmt.Println("✓ Feeds merged successfully")

	fmt.Println("\nStep 5: Validating merged feed...")
	validator := validate.New()
	mergedPath := merger.GetOutputPath(cfg.OutputName)
	err = validator.ValidateFeed(mergedPath)
	if err != nil {
		log.Fatalf("Validation failed: %v", err)
	}
	fmt.Println("✓ Merged feed validated")

	fmt.Println("\nStep 6: Uploading to S3/R2...")
	uploader, err := upload.New(
		envConfig.AwsAccessKeyID,
		envConfig.AwsSecretAccessKey,
		envConfig.AwsEndpointURL,
		envConfig.S3Bucket,
	)
	if err != nil {
		log.Fatalf("Failed to create uploader: %v", err)
	}

	err = uploader.UploadFile(mergedPath, cfg.OutputName)
	if err != nil {
		log.Fatalf("Failed to upload: %v", err)
	}
	fmt.Println("✓ Upload complete")

	fmt.Println("\n=== GTFS Merge Complete ===")
}
