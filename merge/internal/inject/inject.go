// Package inject rewrites a merged GTFS zip to add or replace files that
// are copied through verbatim rather than merged or transformed (see
// docs/config-schema.md §1.2's additionalFiles field). It runs after the
// main combine stage and before validation.
package inject

import (
	"archive/zip"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
)

// Inject reads mergedZipPath, replaces (or adds) each entry named in
// additionalFiles — a map from GTFS filename to the local path of the
// already-downloaded replacement content — and writes the result to
// outputPath. Every other entry in mergedZipPath is copied through
// unchanged. additionalFiles is applied in sorted filename order, so the
// output zip's entry order is deterministic across runs. A failure to close
// the underlying output file also fails Inject (surfaced via the named
// return), not just a failure to close the zip.Writer itself.
func Inject(mergedZipPath string, additionalFiles map[string]string, outputPath string) (err error) {
	reader, err := zip.OpenReader(mergedZipPath)
	if err != nil {
		return fmt.Errorf("failed to open merged zip: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output zip: %w", err)
	}
	// Surface a failure to close the underlying output file (e.g. a
	// deferred write error, or the disk filling up) rather than discarding
	// it — but only if nothing earlier already failed, so the first,
	// more-specific error always wins.
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close output zip: %w", cerr)
		}
	}()

	zw := zip.NewWriter(out)

	for _, f := range reader.File {
		if _, replaced := additionalFiles[f.Name]; replaced {
			continue
		}
		if err := copyZipEntry(zw, f); err != nil {
			_ = zw.Close()
			return fmt.Errorf("failed to copy %s: %w", f.Name, err)
		}
	}

	for _, filename := range slices.Sorted(maps.Keys(additionalFiles)) {
		if err := addFileToZip(zw, filename, additionalFiles[filename]); err != nil {
			_ = zw.Close()
			return fmt.Errorf("failed to add %s: %w", filename, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize output zip: %w", err)
	}

	return nil
}

// copyZipEntry copies a single existing entry into zw unchanged. Only Name,
// Method, and Modified are carried over from the original header; CRC32 and
// sizes are recomputed by the zip writer from the copied bytes.
func copyZipEntry(zw *zip.Writer, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() {
		_ = rc.Close()
	}()

	header := &zip.FileHeader{
		Name:     f.Name,
		Method:   f.Method,
		Modified: f.Modified,
	}
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, rc)
	return err
}

// addFileToZip streams localPath's contents into a new zip entry named
// filename.
func addFileToZip(zw *zip.Writer, filename, localPath string) error {
	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	w, err := zw.Create(filename)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, in)
	return err
}
