package bundleinputs

import (
	"fmt"
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

// recordingUploader records calls; failKey makes the named key fail, for
// asserting abort-on-first-error.
type recordingUploader struct {
	fileCalls  []string // "key<-path"
	bytesCalls []string // "key:contentType:len"
	failKey    string
}

func (r *recordingUploader) UploadFile(filePath, key string) error {
	if key == r.failKey {
		return fmt.Errorf("boom: %s", key)
	}
	r.fileCalls = append(r.fileCalls, key+"<-"+filepath.Base(filePath))
	return nil
}

func (r *recordingUploader) UploadBytes(data []byte, key, contentType string) error {
	if key == r.failKey {
		return fmt.Errorf("boom: %s", key)
	}
	r.bytesCalls = append(r.bytesCalls, fmt.Sprintf("%s:%s:%d", key, contentType, len(data)))
	return nil
}

// TestUpload asserts every prepared zip uploads to its configured key in
// cfg.Feeds order, then the manifest JSON lands at bundleInputsKey as
// application/json.
func TestUpload(t *testing.T) {
	cfg, prepared := buildInput(t)
	m, err := Build(cfg, prepared, fakeObjectURL)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	rec := &recordingUploader{}
	if err := Upload(rec, cfg, prepared, m); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	wantFiles := []string{
		"builds/49/feeds/metro.zip<-metro_prepared.zip",
		"builds/49/feeds/st.zip<-st_prepared.zip",
	}
	if len(rec.fileCalls) != len(wantFiles) {
		t.Fatalf("fileCalls = %v, want %v", rec.fileCalls, wantFiles)
	}
	for i := range wantFiles {
		if rec.fileCalls[i] != wantFiles[i] {
			t.Errorf("fileCalls[%d] = %q, want %q", i, rec.fileCalls[i], wantFiles[i])
		}
	}

	manifestJSON, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	wantBytes := fmt.Sprintf("builds/49/bundle-inputs.json:application/json:%d", len(manifestJSON))
	if len(rec.bytesCalls) != 1 || rec.bytesCalls[0] != wantBytes {
		t.Errorf("bytesCalls = %v, want [%s]", rec.bytesCalls, wantBytes)
	}
}

// TestUploadAbortsOnFirstError pins that a feed-zip failure prevents the
// manifest upload (no manifest pointing at missing zips).
func TestUploadAbortsOnFirstError(t *testing.T) {
	cfg, prepared := buildInput(t)
	m, err := Build(cfg, prepared, fakeObjectURL)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	rec := &recordingUploader{failKey: "builds/49/feeds/metro.zip"}
	err = Upload(rec, cfg, prepared, m)
	if err == nil {
		t.Fatal("Upload() = nil error, want failure")
	}
	if len(rec.bytesCalls) != 0 {
		t.Errorf("manifest uploaded despite feed failure: %v", rec.bytesCalls)
	}
}
