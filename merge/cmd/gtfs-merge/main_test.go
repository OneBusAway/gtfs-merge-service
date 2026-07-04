package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

func TestLoadConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		configURL      string
		configPath     string
		allowedDomains []string
		setupFunc      func() (string, func())
		expectError    bool
		expectedFeeds  int
	}{
		{
			name:           "Load from file path",
			configPath:     "test-config.json",
			allowedDomains: []string{"example.com"},
			setupFunc: func() (string, func()) {
				tmpDir := t.TempDir()
				configFile := filepath.Join(tmpDir, "test-config.json")
				configContent := `{
					"feeds": [
						"https://example.com/feed1.zip",
						"https://example.com/feed2.zip"
					],
					"mergeStrategies": {
						"agency.txt": "identity",
						"stops.txt": "fuzzy"
					},
					"outputName": "test-merged.zip"
				}`
				if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
					t.Fatal(err)
				}
				return configFile, func() {}
			},
			expectError:   false,
			expectedFeeds: 2,
		},
		{
			name:           "Load from non-existent file",
			configPath:     "/non/existent/config.json",
			allowedDomains: []string{"example.com"},
			setupFunc: func() (string, func()) {
				return "/non/existent/config.json", func() {}
			},
			expectError: true,
		},
		{
			name:           "Invalid JSON in file",
			configPath:     "invalid-config.json",
			allowedDomains: []string{"example.com"},
			setupFunc: func() (string, func()) {
				tmpDir := t.TempDir()
				configFile := filepath.Join(tmpDir, "invalid-config.json")
				configContent := `{ invalid json }`
				if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
					t.Fatal(err)
				}
				return configFile, func() {}
			},
			expectError: true,
		},
		{
			name:           "Config with disallowed domain",
			configPath:     "disallowed-domain-config.json",
			allowedDomains: []string{"example.com"},
			setupFunc: func() (string, func()) {
				tmpDir := t.TempDir()
				configFile := filepath.Join(tmpDir, "disallowed-domain-config.json")
				configContent := `{
					"feeds": [
						"https://malicious.com/feed1.zip"
					],
					"outputName": "test-merged.zip"
				}`
				if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
					t.Fatal(err)
				}
				return configFile, func() {}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualPath, cleanup := tt.setupFunc()
			defer cleanup()

			// Use the actual path from setupFunc unless we're testing a specific path
			configPath := tt.configPath
			if tt.configPath == "test-config.json" || tt.configPath == "invalid-config.json" || tt.configPath == "disallowed-domain-config.json" {
				configPath = actualPath
			}

			normalized, err := loadConfiguration(tt.configURL, configPath, tt.allowedDomains)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if normalized != nil && normalized.V1 != nil && len(normalized.V1.Feeds) != tt.expectedFeeds {
					t.Errorf("Expected %d feeds, got %d", tt.expectedFeeds, len(normalized.V1.Feeds))
				}
			}
		})
	}
}

func TestDownloadFeeds(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		expectError bool
		expectCount int
	}{
		{
			name: "Download multiple feeds",
			cfg: &config.Config{
				Feeds: []string{
					"https://example.com/feed1.zip",
					"https://example.com/feed2.zip",
				},
				OutputName: "test-merged.zip",
			},
			expectError: true, // Will fail as we can't actually download from example.com
			expectCount: 0,
		},
		{
			name: "Empty feeds list",
			cfg: &config.Config{
				Feeds:      []string{},
				OutputName: "test-merged.zip",
			},
			expectError: false,
			expectCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			feedFiles, err := downloadFeeds(tt.cfg, tmpDir)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if len(feedFiles) != tt.expectCount {
					t.Errorf("Expected %d feed files, got %d", tt.expectCount, len(feedFiles))
				}
			}
		})
	}
}

func TestLoadConfigurationV2(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "v2-config.json")
	configContent := `{
		"version": 2,
		"output": {"key": "out/gtfs.zip", "reportKey": "out/report.json"},
		"feeds": [
			{"id": "everett", "url": "https://example.com/everett.zip"},
			{"id": "sound-transit", "url": "https://example.com/sound-transit.zip"}
		]
	}`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	normalized, err := loadConfiguration("", configFile, []string{"example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !normalized.IsV2() {
		t.Fatalf("expected version 2, got %d", normalized.Version)
	}
	if normalized.V2 == nil || len(normalized.V2.Feeds) != 2 {
		t.Fatalf("expected 2 v2 feeds, got %+v", normalized.V2)
	}
}

func TestFindJar(t *testing.T) {
	tmpDir := t.TempDir()

	primary := filepath.Join(tmpDir, "primary.jar")
	if err := os.WriteFile(primary, []byte("jar"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("primary path exists", func(t *testing.T) {
		got, err := findJar(primary, "does-not-exist.jar")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != primary {
			t.Errorf("findJar() = %q, want %q", got, primary)
		}
	})

	t.Run("falls back when primary is missing", func(t *testing.T) {
		fallback := filepath.Join(tmpDir, "fallback.jar")
		if err := os.WriteFile(fallback, []byte("jar"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := findJar(filepath.Join(tmpDir, "missing.jar"), fallback)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != fallback {
			t.Errorf("findJar() = %q, want %q", got, fallback)
		}
	})

	t.Run("errors when neither path exists", func(t *testing.T) {
		_, err := findJar(filepath.Join(tmpDir, "missing.jar"), filepath.Join(tmpDir, "also-missing.jar"))
		if err == nil {
			t.Error("expected error, got none")
		}
	})
}

func TestCombinedRules(t *testing.T) {
	shared := []json.RawMessage{json.RawMessage(`{"op":"remove_unused_routes"}`)}
	own := []json.RawMessage{json.RawMessage(`{"op":"remove_current_service"}`)}

	got := combinedRules(shared, own)
	want := []json.RawMessage{
		json.RawMessage(`{"op":"remove_unused_routes"}`),
		json.RawMessage(`{"op":"remove_current_service"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("combinedRules() = %v, want %v (shared rules must come first)", got, want)
	}

	if got := combinedRules(nil, nil); len(got) != 0 {
		t.Errorf("combinedRules(nil, nil) = %v, want empty", got)
	}
}
