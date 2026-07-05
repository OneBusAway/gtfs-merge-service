// Package pairmerge pre-merges a feed's "current" and "upcoming" signup
// zips (feeds[].url and feeds[].pairedWith.url) into a single working zip,
// before that feed enters the prepare (transform) stage and the main
// multi-feed merge. See docs/config-schema.md §1.2's pairedWith field.
package pairmerge

import (
	"fmt"
	"path/filepath"

	"github.com/onebusaway/gtfs-merge-service/internal/javacmd"
)

// PairMerger pre-merges two signups for a single feed using merge-cli.jar's
// default settings (no --file/--duplicateDetection/--duplicateRenaming
// flags — the JAR's own auto-detection applies).
type PairMerger struct {
	jarPath string
	tempDir string
}

// New returns a PairMerger that invokes the merge jar at jarPath, writing
// paired zips under tempDir. jarPath is configurable so tests can stub it.
func New(jarPath, tempDir string) *PairMerger {
	return &PairMerger{
		jarPath: jarPath,
		tempDir: tempDir,
	}
}

// Merge pre-merges currentPath (the feed's primary url) and upcomingPath
// (pairedWith.url) into paired_<feedID>.zip, which becomes that feed's
// working zip entering the prepare stage.
//
// Argument order is deliberate: current signup first, upcoming signup last.
// The merge CLI's entity merge strategies resolve ID collisions in reverse
// argument order — the last input wins collisions and keeps its native,
// unrenamed IDs, while the earlier input's colliding entities are dropped
// or renamed with the JAR's default index-derived prefixing (a-, b-, ...;
// see docs/config-schema.md §1.3). Putting upcoming last means the
// forward-looking signup's IDs survive unrenamed, which is what downstream
// transform rules and reporting expect to see as "the" ID for that feed.
func (p *PairMerger) Merge(feedID, currentPath, upcomingPath string) (string, error) {
	outputPath := filepath.Join(p.tempDir, fmt.Sprintf("paired_%s.zip", feedID))
	args := p.buildArgs(currentPath, upcomingPath, outputPath)

	if err := javacmd.Run("pair-merge", args, nil, nil); err != nil {
		return "", fmt.Errorf("pair-merge failed for feed %s: %w", feedID, err)
	}

	if err := javacmd.VerifyOutputExists("pair-merge", outputPath); err != nil {
		return "", err
	}

	return outputPath, nil
}

// buildArgs constructs the java argv for a pair-merge invocation (JVM flags
// come from the shared javacmd.OptsArgs). No --file/
// --duplicateDetection/--duplicateRenaming flags are ever added here — this
// pre-merge always uses the JAR's defaults.
func (p *PairMerger) buildArgs(currentPath, upcomingPath, outputPath string) []string {
	args := javacmd.OptsArgs()
	args = append(args, "-jar", p.jarPath, currentPath, upcomingPath, outputPath)
	return args
}
