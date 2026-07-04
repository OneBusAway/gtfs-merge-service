package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ConfigV2 is the v2 job configuration schema. It supports multiple named
// feeds, per-feed transform rules, per-file merge settings, and additional
// passthrough files. See docs/config-schema.md for the full contract.
type ConfigV2 struct {
	Version              int                `json:"version"`
	Output               OutputV2           `json:"output"`
	Feeds                []FeedV2           `json:"feeds"`
	SharedTransformRules []json.RawMessage  `json:"sharedTransformRules"`
	MergeSettings        MergeSettingsV2    `json:"mergeSettings"`
	AdditionalFiles      []AdditionalFileV2 `json:"additionalFiles"`
}

// OutputV2 describes where the merged feed and its report are written.
type OutputV2 struct {
	Key       string `json:"key"`
	ReportKey string `json:"reportKey"`
}

// FeedV2 describes a single input GTFS feed and how it should be prepared
// before the main merge.
type FeedV2 struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Prefix         string            `json:"prefix"`
	TransformRules []json.RawMessage `json:"transformRules"`
	PairedWith     *PairedFeedV2     `json:"pairedWith"`
}

// PairedFeedV2 is a second signup zip that is pair-merged with the parent
// feed's url using default merge settings, before the feed participates in
// the main multi-feed merge.
type PairedFeedV2 struct {
	URL string `json:"url"`
}

// MergeSettingsV2 controls the main merge step: a global duplicate-handling
// policy plus per-file detection/renaming strategy overrides.
type MergeSettingsV2 struct {
	DuplicateHandling string                        `json:"duplicateHandling"`
	Files             map[string]FileMergeSettingV2 `json:"files"`
}

// FileMergeSettingV2 is a per-file duplicate detection/renaming strategy
// pair. Only files in independentMergeFiles may be used as keys in
// MergeSettingsV2.Files. Renaming is optional: an empty string is valid and
// means "use the JAR's own default" (equivalent to "context"; see
// docs/config-schema.md §1.5's emission note for why the Go service can't
// simply omit --duplicateRenaming for only some files).
type FileMergeSettingV2 struct {
	Detection string `json:"detection"`
	Renaming  string `json:"renaming"`
}

// AdditionalFileV2 is a GTFS file that is downloaded and copied into the
// merged output verbatim rather than merged or transformed.
type AdditionalFileV2 struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// reservedFilenames are additionalFiles filenames that are always rejected
// even though they match additionalFilenamePattern: "." and ".." are valid
// path segments to an unzip/os.Create call, and each resolves to a
// directory rather than a real file, opening a zip-slip-style write outside
// the intended location.
var reservedFilenames = map[string]bool{
	".":  true,
	"..": true,
}

// NormalizedConfig wraps either a v1 or v2 config so callers can branch on
// the schema version without re-parsing the source document.
type NormalizedConfig struct {
	Version int
	V1      *Config
	V2      *ConfigV2
}

// IsV2 reports whether the loaded config used schema version 2.
func (n *NormalizedConfig) IsV2() bool {
	return n.Version == 2
}

// independentMergeFiles are the GTFS files the merge CLI can apply an
// independent duplicate detection/renaming strategy to. --file,
// --duplicateDetection, and --duplicateRenaming are index-paired lists in
// the JAR, one entry per independent file.
var independentMergeFiles = []string{
	"agency.txt",
	"stops.txt",
	"routes.txt",
	"trips.txt",
	"calendar.txt",
	"shapes.txt",
	"frequencies.txt",
	"transfers.txt",
	"fare_attributes.txt",
	"fare_rules.txt",
	"feed_info.txt",
	"areas.txt",
}

// followsFileParent maps a GTFS file with no independent merge strategy to
// the file whose strategy it follows inside the JAR.
var followsFileParent = map[string]string{
	"stop_times.txt":     "trips.txt",
	"calendar_dates.txt": "calendar.txt",
}

var validDuplicateHandling = map[string]bool{
	"ignore": true,
	"log":    true,
	"fail":   true,
}

var validDetection = map[string]bool{
	"identity": true,
	"fuzzy":    true,
	"none":     true,
}

var validRenaming = map[string]bool{
	"context": true,
	"agency":  true,
}

var feedIDPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

var additionalFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func isIndependentMergeFile(name string) bool {
	for _, f := range independentMergeFiles {
		if f == name {
			return true
		}
	}
	return false
}

// LoadNormalizedConfigFromURL loads configuration from a URL, sniffing
// whether it is a v1 or v2 config and validating it accordingly.
func LoadNormalizedConfigFromURL(configURL string, allowedDomains []string) (*NormalizedConfig, error) {
	if err := validateURL(configURL, allowedDomains); err != nil {
		return nil, fmt.Errorf("invalid config URL: %w", err)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}

	resp, err := client.Get(configURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download config: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download config: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	return parseNormalizedConfig(body, allowedDomains)
}

// LoadNormalizedConfigFromFile loads configuration from a local file,
// sniffing whether it is a v1 or v2 config and validating it accordingly.
func LoadNormalizedConfigFromFile(configPath string, allowedDomains []string) (*NormalizedConfig, error) {
	body, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}

	return parseNormalizedConfig(body, allowedDomains)
}

