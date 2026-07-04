package download

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake zip contents"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := New(filepath.Join(dir, "work"))

	path, err := d.DownloadFile(srv.URL, "translations.txt")
	if err != nil {
		t.Fatalf("DownloadFile() error: %v", err)
	}

	wantPath := filepath.Join(dir, "work", "translations.txt")
	if path != wantPath {
		t.Errorf("DownloadFile() path = %q, want %q", path, wantPath)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(got) != "fake zip contents" {
		t.Errorf("downloaded content = %q, want %q", string(got), "fake zip contents")
	}
}

func TestDownloadFileErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := New(dir)

	if _, err := d.DownloadFile(srv.URL, "missing.txt"); err == nil {
		t.Error("expected error for non-200 status, got none")
	}
}
