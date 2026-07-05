package report

import (
	"fmt"
	"strings"
	"testing"
)

// These lines mirror exactly what onebusaway-gtfs-merge actually logs (see
// AbstractSingleEntityMergeStrategy.logDuplicateEntity in gtfs-modules):
//
//	_log.warn("duplicate entity: type=" + _entityType + " id=" + id);
//
// _entityType is a java.lang.Class, whose toString() prints "class "
// ahead of the fully qualified name; id is an AgencyAndId, whose
// toString() is "agencyId_id". A realistic captured line also carries
// whatever prefix the logging backend adds (timestamp, level, logger
// name) ahead of the message — parsing must not assume that prefix has
// any particular shape.
const sampleStopDuplicateLine = "12:00:00.000 [main] WARN  o.o.g.m.s.AbstractSingleEntityMergeStrategy - duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1234"

func TestParseDroppedDuplicatesParsesKnownFormat(t *testing.T) {
	dropped, truncated := parseDroppedDuplicates(sampleStopDuplicateLine, 500)

	if truncated {
		t.Errorf("truncated = true, want false")
	}
	if len(dropped) != 1 {
		t.Fatalf("len(dropped) = %d, want 1", len(dropped))
	}

	got := dropped[0]
	if got.File != "stops.txt" {
		t.Errorf("File = %q, want %q", got.File, "stops.txt")
	}
	if got.Raw != sampleStopDuplicateLine {
		t.Errorf("Raw = %q, want the original line unmodified", got.Raw)
	}
	if got.Parsed == nil {
		t.Fatalf("Parsed = nil, want a populated struct")
	}
	if got.Parsed.ID != "1_1234" {
		t.Errorf("Parsed.ID = %q, want %q", got.Parsed.ID, "1_1234")
	}
}

func TestParseDroppedDuplicatesMultipleEntityTypes(t *testing.T) {
	lines := []string{
		"duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_1001",
		"duplicate entity: type=class org.onebusaway.gtfs.model.Route id=1_100",
		"duplicate entity: type=class org.onebusaway.gtfs.model.Trip id=1_T1",
		"duplicate entity: type=class org.onebusaway.gtfs.model.Agency id=1",
		// Non-identifiable entities: the generated integer id is reset to 0
		// before the log call runs (see resetGeneratedIds), so "id=0" is
		// expected and carries no information, not a parse failure.
		"duplicate entity: type=class org.onebusaway.gtfs.model.Frequency id=0",
		// A different log message shape entirely (the collection-strategy
		// "duplicate key:" format) must not be picked up by this parser.
		"duplicate key: type=calendar.txt/calendar_dates.txt service_id key=WK",
	}
	dropped, truncated := parseDroppedDuplicates(strings.Join(lines, "\n"), 500)

	if truncated {
		t.Errorf("truncated = true, want false")
	}
	if len(dropped) != 5 {
		t.Fatalf("len(dropped) = %d, want 5 (the \"duplicate key:\" line must not match)", len(dropped))
	}

	wantFiles := []string{"stops.txt", "routes.txt", "trips.txt", "agency.txt", "frequencies.txt"}
	for i, want := range wantFiles {
		if dropped[i].File != want {
			t.Errorf("dropped[%d].File = %q, want %q", i, dropped[i].File, want)
		}
	}
	if dropped[4].Parsed.ID != "0" {
		t.Errorf("non-identifiable entity Parsed.ID = %q, want \"0\"", dropped[4].Parsed.ID)
	}
}

func TestParseDroppedDuplicatesUnparseableLineKeepsRawOnly(t *testing.T) {
	line := "duplicate entity: something unexpected happened here"
	dropped, _ := parseDroppedDuplicates(line, 500)

	if len(dropped) != 1 {
		t.Fatalf("len(dropped) = %d, want 1", len(dropped))
	}
	if dropped[0].Raw != line {
		t.Errorf("Raw = %q, want %q", dropped[0].Raw, line)
	}
	if dropped[0].Parsed != nil {
		t.Errorf("Parsed = %+v, want nil for an unparseable line", dropped[0].Parsed)
	}
}

func TestParseDroppedDuplicatesTruncatesAt500(t *testing.T) {
	var lines []string
	for i := 0; i < 501; i++ {
		lines = append(lines, fmt.Sprintf("duplicate entity: type=class org.onebusaway.gtfs.model.Stop id=1_%d", i))
	}

	dropped, truncated := parseDroppedDuplicates(strings.Join(lines, "\n"), 500)

	if !truncated {
		t.Errorf("truncated = false, want true")
	}
	if len(dropped) != 500 {
		t.Errorf("len(dropped) = %d, want 500", len(dropped))
	}
}

func TestParseDroppedDuplicatesEmptyOutput(t *testing.T) {
	dropped, truncated := parseDroppedDuplicates("", 500)
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	if len(dropped) != 0 {
		t.Errorf("len(dropped) = %d, want 0", len(dropped))
	}
}

func TestParseDroppedDuplicatesNoMatchingLines(t *testing.T) {
	output := "Running merge command: java -jar merge-cli.jar ...\nMerge complete.\n"
	dropped, truncated := parseDroppedDuplicates(output, 500)
	if truncated {
		t.Errorf("truncated = true, want false")
	}
	if len(dropped) != 0 {
		t.Errorf("len(dropped) = %d, want 0", len(dropped))
	}
}

func TestFileForEntityTypeFallsBackToSimpleName(t *testing.T) {
	got := fileForEntityType("org.onebusaway.gtfs.model.SomeNewEntity")
	if got != "SomeNewEntity" {
		t.Errorf("fileForEntityType() = %q, want %q", got, "SomeNewEntity")
	}
}
