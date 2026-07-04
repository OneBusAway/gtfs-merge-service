package report

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeGTFSZip creates a zip file at path containing entries (filename ->
// full file content, including header row). It's the shared fixture
// builder for this package's tests: every fixture GTFS zip used below is
// built in-test with this helper, never checked in as a binary testdata
// file.
func writeGTFSZip(t *testing.T, path string, files map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create zip %s: %v", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer for %s: %v", path, err)
	}
}

// newGTFSZip is writeGTFSZip plus returning the path, for tests that don't
// need to name the file themselves.
func newGTFSZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	writeGTFSZip(t, path, files)
	return path
}
