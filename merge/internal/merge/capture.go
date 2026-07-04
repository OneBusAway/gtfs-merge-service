package merge

import (
	"bytes"
	"strings"
)

// DroppedDuplicatesLimit caps how many "duplicate entity:" lines
// duplicateLineCapture retains from a single merge run. Shared with
// internal/report (report.Generate uses this exact constant when calling
// parseDroppedDuplicates) so report.json's droppedDuplicates cap
// (docs/config-schema.md §3.1) and the capture that feeds it never drift
// apart.
const DroppedDuplicatesLimit = 500

// duplicateEntityMarker is the substring that identifies a merge JAR
// dropped-duplicate warning line; see internal/report/duplicates.go's
// droppedDuplicateRE doc comment for the full log line shape
// (AbstractSingleEntityMergeStrategy.logDuplicateEntity).
const duplicateEntityMarker = "duplicate entity:"

// duplicateLineCapture is an io.Writer that scans written bytes for
// complete lines and retains only those containing duplicateEntityMarker,
// up to DroppedDuplicatesLimit of them. It exists so Merger.run can tee the
// merge JAR's combined stdout+stderr for report.json's dropped-duplicate
// parsing (internal/report) without holding the JAR's entire, potentially
// unbounded, console output in memory — a real merge run can log far more
// than 500 lines total even though only "duplicate entity:" lines are ever
// useful downstream.
//
// Write never errors: it always reports (len(p), nil), matching the "sink"
// contract expected of an io.MultiWriter fan-out target, so a bug here can
// never affect the live stdout/stderr streaming that Merger.run also does.
type duplicateLineCapture struct {
	partial   bytes.Buffer // an incomplete line carried over between Write calls
	retained  []string
	truncated bool
}

// Write implements io.Writer. It reassembles lines split across separate
// Write calls (a single log line is not guaranteed to arrive in one Write),
// filters each complete line, and buffers any trailing partial line for the
// next call.
func (c *duplicateLineCapture) Write(p []byte) (int, error) {
	data := p
	for {
		idx := bytes.IndexByte(data, '\n')
		if idx == -1 {
			c.partial.Write(data)
			break
		}

		if c.partial.Len() > 0 {
			c.partial.Write(data[:idx])
			c.addLine(c.partial.String())
			c.partial.Reset()
		} else {
			c.addLine(string(data[:idx]))
		}

		data = data[idx+1:]
	}

	return len(p), nil
}

// addLine records line if it contains duplicateEntityMarker, up to
// DroppedDuplicatesLimit; beyond that it just flips truncated.
func (c *duplicateLineCapture) addLine(line string) {
	line = strings.TrimRight(line, "\r")
	if !strings.Contains(line, duplicateEntityMarker) {
		return
	}

	if len(c.retained) >= DroppedDuplicatesLimit {
		c.truncated = true
		return
	}
	c.retained = append(c.retained, line)
}

// Lines returns the retained "duplicate entity:" lines joined by "\n", and
// whether more than DroppedDuplicatesLimit such lines were seen overall. Any
// not-yet-newline-terminated trailing partial line is intentionally
// excluded — in practice the JAR's dropped-duplicate warnings are always
// complete, newline-terminated log lines by the time the process exits.
func (c *duplicateLineCapture) Lines() (lines string, truncated bool) {
	return strings.Join(c.retained, "\n"), c.truncated
}

// reset clears all state, preparing the capture for a new merge run.
func (c *duplicateLineCapture) reset() {
	c.partial.Reset()
	c.retained = nil
	c.truncated = false
}
