package inject

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// writeZip creates a zip file at path containing entries (name -> content).
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()

	zw := zip.NewWriter(f)
	for _, name := range sortedKeys(entries) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(entries[name])); err != nil {
			t.Fatalf("failed to write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
}

// readZip reads a zip file's entries into a name -> content map, and
// returns the entry names in their on-disk order.
func readZip(t *testing.T, path string) (map[string]string, []string) {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()

	entries := make(map[string]string, len(r.File))
	var order []string
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open entry %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("failed to read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = string(content)
		order = append(order, f.Name)
	}
	return entries, order
}

func TestInject(t *testing.T) {
	dir := t.TempDir()

	mergedPath := filepath.Join(dir, "merged.zip")
	writeZip(t, mergedPath, map[string]string{
		"agency.txt":       "agency_id,agency_name\n1,Everett Transit\n",
		"stops.txt":        "stop_id,stop_name\n1,Main St\n",
		"translations.txt": "old translations content",
	})

	// The replacement content for translations.txt (an existing entry,
	// replaced) and a brand-new attributions.txt (added).
	newTranslations := filepath.Join(dir, "new-translations.txt")
	if err := os.WriteFile(newTranslations, []byte("new translations content"), 0644); err != nil {
		t.Fatal(err)
	}
	newAttributions := filepath.Join(dir, "new-attributions.txt")
	if err := os.WriteFile(newAttributions, []byte("attribution content"), 0644); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(dir, "output.zip")
	additionalFiles := map[string]string{
		"translations.txt": newTranslations,
		"attributions.txt": newAttributions,
	}

	if err := Inject(mergedPath, additionalFiles, outputPath); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}

	got, order := readZip(t, outputPath)

	want := map[string]string{
		"agency.txt":       "agency_id,agency_name\n1,Everett Transit\n",
		"stops.txt":        "stop_id,stop_name\n1,Main St\n",
		"translations.txt": "new translations content",
		"attributions.txt": "attribution content",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Inject() output entries =\n  %v\nwant\n  %v", got, want)
	}

	// Unreplaced entries are copied first (in their original order), then
	// additionalFiles are appended in sorted filename order.
	wantOrder := []string{"agency.txt", "stops.txt", "attributions.txt", "translations.txt"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("Inject() output order = %v, want %v", order, wantOrder)
	}
}

func TestInjectNoAdditionalFiles(t *testing.T) {
	dir := t.TempDir()

	mergedPath := filepath.Join(dir, "merged.zip")
	writeZip(t, mergedPath, map[string]string{
		"agency.txt": "agency_id,agency_name\n1,Everett Transit\n",
	})

	outputPath := filepath.Join(dir, "output.zip")
	if err := Inject(mergedPath, nil, outputPath); err != nil {
		t.Fatalf("Inject() error: %v", err)
	}

	got, _ := readZip(t, outputPath)
	want := map[string]string{"agency.txt": "agency_id,agency_name\n1,Everett Transit\n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Inject() output entries = %v, want %v", got, want)
	}
}

func TestInjectMissingMergedZip(t *testing.T) {
	dir := t.TempDir()
	if err := Inject(filepath.Join(dir, "does-not-exist.zip"), nil, filepath.Join(dir, "out.zip")); err == nil {
		t.Error("expected error for missing merged zip, got none")
	}
}

func TestInjectMissingAdditionalFileSource(t *testing.T) {
	dir := t.TempDir()

	mergedPath := filepath.Join(dir, "merged.zip")
	writeZip(t, mergedPath, map[string]string{"agency.txt": "content"})

	err := Inject(mergedPath, map[string]string{"translations.txt": filepath.Join(dir, "missing.txt")}, filepath.Join(dir, "out.zip"))
	if err == nil {
		t.Error("expected error for missing additional file source, got none")
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]string{"c": "3", "a": "1", "b": "2"}
	got := sortedKeys(m)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys() = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("sortedKeys() result is not sorted: %v", got)
	}
}
