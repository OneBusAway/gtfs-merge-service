package bundleinputs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
)

// writeTempFeed writes deterministic bytes for one fake prepared zip and
// returns its path. Content is fixed so byteSize/sha256 in the golden file
// never drift.
func writeTempFeed(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func buildInput(t *testing.T) (*config.ConfigV2, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	prepared := map[string]string{
		"metro": writeTempFeed(t, dir, "metro_prepared.zip", "metro-bytes"),
		"st":    writeTempFeed(t, dir, "st_prepared.zip", "st-bytes"),
	}
	cfg := &config.ConfigV2{
		Version: 2,
		Output: config.OutputV2{
			Key:             "builds/49/gtfs.zip",
			ReportKey:       "builds/49/report.json",
			BundleInputsKey: "builds/49/bundle-inputs.json",
			FeedKeys: map[string]string{
				"metro": "builds/49/feeds/metro.zip",
				"st":    "builds/49/feeds/st.zip",
			},
		},
		Feeds: []config.FeedV2{
			{ID: "metro", Name: "King County Metro", URL: "https://gtfs.example.com/metro.zip", DefaultAgencyID: "1"},
			{ID: "st", Name: "Sound Transit", URL: "https://gtfs.example.com/st.zip", DefaultAgencyID: "40"},
		},
	}
	return cfg, prepared
}

func fakeObjectURL(key string) string {
	return "https://r2.example.com/bucket/" + key
}

// TestBuildGolden pins the exact bundle-inputs.json bytes: schema version,
// field names, feed order (= cfg.Feeds order), per-build URLs, byteSize and
// sha256 of the prepared files — and, by absence, that Go never emits
// stopConsolidationUrl.
func TestBuildGolden(t *testing.T) {
	cfg, prepared := buildInput(t)

	m, err := Build(cfg, prepared, fakeObjectURL)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	got, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	goldenPath := filepath.Join("testdata", "manifest_golden.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("manifest mismatch.\n--- got ---\n%s\n--- want (%s) ---\n%s", got, goldenPath, want)
	}
}

func TestBuildFeedOrderFollowsConfig(t *testing.T) {
	cfg, prepared := buildInput(t)
	// Reverse the feed order; the manifest must follow.
	cfg.Feeds[0], cfg.Feeds[1] = cfg.Feeds[1], cfg.Feeds[0]

	m, err := Build(cfg, prepared, fakeObjectURL)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if m.Feeds[0].ID != "st" || m.Feeds[1].ID != "metro" {
		t.Errorf("feed order = [%s, %s], want [st, metro]", m.Feeds[0].ID, m.Feeds[1].ID)
	}
}

func TestBuildMissingPreparedPath(t *testing.T) {
	cfg, prepared := buildInput(t)
	delete(prepared, "st")

	if _, err := Build(cfg, prepared, fakeObjectURL); err == nil {
		t.Fatal("Build() = nil error, want error for missing prepared path")
	}
}

func TestBuildUnreadablePreparedFile(t *testing.T) {
	cfg, prepared := buildInput(t)
	prepared["st"] = filepath.Join(t.TempDir(), "does-not-exist.zip")

	if _, err := Build(cfg, prepared, fakeObjectURL); err == nil {
		t.Fatal("Build() = nil error, want error for unreadable prepared file")
	}
}
