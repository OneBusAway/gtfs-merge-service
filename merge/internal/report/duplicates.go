package report

import (
	"regexp"
	"strings"
)

// droppedDuplicateRE matches the merge JAR's dropped-duplicate warning line,
// emitted by AbstractSingleEntityMergeStrategy.logDuplicateEntity (in
// onebusaway-gtfs-merge, gtfs-modules repo) only when
// mergeSettings.duplicateHandling is "log" (--logDroppedDuplicates):
//
//	_log.warn("duplicate entity: type=" + _entityType + " id=" + id);
//
// _entityType is a java.lang.Class, so string concatenation invokes its
// toString(), which prints "class " (or "interface ") ahead of the fully
// qualified name — e.g. "class org.onebusaway.gtfs.model.Stop". id is the
// dropped entity's own raw id (never the id of the entity it was kept in
// favor of — the JAR's log line doesn't include that), stringified via
// AgencyAndId.toString() ("agencyId_id", e.g. "1_1234") for identifiable
// entities. This is verified against the actual source, not guessed; the
// non-identifiable-entity strategies (FareRule, Frequency, Transfer) share
// the same log call, but by the time it runs their generated integer id has
// already been reset to 0 (see resetGeneratedIds), so their "id=" is always
// "0" and carries no useful information.
//
// A parallel "duplicate key: type=... key=..." message exists for the
// *collection*-based merge strategies (calendar.txt/calendar_dates.txt,
// shapes.txt — AbstractCollectionEntityMergeStrategy), but it's a distinct
// log format from "duplicate entity:" and is intentionally not parsed here
// (the milestone only asked for the "duplicate entity:" format that
// AbstractSingleEntityMergeStrategy actually emits).
var droppedDuplicateRE = regexp.MustCompile(`duplicate entity: type=(?:class |interface )?(\S+) id=(.*)$`)

// entityTypeToFile maps the merge JAR's fully qualified entity class name
// (as it appears in a dropped-duplicate log line) to the GTFS filename it
// corresponds to. Built directly from each *MergeStrategy's constructor
// call in onebusaway-gtfs-merge's strategies/ package (e.g.
// StopMergeStrategy's `super(Stop.class)`).
var entityTypeToFile = map[string]string{
	"org.onebusaway.gtfs.model.Agency":        "agency.txt",
	"org.onebusaway.gtfs.model.Area":          "areas.txt",
	"org.onebusaway.gtfs.model.FareAttribute": "fare_attributes.txt",
	"org.onebusaway.gtfs.model.FareRule":      "fare_rules.txt",
	"org.onebusaway.gtfs.model.FeedInfo":      "feed_info.txt",
	"org.onebusaway.gtfs.model.Frequency":     "frequencies.txt",
	"org.onebusaway.gtfs.model.Route":         "routes.txt",
	"org.onebusaway.gtfs.model.Stop":          "stops.txt",
	"org.onebusaway.gtfs.model.Transfer":      "transfers.txt",
	"org.onebusaway.gtfs.model.Trip":          "trips.txt",
}

// fileForEntityType resolves a Java entity class name (fully qualified, as
// printed by Class.toString() minus its "class "/"interface " prefix) to a
// GTFS filename, falling back to the class's simple name (stripped of its
// package) when the type isn't one of ours to keep — still useful, if
// unexpected — information rather than silently dropping it.
func fileForEntityType(entityType string) string {
	if file, ok := entityTypeToFile[entityType]; ok {
		return file
	}
	if idx := strings.LastIndexByte(entityType, '.'); idx != -1 {
		return entityType[idx+1:]
	}
	return entityType
}

// parseDroppedDuplicates scans mergeOutput (the merge JAR's combined
// stdout+stderr, captured via merge.Merger.CapturedOutput) for
// "duplicate entity:" lines, parsing up to limit of them into
// DroppedDuplicate entries. truncated is true if more lines matched than
// limit. A matching line that the regex can't further decompose is still
// recorded (raw only, parsed omitted) rather than dropped, per
// docs/config-schema.md §3.1 ("parsed... may be absent if the line
// couldn't be parsed").
func parseDroppedDuplicates(mergeOutput string, limit int) (dropped []DroppedDuplicate, truncated bool) {
	dropped = []DroppedDuplicate{}
	if mergeOutput == "" {
		return dropped, false
	}

	for _, line := range strings.Split(mergeOutput, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.Contains(line, "duplicate entity:") {
			continue
		}

		if len(dropped) >= limit {
			truncated = true
			continue
		}

		entry := DroppedDuplicate{Raw: line}
		if m := droppedDuplicateRE.FindStringSubmatch(line); m != nil {
			entityType, id := m[1], strings.TrimSpace(m[2])
			entry.File = fileForEntityType(entityType)
			entry.Parsed = &DroppedDuplicateParsed{ID: id}
		}
		dropped = append(dropped, entry)
	}

	return dropped, truncated
}
