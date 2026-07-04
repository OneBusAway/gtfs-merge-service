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
	"github.com/onebusaway/gtfs-merge-service/internal/merge"
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
// for logging now and (in a later milestone) serialization into
// report.json's stages[] (see docs/config-schema.md §3). feedID is set for
// per-feed stages (download, prepare) and empty for whole-job stages
// (combine, post).
//
// Note: report.json's documented stage key for downloading is "watch", not
// "download" — this pipeline uses "download" internally since that's what
// this stage actually does; a later milestone maps it to "watch" when
// writing report.json.
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

// runV2 executes the v2 pipeline: download -> prepare -> combine -> validate
// -> upload. (Pair-merge and additional-file injection are added in later
// milestones.)
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

	fmt.Println("\nStep 4: Preparing feeds (transform)...")
	transformer := transform.New(transformerJarPath, tempDir)
	preparedPaths, stages, err := prepareFeeds(cfg, transformer, feedPaths, stages)
	if err != nil {
		logStages(stages)
		return err
	}
	fmt.Println("✓ Feeds prepared")

	fmt.Println("\nStep 5: Combining feeds (merge)...")
	combineStart := time.Now()
	merger := merge.New(mergeJarPath, tempDir)
	mergedPath, err := combineFeeds(cfg, merger, preparedPaths, "gtfs.zip")
	stages = appendStage(stages, "combine", "", combineStart, err)
	if err != nil {
		logStages(stages)
		return err
	}
	fmt.Println("✓ Feeds merged successfully")

	fmt.Println("\nStep 6: Validating merged feed...")
	postStart := time.Now()
	validator := validate.New()
	err = validator.ValidateFeed(mergedPath)
	if err != nil {
		stages = appendStage(stages, "post", "", postStart, err)
		logStages(stages)
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("✓ Merged feed validated")

	fmt.Println("\nStep 7: Uploading to S3/R2...")
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

	err = uploader.UploadFile(mergedPath, cfg.Output.Key)
	stages = appendStage(stages, "post", "", postStart, err)
	if err != nil {
		logStages(stages)
		return fmt.Errorf("failed to upload: %w", err)
	}
	fmt.Println("✓ Upload complete")

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
