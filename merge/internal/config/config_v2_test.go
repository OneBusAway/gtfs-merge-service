package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goldenV2Config = `{
	"version": 2,
	"output": {
		"key": "combined-feeds/12/5/builds/49/gtfs.zip",
		"reportKey": "combined-feeds/12/5/builds/49/report.json"
	},
	"feeds": [
		{
			"id": "everett",
			"name": "Everett Transit",
			"url": "https://example.com/everett.zip",
			"prefix": "97-",
			"transformRules": [
				{"op": "update", "match": {"file": "agency.txt", "agency_id": "1"}, "update": {"agency_id": "97"}}
			],
			"pairedWith": {"url": "https://example.com/everett-upcoming.zip"}
		},
		{
			"id": "sound-transit",
			"name": "Sound Transit",
			"url": "https://example.com/sound-transit.zip"
		}
	],
	"sharedTransformRules": [
		{"op": "remove_unused_routes"}
	],
	"mergeSettings": {
		"duplicateHandling": "log",
		"files": {
			"stops.txt": {"detection": "fuzzy", "renaming": "agency"},
			"trips.txt": {"detection": "identity", "renaming": "agency"}
		}
	},
	"additionalFiles": [
		{"filename": "translations.txt", "url": "https://example.com/translations.txt"}
	]
}`

func validConfigV2() ConfigV2 {
	return ConfigV2{
		Version: 2,
		Output: OutputV2{
			Key:       "combined-feeds/12/5/builds/49/gtfs.zip",
			ReportKey: "combined-feeds/12/5/builds/49/report.json",
		},
		Feeds: []FeedV2{
			{ID: "everett", URL: "https://example.com/everett.zip"},
			{ID: "sound-transit", URL: "https://example.com/sound-transit.zip"},
		},
		MergeSettings: MergeSettingsV2{
			DuplicateHandling: "log",
			Files: map[string]FileMergeSettingV2{
				"stops.txt": {Detection: "fuzzy", Renaming: "agency"},
			},
		},
	}
}

func TestConfigV2GoldenParses(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(goldenV2Config), 0644); err != nil {
		t.Fatal(err)
	}

	normalized, err := LoadNormalizedConfigFromFile(configPath, []string{"example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !normalized.IsV2() {
		t.Fatalf("expected version 2, got %d", normalized.Version)
	}
	if normalized.V2 == nil {
		t.Fatal("expected V2 to be populated")
	}
	if got, want := len(normalized.V2.Feeds), 2; got != want {
		t.Errorf("expected %d feeds, got %d", want, got)
	}
	if got, want := normalized.V2.Output.Key, "combined-feeds/12/5/builds/49/gtfs.zip"; got != want {
		t.Errorf("expected output.key %q, got %q", want, got)
	}
	if got, want := normalized.V2.Feeds[0].PairedWith.URL, "https://example.com/everett-upcoming.zip"; got != want {
		t.Errorf("expected pairedWith.url %q, got %q", want, got)
	}
	if len(normalized.V2.Feeds[0].TransformRules) != 1 {
		t.Errorf("expected 1 transform rule on feed 0, got %d", len(normalized.V2.Feeds[0].TransformRules))
	}
	if len(normalized.V2.SharedTransformRules) != 1 {
		t.Errorf("expected 1 shared transform rule, got %d", len(normalized.V2.SharedTransformRules))
	}
}

