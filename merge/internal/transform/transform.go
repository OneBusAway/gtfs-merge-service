// Package transform runs the OneBusAway GTFS Transformer CLI
// (transformer-cli.jar) against a single feed's working zip, applying the
// combined list of transform rules for that feed (see
// docs/config-schema.md §2 for the rule vocabulary).
package transform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebusaway/gtfs-merge-service/internal/javacmd"
)

// DefaultJarPath is where the Docker image installs transformer-cli.jar.
const DefaultJarPath = "/app/transformer-cli.jar"

// Transformer prepares a feed by running transformer-cli.jar against it.
type Transformer struct {
	jarPath string
	tempDir string
}

// New returns a Transformer that invokes the transformer jar at jarPath,
// writing rules files and transformed zips under tempDir. jarPath is
// configurable so tests can point it at a stub.
func New(jarPath, tempDir string) *Transformer {
	return &Transformer{
		jarPath: jarPath,
		tempDir: tempDir,
	}
}

// Transform applies rules (already combined: sharedTransformRules ++
// feed.transformRules, in that order) to inputPath and returns the path to
// the transformed zip. If rules is empty, Transform is a no-op and returns
// inputPath unchanged — there is nothing for the transformer JAR to do.
func (t *Transformer) Transform(feedID, inputPath string, rules []json.RawMessage) (string, error) {
	if len(rules) == 0 {
		return inputPath, nil
	}

	rulesPath := filepath.Join(t.tempDir, fmt.Sprintf("rules_%s.txt", feedID))
	if err := writeRulesFile(rulesPath, rules); err != nil {
		return "", fmt.Errorf("failed to write rules file for feed %s: %w", feedID, err)
	}

	outputPath := filepath.Join(t.tempDir, fmt.Sprintf("transformed_%s.zip", feedID))
	args := t.buildArgs(rulesPath, inputPath, outputPath)

	if err := javacmd.Run("transform", args, nil, nil); err != nil {
		return "", fmt.Errorf("transform failed for feed %s: %w", feedID, err)
	}

	if err := javacmd.VerifyOutputExists("transform", outputPath); err != nil {
		return "", err
	}

	return outputPath, nil
}

// buildArgs constructs the java argv for a transform invocation, honoring
// JAVA_OPTS the same way the merge package does (leading JVM flags, split
// on whitespace, ahead of -jar).
func (t *Transformer) buildArgs(rulesPath, inputPath, outputPath string) []string {
	args := javacmd.OptsArgs()
	args = append(args, "-jar", t.jarPath, fmt.Sprintf("--transform=%s", rulesPath), inputPath, outputPath)
	return args
}

// writeRulesFile writes one JSON object per line, verbatim, matching the
// transformer CLI's rule file format (one non-blank, non-comment JSON
// object per line; see docs/config-schema.md §2).
func writeRulesFile(path string, rules []json.RawMessage) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	for _, rule := range rules {
		if _, err := f.Write(rule); err != nil {
			return err
		}
		if _, err := f.Write([]byte("\n")); err != nil {
			return err
		}
	}

	return nil
}
