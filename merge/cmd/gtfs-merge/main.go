package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
	"github.com/onebusaway/gtfs-merge-service/internal/download"
	"github.com/onebusaway/gtfs-merge-service/internal/merge"
	"github.com/onebusaway/gtfs-merge-service/internal/upload"
	"github.com/onebusaway/gtfs-merge-service/internal/validate"
)

// loadConfiguration loads the configuration from either a URL or a file path.
// It sniffs the config's schema version; v1 configs are parsed and validated
// exactly as before, and v2 configs are validated but not yet executable
// (see config v2 execution note below).
func loadConfiguration(configURL, configPath string, allowedDomains []string) (*config.Config, error) {
	var normalized *config.NormalizedConfig
	var err error

	if configURL != "" {
		fmt.Println("\nStep 2: Loading configuration from URL...")
		normalized, err = config.LoadNormalizedConfigFromURL(configURL, allowedDomains)
		if err != nil {
			return nil, fmt.Errorf("failed to load config from URL: %w", err)
		}
	} else {
		fmt.Println("\nStep 2: Loading configuration from file...")
		normalized, err = config.LoadNormalizedConfigFromFile(configPath, allowedDomains)
		if err != nil {
			return nil, fmt.Errorf("failed to load config from file: %w", err)
		}
	}

	if normalized.IsV2() {
		// Config v2 execution (transform rules, paired feeds, per-file merge
		// settings, additional files, report.json generation) is not wired up
		// yet. Loading and validation are complete; wiring the execution path
		// here would be speculative ahead of the milestones that implement it.
		return nil, fmt.Errorf("config v2 execution not yet implemented")
	}

	return normalized.V1, nil
}

// downloadFeeds handles the downloading of GTFS feeds
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
	cfg, err := loadConfiguration(configURL, configPath, envConfig.AllowedDomains)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	fmt.Printf("✓ Loaded config with %d feeds\n", len(cfg.Feeds))

	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("gtfs-merge-%d", os.Getpid()))
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove temp directory %s: %v\n", tempDir, err)
		}
	}()

	// Download GTFS feeds
	feedFiles, err := downloadFeeds(cfg, tempDir)
	if err != nil {
		log.Fatalf("Failed to download feeds: %v", err)
	}

	fmt.Println("\nStep 4: Merging GTFS feeds...")
	jarPath := "/app/merge-cli.jar"
	if _, err := os.Stat(jarPath); os.IsNotExist(err) {
		jarPath = "merge-cli.jar"
		if _, err := os.Stat(jarPath); os.IsNotExist(err) {
			log.Fatal("merge-cli.jar not found in expected locations")
		}
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