func parseNormalizedConfig(body []byte, allowedDomains []string) (*NormalizedConfig, error) {
	if isConfigV2(body) {
		var cfg ConfigV2
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}
		if err := cfg.Validate(allowedDomains); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
		return &NormalizedConfig{Version: 2, V2: &cfg}, nil
	}

	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if err := cfg.ValidateWithAllowedDomains(allowedDomains); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &NormalizedConfig{Version: 1, V1: &cfg}, nil
}

// isConfigV2 sniffs the "version" field without committing to a full parse.
// Anything other than exactly 2 (including a missing key, or invalid JSON
// that will fail again during the real parse) is treated as v1, matching
// today's behavior for existing configs.
func isConfigV2(body []byte) bool {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Version == 2
}

// Validate checks a v2 config for structural and semantic errors. It also
// applies defaults: an empty DuplicateHandling becomes "ignore", matching
// the JAR's behavior when neither --logDroppedDuplicates nor
// --errorOnDroppedDuplicates is passed.
func (c *ConfigV2) Validate(allowedDomains []string) error {
	if len(c.Feeds) == 0 {
		return fmt.Errorf("no feeds specified")
	}

	seenIDs := make(map[string]bool, len(c.Feeds))
	for _, feed := range c.Feeds {
		if feed.ID == "" {
			return fmt.Errorf("feed missing required 'id'")
		}
		if !feedIDPattern.MatchString(feed.ID) {
			return fmt.Errorf("invalid feed id '%s': must match ^[a-z0-9_-]+$", feed.ID)
		}
		if seenIDs[feed.ID] {
			return fmt.Errorf("duplicate feed id '%s'", feed.ID)
		}
		seenIDs[feed.ID] = true

		if err := validateURL(feed.URL, allowedDomains); err != nil {
			return fmt.Errorf("invalid feed URL '%s': %w", feed.URL, err)
		}

		if feed.PairedWith != nil {
			if err := validateURL(feed.PairedWith.URL, allowedDomains); err != nil {
				return fmt.Errorf("invalid pairedWith URL '%s': %w", feed.PairedWith.URL, err)
			}
		}
	}

	if c.Output.Key == "" {
		return fmt.Errorf("output.key is required")
	}
	if strings.Contains(c.Output.Key, "..") {
		return fmt.Errorf("output.key must not contain '..'")
	}
	if c.Output.ReportKey == "" {
		return fmt.Errorf("output.reportKey is required")
	}
	if strings.Contains(c.Output.ReportKey, "..") {
		return fmt.Errorf("output.reportKey must not contain '..'")
	}
	if c.Output.Key == c.Output.ReportKey {
		return fmt.Errorf("output.key and output.reportKey must not be the same")
	}

	if c.MergeSettings.DuplicateHandling == "" {
		c.MergeSettings.DuplicateHandling = "ignore"
	}
	if !validDuplicateHandling[c.MergeSettings.DuplicateHandling] {
		return fmt.Errorf("invalid duplicateHandling '%s'", c.MergeSettings.DuplicateHandling)
	}

	for file, setting := range c.MergeSettings.Files {
		if parent, ok := followsFileParent[file]; ok {
			return fmt.Errorf("%s has no independent strategy; it follows %s", file, parent)
		}
		if !isIndependentMergeFile(file) {
			return fmt.Errorf("unsupported merge file '%s'", file)
		}
		if !validDetection[setting.Detection] {
			return fmt.Errorf("invalid detection strategy '%s' for file '%s'", setting.Detection, file)
		}
		// Renaming is optional: an empty value is left as-is (meaning "use
		// the JAR's own default"), but a non-empty value must still be one
		// of the recognized strategies.
		if setting.Renaming != "" && !validRenaming[setting.Renaming] {
			return fmt.Errorf("invalid renaming strategy '%s' for file '%s'", setting.Renaming, file)
		}
	}

	seenAdditionalFilenames := make(map[string]bool, len(c.AdditionalFiles))
	for _, af := range c.AdditionalFiles {
		if af.Filename == "" || reservedFilenames[af.Filename] || !additionalFilenamePattern.MatchString(af.Filename) {
			return fmt.Errorf("invalid additionalFiles filename '%s'", af.Filename)
		}
		if seenAdditionalFilenames[af.Filename] {
			return fmt.Errorf("duplicate additionalFiles filename '%s'", af.Filename)
		}
		seenAdditionalFilenames[af.Filename] = true

		if err := validateURL(af.URL, allowedDomains); err != nil {
			return fmt.Errorf("invalid additionalFiles URL '%s': %w", af.URL, err)
		}
	}

	return nil
}
