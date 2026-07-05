package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebusaway/gtfs-merge-service/internal/config"
	"github.com/onebusaway/gtfs-merge-service/internal/download"
	"github.com/onebusaway/gtfs-merge-service/internal/merge"
	"github.com/onebusaway/gtfs-merge-service/internal/pairmerge"
)

// installStubJava writes a fake `java` executable into a fresh directory,
// prepends it to PATH for the test's duration, and returns the path to a
// log file the stub appends its argv to (one line per invocation, in
// invocation order). Every java invocation this service makes
// (pairmerge.Merge, merge.Merger.run, transform.Transformer.Transform) ends
// its argv with the expected output file path, so the stub's only real
// behavior is to touch that last argument — enough to satisfy
// javacmd.VerifyOutputExists without a real JVM or any of the merge/
// transformer jars.
func installStubJava(t *testing.T) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub java script assumes a POSIX shell")
	}

	dir := t.TempDir()
	logPath = filepath.Join(dir, "invocations.log")

	script := "#!/bin/sh\n" +
		"if [ -n \"$STUB_JAVA_LOG\" ]; then\n" +
		"  echo \"$@\" >> \"$STUB_JAVA_LOG\"\n" +
		"fi\n" +
		"last=\"\"\n" +
		"for a in \"$@\"; do last=\"$a\"; done\n" +
		"touch \"$last\"\n"

	javaPath := filepath.Join(dir, "java")
	if err := os.WriteFile(javaPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write stub java: %v", err)
	}

	t.Setenv("STUB_JAVA_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return logPath
}

// TestV2PairMergeReplacesPathBeforeCombine covers the seam between
// pairMergeFeeds and combineFeeds: a feed with a pairedWith url must enter
// the combine step using its pair-merged output path, not its raw
// downloaded path, and combineFeeds's argv must still list feeds in
// cfg.Feeds order regardless of which feeds were pair-merged. It uses a
// stubbed `java` on PATH (see installStubJava) plus a local HTTP server
// standing in for the feed URLs, deliberately skipping the
// download/prepare/inject/validate/upload stages that runV2 also runs —
// those are exercised by their own package tests.
func TestV2PairMergeReplacesPathBeforeCombine(t *testing.T) {
	logPath := installStubJava(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake zip contents for " + r.URL.Path))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()

	// "beta" is listed first but has no pairedWith; "alpha" is listed
	// second and does. If combineFeeds's argv followed feedPaths' (map)
	// iteration order instead of cfg.Feeds order, this asymmetry would make
	// the test flaky/wrong; ordering by cfg.Feeds keeps it deterministic.
	cfg := &config.ConfigV2{
		Version: 2,
		Feeds: []config.FeedV2{
			{ID: "beta", URL: srv.URL + "/beta.zip"},
			{ID: "alpha", URL: srv.URL + "/alpha.zip", PairedWith: &config.PairedFeedV2{URL: srv.URL + "/alpha-upcoming.zip"}},
		},
	}

	downloader := download.New(tmpDir)
	feedPaths, err := downloadV2Feeds(cfg, downloader)
	if err != nil {
		t.Fatalf("downloadV2Feeds() error: %v", err)
	}

	pairMerger := pairmerge.New("merge-cli.jar", tmpDir)
	feedPaths, _, err = pairMergeFeeds(cfg, downloader, pairMerger, feedPaths, nil)
	if err != nil {
		t.Fatalf("pairMergeFeeds() error: %v", err)
	}

	wantPairedPath := filepath.Join(tmpDir, "paired_alpha.zip")
	if feedPaths["alpha"] != wantPairedPath {
		t.Errorf("feedPaths[alpha] = %q, want the pair-merged output path %q", feedPaths["alpha"], wantPairedPath)
	}
	if strings.Contains(feedPaths["beta"], "paired_") {
		t.Errorf("feedPaths[beta] = %q, want its unchanged download path (beta has no pairedWith)", feedPaths["beta"])
	}

	merger := merge.New("merge-cli.jar", tmpDir)
	if _, err := combineFeeds(cfg, merger, feedPaths, "gtfs.zip"); err != nil {
		t.Fatalf("combineFeeds() error: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read stub java invocation log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 java invocations (pair-merge + combine), got %d: %q", len(lines), lines)
	}
	// pairMergeFeeds's one invocation (for alpha) runs before combineFeeds's,
	// so the combine invocation is always the log's last line.
	combineArgs := lines[len(lines)-1]

	betaIdx := strings.Index(combineArgs, feedPaths["beta"])
	alphaIdx := strings.Index(combineArgs, wantPairedPath)
	if betaIdx == -1 {
		t.Fatalf("combine argv %q missing beta's feed path %q", combineArgs, feedPaths["beta"])
	}
	if alphaIdx == -1 {
		t.Fatalf("combine argv %q missing alpha's *pair-merged* path %q (used its raw download path instead?)", combineArgs, wantPairedPath)
	}
	if betaIdx > alphaIdx {
		t.Errorf("combine argv = %q, want beta's path before alpha's (cfg.Feeds order: beta, alpha)", combineArgs)
	}
}