func TestConfigV2Validate(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*ConfigV2)
		allowed     []string
		errContains string
	}{
		{
			name:   "valid config",
			mutate: func(c *ConfigV2) {},
		},
		{
			name: "empty feeds",
			mutate: func(c *ConfigV2) {
				c.Feeds = nil
			},
			errContains: "no feeds specified",
		},
		{
			name: "feed missing id",
			mutate: func(c *ConfigV2) {
				c.Feeds[0].ID = ""
			},
			errContains: "missing required 'id'",
		},
		{
			name: "feed id uppercase",
			mutate: func(c *ConfigV2) {
				c.Feeds[0].ID = "Everett"
			},
			errContains: "must match ^[a-z0-9_-]+$",
		},
		{
			name: "feed id with spaces",
			mutate: func(c *ConfigV2) {
				c.Feeds[0].ID = "everett transit"
			},
			errContains: "must match ^[a-z0-9_-]+$",
		},
		{
			name: "duplicate feed id",
			mutate: func(c *ConfigV2) {
				c.Feeds[1].ID = c.Feeds[0].ID
			},
			errContains: "duplicate feed id",
		},
		{
			name: "disallowed feed domain",
			mutate: func(c *ConfigV2) {
				c.Feeds[0].URL = "https://malicious.com/feed.zip"
			},
			errContains: "invalid feed URL",
		},
		{
			name: "disallowed pairedWith domain",
			mutate: func(c *ConfigV2) {
				c.Feeds[0].PairedWith = &PairedFeedV2{URL: "https://malicious.com/feed.zip"}
			},
			errContains: "invalid pairedWith URL",
		},
		{
			name: "missing output key",
			mutate: func(c *ConfigV2) {
				c.Output.Key = ""
			},
			errContains: "output.key is required",
		},
		{
			name: "output key with dot-dot",
			mutate: func(c *ConfigV2) {
				c.Output.Key = "../escape/gtfs.zip"
			},
			errContains: "output.key must not contain",
		},
		{
			name: "missing output reportKey",
			mutate: func(c *ConfigV2) {
				c.Output.ReportKey = ""
			},
			errContains: "output.reportKey is required",
		},
		{
			name: "output reportKey with dot-dot",
			mutate: func(c *ConfigV2) {
				c.Output.ReportKey = "../escape/report.json"
			},
			errContains: "output.reportKey must not contain",
		},
		{
			name: "invalid duplicateHandling",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.DuplicateHandling = "explode"
			},
			errContains: "invalid duplicateHandling",
		},
		{
			name: "empty duplicateHandling defaults to ignore",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.DuplicateHandling = ""
			},
		},
		{
			name: "stop_times.txt is a follows-file",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["stop_times.txt"] = FileMergeSettingV2{Detection: "identity", Renaming: "agency"}
			},
			errContains: "stop_times.txt has no independent strategy; it follows trips.txt",
		},
		{
			name: "calendar_dates.txt is a follows-file",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["calendar_dates.txt"] = FileMergeSettingV2{Detection: "identity", Renaming: "agency"}
			},
			errContains: "calendar_dates.txt has no independent strategy; it follows calendar.txt",
		},
		{
			name: "unsupported merge file",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["pathways.txt"] = FileMergeSettingV2{Detection: "identity", Renaming: "agency"}
			},
			errContains: "unsupported merge file 'pathways.txt'",
		},
		{
			name: "invalid detection strategy",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["stops.txt"] = FileMergeSettingV2{Detection: "bogus", Renaming: "agency"}
			},
			errContains: "invalid detection strategy",
		},
		{
			name: "invalid renaming strategy",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["stops.txt"] = FileMergeSettingV2{Detection: "identity", Renaming: "bogus"}
			},
			errContains: "invalid renaming strategy",
		},
		{
			name: "empty renaming is valid (optional)",
			mutate: func(c *ConfigV2) {
				c.MergeSettings.Files["stops.txt"] = FileMergeSettingV2{Detection: "identity", Renaming: ""}
			},
		},
		{
			name: "additionalFiles bad filename with path separator",
			mutate: func(c *ConfigV2) {
				c.AdditionalFiles = []AdditionalFileV2{{Filename: "../etc/passwd", URL: "https://example.com/x"}}
			},
			errContains: "invalid additionalFiles filename",
		},
		{
			name: "additionalFiles filename is a single dot",
			mutate: func(c *ConfigV2) {
				c.AdditionalFiles = []AdditionalFileV2{{Filename: ".", URL: "https://example.com/x"}}
			},
			errContains: "invalid additionalFiles filename",
		},
		{
			name: "additionalFiles filename is dot-dot",
			mutate: func(c *ConfigV2) {
				c.AdditionalFiles = []AdditionalFileV2{{Filename: "..", URL: "https://example.com/x"}}
			},
			errContains: "invalid additionalFiles filename",
		},
		{
			name: "additionalFiles disallowed domain",
			mutate: func(c *ConfigV2) {
				c.AdditionalFiles = []AdditionalFileV2{{Filename: "translations.txt", URL: "https://malicious.com/x"}}
			},
			errContains: "invalid additionalFiles URL",
		},
		{
			name: "additionalFiles duplicate filenames",
			mutate: func(c *ConfigV2) {
				c.AdditionalFiles = []AdditionalFileV2{
					{Filename: "translations.txt", URL: "https://example.com/x"},
					{Filename: "translations.txt", URL: "https://example.com/y"},
				}
			},
			errContains: "duplicate additionalFiles filename",
		},
		{
			name: "output.key and output.reportKey are the same",
			mutate: func(c *ConfigV2) {
				c.Output.ReportKey = c.Output.Key
			},
			errContains: "output.key and output.reportKey must not be the same",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigV2()
			tt.mutate(&cfg)

			allowed := tt.allowed
			if allowed == nil {
				allowed = []string{"example.com"}
			}

			err := cfg.Validate(allowed)
			if tt.errContains == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got none", tt.errContains)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected error containing %q, got: %v", tt.errContains, err)
			}
		})
	}
}

