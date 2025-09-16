package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Feeds           []string          `json:"feeds"`
	MergeStrategies map[string]string `json:"mergeStrategies"`
	OutputName      string            `json:"outputName"`
}

type EnvConfig struct {
	AwsAccessKeyID     string
	AwsSecretAccessKey string
	AwsEndpointURL     string
	S3Bucket           string
	AllowedDomains     []string
}

// LoadConfigFromURL loads configuration from a URL
func LoadConfigFromURL(configURL string, allowedDomains []string) (*Config, error) {
	// Validate URL format and domain
	if err := validateURL(configURL, allowedDomains); err != nil {
		return nil, fmt.Errorf("invalid config URL: %w", err)
	}

	// Create HTTP client with timeout
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

	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.ValidateWithAllowedDomains(allowedDomains); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// LoadConfigFromFile loads configuration from a local file
func LoadConfigFromFile(configPath string, allowedDomains []string) (*Config, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	body, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.ValidateWithAllowedDomains(allowedDomains); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// LoadConfig loads configuration from either a URL or a file path
// Deprecated: Use LoadConfigFromURL or LoadConfigFromFile instead
func LoadConfig(configURL string, allowedDomains []string) (*Config, error) {
	return LoadConfigFromURL(configURL, allowedDomains)
}

func (c *Config) ValidateWithAllowedDomains(allowedDomains []string) error {
	if len(c.Feeds) == 0 {
		return fmt.Errorf("no feeds specified")
	}

	// Validate each feed URL against the allowed domains
	for _, feedURL := range c.Feeds {
		if err := validateURL(feedURL, allowedDomains); err != nil {
			return fmt.Errorf("invalid feed URL '%s': %w", feedURL, err)
		}
	}

	if c.OutputName == "" {
		c.OutputName = "merged-gtfs.zip"
	}

	validStrategies := map[string]bool{
		"identity": true,
		"fuzzy":    true,
		"none":     true,
	}

	for file, strategy := range c.MergeStrategies {
		if !validStrategies[strategy] {
			return fmt.Errorf("invalid merge strategy '%s' for file '%s'", strategy, file)
		}
	}

	return nil
}

func validateURL(urlStr string, allowedDomains []string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	// Only allow http and https schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme '%s', only http/https allowed", parsedURL.Scheme)
	}

	// Check if the domain is in the allowed list
	if len(allowedDomains) > 0 {
		hostname := strings.ToLower(parsedURL.Hostname())
		allowed := false
		for _, domain := range allowedDomains {
			domain = strings.ToLower(strings.TrimSpace(domain))
			// Check exact match or subdomain match
			if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("domain '%s' not in allowed list", hostname)
		}
	}

	return nil
}

func LoadEnvConfig() (*EnvConfig, error) {
	env := &EnvConfig{
		AwsAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		AwsSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		AwsEndpointURL:     os.Getenv("AWS_ENDPOINT_URL"),
		S3Bucket:           os.Getenv("S3_BUCKET"),
	}

	// Parse allowed domains from environment variable
	allowedDomainsStr := os.Getenv("ALLOWED_DOMAINS")
	if allowedDomainsStr != "" {
		env.AllowedDomains = strings.Split(allowedDomainsStr, ",")
		for i := range env.AllowedDomains {
			env.AllowedDomains[i] = strings.TrimSpace(env.AllowedDomains[i])
		}
	}

	if env.AwsAccessKeyID == "" {
		return nil, fmt.Errorf("AWS_ACCESS_KEY_ID not set")
	}
	if env.AwsSecretAccessKey == "" {
		return nil, fmt.Errorf("AWS_SECRET_ACCESS_KEY not set")
	}
	if env.AwsEndpointURL == "" {
		return nil, fmt.Errorf("AWS_ENDPOINT_URL not set")
	}
	if env.S3Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET not set")
	}
	if len(env.AllowedDomains) == 0 {
		return nil, fmt.Errorf("ALLOWED_DOMAINS not set (comma-separated list of allowed domains)")
	}

	return env, nil
}
