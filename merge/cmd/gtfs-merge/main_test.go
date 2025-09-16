package main

import (
	"os"
	"path/filepath"
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

			cfg, err := loadConfiguration(tt.configURL, configPath, tt.allowedDomains)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if cfg != nil && len(cfg.Feeds) != tt.expectedFeeds {
					t.Errorf("Expected %d feeds, got %d", tt.expectedFeeds, len(cfg.Feeds))
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