func TestConfigV2ValidateAppliesDuplicateHandlingDefault(t *testing.T) {
	cfg := validConfigV2()
	cfg.MergeSettings.DuplicateHandling = ""
	if err := cfg.Validate([]string{"example.com"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MergeSettings.DuplicateHandling != "ignore" {
		t.Errorf("expected duplicateHandling to default to 'ignore', got %q", cfg.MergeSettings.DuplicateHandling)
	}
}

func TestVersionSniffing(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		expectVersion int
		expectError   bool
	}{
		{
			name: "v1 without version key",
			configContent: `{
				"feeds": ["https://example.com/feed1.zip"],
				"outputName": "merged.zip"
			}`,
			expectVersion: 1,
		},
		{
			name: "v1 with feeds as strings and mergeStrategies",
			configContent: `{
				"feeds": ["https://example.com/feed1.zip", "https://example.com/feed2.zip"],
				"mergeStrategies": {"agency.txt": "identity"},
				"outputName": "merged.zip"
			}`,
			expectVersion: 1,
		},
		{
			name:          "v2 with version 2",
			configContent: goldenV2Config,
			expectVersion: 2,
		},
		{
			name: "unknown top-level key is tolerated",
			configContent: `{
				"version": 2,
				"output": {"key": "out/gtfs.zip", "reportKey": "out/report.json"},
				"feeds": [{"id": "a", "url": "https://example.com/a.zip"}],
				"somethingWeNeverHeardOf": {"nested": true}
			}`,
			expectVersion: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatal(err)
			}

			normalized, err := LoadNormalizedConfigFromFile(configPath, []string{"example.com"})
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if normalized.Version != tt.expectVersion {
				t.Errorf("expected version %d, got %d", tt.expectVersion, normalized.Version)
			}
			if tt.expectVersion == 1 && normalized.V1 == nil {
				t.Error("expected V1 to be populated")
			}
			if tt.expectVersion == 2 && normalized.V2 == nil {
				t.Error("expected V2 to be populated")
			}
		})
	}
}

func TestLoadNormalizedConfigFromFileNonExistent(t *testing.T) {
	_, err := LoadNormalizedConfigFromFile("/non/existent/file.json", []string{"example.com"})
	if err == nil {
		t.Error("expected error for non-existent file, got none")
	}
}

func TestLoadNormalizedConfigFromFileInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{ invalid json`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadNormalizedConfigFromFile(configPath, []string{"example.com"})
	if err == nil {
		t.Error("expected error for invalid JSON, got none")
	}
}
