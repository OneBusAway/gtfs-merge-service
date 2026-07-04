package merge

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Merger struct {
	jarPath        string
	tempDir        string
	capturedOutput bytes.Buffer
}

func New(jarPath, tempDir string) *Merger {
	return &Merger{
		jarPath: jarPath,
		tempDir: tempDir,
	}
}

// CapturedOutput returns the combined stdout+stderr text from the most
// recent MergeFeeds/MergeFeedsV2 invocation. It's used by report.json
// generation (see internal/report) to parse the merge JAR's
// dropped-duplicate log lines; the output is also still streamed live to
// this process's own stdout/stderr as it always has been (see run).
func (m *Merger) CapturedOutput() string {
	return m.capturedOutput.String()
}

// FileSetting is a per-file duplicate detection/renaming strategy pair, used
// by the v2 merge path (see docs/config-schema.md §1.5).
type FileSetting struct {
	Detection string
	Renaming  string
}

// javaOptsArgs returns the leading JVM flags from the JAVA_OPTS environment
// variable, split on whitespace, or nil if it is unset.
func javaOptsArgs() []string {
	javaOpts := os.Getenv("JAVA_OPTS")
	if javaOpts == "" {
		return nil
	}
	return strings.Fields(javaOpts)
}

// MergeFeeds merges feedFiles using v1 semantics: per-file duplicate
// detection only (no renaming strategy, no global duplicate-handling flags).
func (m *Merger) MergeFeeds(feedFiles []string, strategies map[string]string, outputFile string) error {
	args := m.buildArgs(feedFiles, strategies, outputFile)
	return m.run(args, outputFile)
}

// buildArgs constructs the java argv for the v1 merge invocation. Files are
// iterated in sorted order so the resulting command line is deterministic
// across runs, regardless of Go's randomized map iteration order.
func (m *Merger) buildArgs(feedFiles []string, strategies map[string]string, outputFile string) []string {
	args := javaOptsArgs()
	args = append(args, "-jar", m.jarPath)

	for _, file := range sortedKeys(strategies) {
		args = append(args, fmt.Sprintf("--file=%s", file))
		args = append(args, fmt.Sprintf("--duplicateDetection=%s", strategies[file]))
	}

	args = append(args, feedFiles...)
	args = append(args, filepath.Join(m.tempDir, outputFile))

	return args
}

// MergeFeedsV2 merges feedFiles using v2 semantics: per-file duplicate
// detection and renaming settings, plus a global duplicate-handling policy
// (see docs/config-schema.md §1.4-1.5).
func (m *Merger) MergeFeedsV2(feedFiles []string, fileSettings map[string]FileSetting, duplicateHandling string, outputFile string) error {
	args := m.buildArgsV2(feedFiles, fileSettings, duplicateHandling, outputFile)
	return m.run(args, outputFile)
}

// buildArgsV2 constructs the java argv for the v2 merge invocation. Files
// are iterated in sorted order for determinism (same rationale as
// buildArgs).
func (m *Merger) buildArgsV2(feedFiles []string, fileSettings map[string]FileSetting, duplicateHandling string, outputFile string) []string {
	args := javaOptsArgs()
	args = append(args, "-jar", m.jarPath)

	for _, file := range sortedFileSettingKeys(fileSettings) {
		setting := fileSettings[file]
		args = append(args, fmt.Sprintf("--file=%s", file))
		args = append(args, fmt.Sprintf("--duplicateDetection=%s", setting.Detection))
		args = append(args, fmt.Sprintf("--duplicateRenaming=%s", setting.Renaming))
	}

	switch duplicateHandling {
	case "log":
		args = append(args, "--logDroppedDuplicates")
	case "fail":
		args = append(args, "--errorOnDroppedDuplicates")
	}

	args = append(args, feedFiles...)
	args = append(args, filepath.Join(m.tempDir, outputFile))

	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedFileSettingKeys(m map[string]FileSetting) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m *Merger) run(args []string, outputFile string) error {
	cmd := exec.Command("java", args...)
	m.capturedOutput.Reset()
	cmd.Stdout = io.MultiWriter(os.Stdout, &m.capturedOutput)
	cmd.Stderr = io.MultiWriter(os.Stderr, &m.capturedOutput)

	fmt.Printf("Running merge command: java %s\n", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	outputPath := filepath.Join(m.tempDir, outputFile)
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("merge output file not created: %s", outputPath)
	}

	return nil
}

func (m *Merger) GetOutputPath(outputFile string) string {
	return filepath.Join(m.tempDir, outputFile)
}
