package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromFile(t *testing.T) {
	tests := []struct {
		name           string
		configContent  string
		allowedDomains []string
		expectError    bool
		expectedFeeds  int
	}{
		{
			name: "Valid config file",
			configContent: `{
				"feeds": [
					"https://example.com/feed1.zip",
					"https://example.com/feed2.zip"
				],
				"mergeStrategies": {
					"agency.txt": "identity",
					"stops.txt": "fuzzy"
				},
				"outputName": "merged.zip"
			}`,
			allowedDomains: []string{"example.com"},
			expectError:    false,
			expectedFeeds:  2,
		},
		{
			name: "Invalid JSON",
			configContent: `{
				"feeds": [invalid json
			}`,
			allowedDomains: []string{"example.com"},
			expectError:    true,
		},
		{
			name: "Empty feeds",
			configContent: `{
				"feeds": [],
				"outputName": "merged.zip"
			}`,
			allowedDomains: []string{"example.com"},
			expectError:    true, // Should fail validation
		},
		{
			name: "Disallowed domain",
			configContent: `{
				"feeds": [
					"https://malicious.com/feed.zip"
				],
				"outputName": "merged.zip"
			}`,
			allowedDomains: []string{"example.com"},
			expectError:    true,
		},
		{
			name: "Invalid merge strategy",
			configContent: `{
				"feeds": [
					"https://example.com/feed.zip"
				],
				"mergeStrategies": {
					"agency.txt": "invalid-strategy"
				},
				"outputName": "merged.zip"
			}`,
			allowedDomains: []string{"example.com"},
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfigFromFile(configPath, tt.allowedDomains)

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

func TestLoadConfigFromURL(t *testing.T) {
	tests := []struct {
		name           string
		configContent  string
		statusCode     int
		allowedDomains []string
		expectError    bool
		expectedFeeds  int
	}{
		{
			name: "Valid config from URL",
			configContent: `{
				"feeds": [
					"https://example.com/feed1.zip",
					"https://example.com/feed2.zip"
				],
				"outputName": "merged.zip"
			}`,
			statusCode:     http.StatusOK,
			allowedDomains: []string{"example.com", "127.0.0.1"},
			expectError:    false,
			expectedFeeds:  2,
		},
		{
			name:           "Server returns 404",
			configContent:  `{}`,
			statusCode:     http.StatusNotFound,
			allowedDomains: []string{"example.com", "127.0.0.1"},
			expectError:    true,
		},
		{
			name:           "Invalid JSON from server",
			configContent:  `{invalid json}`,
			statusCode:     http.StatusOK,
			allowedDomains: []string{"example.com", "127.0.0.1"},
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.configContent))
			}))
			defer server.Close()

			cfg, err := LoadConfigFromURL(server.URL, tt.allowedDomains)

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

func TestLoadConfigFromFileNonExistent(t *testing.T) {
	_, err := LoadConfigFromFile("/non/existent/file.json", []string{"example.com"})
	if err == nil {
		t.Errorf("Expected error for non-existent file, got none")
	}
}
