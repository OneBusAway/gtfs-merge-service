package merge

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDuplicateLineCaptureFiltersNonMatchingLines(t *testing.T) {
	var c duplicateLineCapture
	input := "Running merge command: java -jar merge-cli.jar ...\n" +
		"12:00:00.000 [main] WARN  - duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234\n" +
		"Merge complete.\n"

	n, err := c.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(input) {
		t.Errorf("Write() n = %d, want %d", n, len(input))
	}

	lines, truncated := c.Lines()
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	want := "12:00:00.000 [main] WARN  - duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234"
	if lines != want {
		t.Errorf("Lines() = %q, want %q", lines, want)
	}
}

func TestDuplicateLineCaptureHandlesPartialLinesAcrossWrites(t *testing.T) {
	var c duplicateLineCapture

	// Split a single "duplicate entity:" line across three Write calls, as
	// exec.Cmd's io.MultiWriter fan-out might deliver it in arbitrary
	// chunks.
	full := "duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234\n"
	chunk1 := full[:10]
	chunk2 := full[10:40]
	chunk3 := full[40:]

	for _, chunk := range []string{chunk1, chunk2, chunk3} {
		if _, err := c.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	lines, truncated := c.Lines()
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	want := "duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234"
	if lines != want {
		t.Errorf("Lines() = %q, want %q", lines, want)
	}
}

func TestDuplicateLineCaptureIncompleteTrailingLineIsExcluded(t *testing.T) {
	var c duplicateLineCapture

	// No trailing newline: this line is never "completed", so it must not
	// show up in Lines() yet.
	if _, err := c.Write([]byte("duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	lines, _ := c.Lines()
	if lines != "" {
		t.Errorf("Lines() = %q, want empty (line never terminated by newline)", lines)
	}

	// Now terminate it - it should appear.
	if _, err := c.Write([]byte("\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	lines, _ = c.Lines()
	want := "duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234"
	if lines != want {
		t.Errorf("Lines() = %q, want %q", lines, want)
	}
}

func TestDuplicateLineCaptureTruncatesAtLimit(t *testing.T) {
	var c duplicateLineCapture

	var sb strings.Builder
	for i := 0; i < DroppedDuplicatesLimit+1; i++ {
		fmt.Fprintf(&sb, "duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_%d\n", i)
	}

	if _, err := c.Write([]byte(sb.String())); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	lines, truncated := c.Lines()
	if !truncated {
		t.Errorf("truncated = false, want true")
	}
	got := strings.Split(lines, "\n")
	if len(got) != DroppedDuplicatesLimit {
		t.Errorf("len(retained lines) = %d, want %d", len(got), DroppedDuplicatesLimit)
	}
}

func TestDuplicateLineCaptureMultipleWritesRespectLimit(t *testing.T) {
	var c duplicateLineCapture

	// Feed the same total number of matching lines as the previous test,
	// but split across many small Write calls, to make sure the limit is
	// enforced across calls, not just within one.
	for i := 0; i < DroppedDuplicatesLimit+10; i++ {
		line := fmt.Sprintf("duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_%d\n", i)
		if _, err := c.Write([]byte(line)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	lines, truncated := c.Lines()
	if !truncated {
		t.Errorf("truncated = false, want true")
	}
	got := strings.Split(lines, "\n")
	if len(got) != DroppedDuplicatesLimit {
		t.Errorf("len(retained lines) = %d, want %d", len(got), DroppedDuplicatesLimit)
	}
}

func TestDuplicateLineCaptureEmptyWhenNothingWritten(t *testing.T) {
	var c duplicateLineCapture
	lines, truncated := c.Lines()
	if lines != "" || truncated {
		t.Errorf("Lines() = (%q, %v), want (\"\", false)", lines, truncated)
	}
}

// TestDuplicateLineCaptureConcurrentWritesAreRace safe reproduces the real
// shape of the data race this capture is exposed to: Merger.run passes the
// same *duplicateLineCapture as both extraStdout and extraStderr to
// javacmd.Run, which os/exec then copies into from two separate goroutines
// (one for the child's stdout, one for its stderr) concurrently. Without
// duplicateLineCapture's mutex, `go test -race` flags this as a data race
// on the shared bytes.Buffer/slice fields; with it, the two goroutines'
// writes are serialized and every "duplicate entity:" line from both
// streams is retained.
func TestDuplicateLineCaptureConcurrentWritesAreRaceSafe(t *testing.T) {
	var c duplicateLineCapture

	const linesPerGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(2)

	writeLines := func(label string) {
		defer wg.Done()
		for i := 0; i < linesPerGoroutine; i++ {
			line := fmt.Sprintf("duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=%s_%d\n", label, i)
			if _, err := c.Write([]byte(line)); err != nil {
				t.Errorf("Write() error = %v", err)
				return
			}
		}
	}

	go writeLines("stdout")
	go writeLines("stderr")
	wg.Wait()

	lines, truncated := c.Lines()
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	got := strings.Split(lines, "\n")
	if len(got) != 2*linesPerGoroutine {
		t.Errorf("len(retained lines) = %d, want %d (concurrent writes from both streams must both be captured)", len(got), 2*linesPerGoroutine)
	}
}

func TestDuplicateLineCaptureResetClearsState(t *testing.T) {
	var c duplicateLineCapture
	if _, err := c.Write([]byte("duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	c.reset()

	lines, truncated := c.Lines()
	if lines != "" || truncated {
		t.Errorf("Lines() after reset() = (%q, %v), want (\"\", false)", lines, truncated)
	}
}
