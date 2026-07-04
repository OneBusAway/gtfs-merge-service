package merge

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Merger struct {
	jarPath string
	tempDir string
	capture duplicateLineCapture
}

func New(jarPath, tempDir string) *Merger {
	return &Merger{
		jarPath: jarPath,
		tempDir: tempDir,
	}
}

// CapturedDuplicateLines returns the merge JAR's "duplicate entity:" lines
// retained from the most recent MergeFeedsV2 invocation (up to
// DroppedDuplicatesLimit of them), and whether more than that many such
// lines were actually logged. It's used by report.json generation (see
// internal/report) to parse dropped-duplicate log lines without holding
// the JAR's entire, potentially unbounded, console output in memory; the
// full output is also still streamed live to this process's own
// stdout/stderr as it always has been (see run). MergeFeeds (the v1 path)
// never populates this — it always returns "", false.
func (m *Merger) CapturedDuplicateLines() (lines string, truncated bool) {
	return m.capture.Lines()
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
// It never captures the merge JAR's output (see run) — v1 has no
// report.json/dropped-duplicate parsing to feed.
func (m *Merger) MergeFeeds(feedFiles []string, strategies map[string]string, outputFile string) error {
	args := m.buildArgs(feedFiles, strategies, outputFile)
	return m.run(args, outputFile, false)
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
	return m.run(args, outputFile, true)
}

// buildArgsV2 constructs the java argv for the v2 merge invocation. Files
// are iterated in sorted order for determinism (same rationale as
// buildArgs).
//
// --duplicateRenaming emission is intentionally all-or-nothing across
// fileSettings, not per file. GtfsMergerMain.buildMerger pairs --file,
// --duplicateDetection, and --duplicateRenaming purely by list *index*
// (`for (int i = 0; i < fileOptions.size(); i++) { ...
// duplicateRenamingOptions.get(i) ... }`), not by filename. So omitting
// --duplicateRenaming for only some files would shift every later file's
// renaming strategy onto the wrong file. Since renaming is optional
// (docs/config-schema.md §1.5) and "" means "use the JAR's default"
// (equivalent to "context"), we can safely omit the flag for every file
// when none of them need "agency" renaming — but the moment any file does,
// every file needs an explicit --duplicateRenaming to keep the index
// pairing intact, using "context" for the ones left empty/unset.
func (m *Merger) buildArgsV2(feedFiles []string, fileSettings map[string]FileSetting, duplicateHandling string, outputFile string) []string {
	args := javaOptsArgs()
	args = append(args, "-jar", m.jarPath)

	emitRenaming := anyFileWantsAgencyRenaming(fileSettings)

	for _, file := range sortedFileSettingKeys(fileSettings) {
		setting := fileSettings[file]
		args = append(args, fmt.Sprintf("--file=%s", file))
		args = append(args, fmt.Sprintf("--duplicateDetection=%s", setting.Detection))
		if emitRenaming {
			renaming := setting.Renaming
			if renaming == "" {
				renaming = "context"
			}
			args = append(args, fmt.Sprintf("--duplicateRenaming=%s", renaming))
		}
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

// anyFileWantsAgencyRenaming reports whether any file setting requests
// "agency" duplicate renaming, in which case --duplicateRenaming must be
// emitted for every file (see buildArgsV2's doc comment).
func anyFileWantsAgencyRenaming(fileSettings map[string]FileSetting) bool {
	for _, setting := range fileSettings {
		if setting.Renaming == "agency" {
			return true
		}
	}
	return false
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

// run executes the java argv, always streaming stdout/stderr live to this
// process's own stdout/stderr. When capture is true (the v2 path — see
// MergeFeedsV2), it also tees that output through m.capture, a
// duplicateLineCapture that retains only "duplicate entity:" lines, bounded
// to DroppedDuplicatesLimit, so memory use stays flat regardless of how
// much the JAR logs overall. capture is false on the v1 path (MergeFeeds),
// which has no use for the captured lines at all.
func (m *Merger) run(args []string, outputFile string, capture bool) error {
	cmd := exec.Command("java", args...)
	if capture {
		m.capture.reset()
		cmd.Stdout = io.MultiWriter(os.Stdout, &m.capture)
		cmd.Stderr = io.MultiWriter(os.Stderr, &m.capture)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

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
