package merge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Merger struct {
	jarPath string
	tempDir string
}

func New(jarPath, tempDir string) *Merger {
	return &Merger{
		jarPath: jarPath,
		tempDir: tempDir,
	}
}

func (m *Merger) MergeFeeds(feedFiles []string, strategies map[string]string, outputFile string) error {
	args := []string{"-jar", m.jarPath}

	for file, strategy := range strategies {
		args = append(args, fmt.Sprintf("--file=%s", file))
		args = append(args, fmt.Sprintf("--duplicateDetection=%s", strategy))
	}

	args = append(args, feedFiles...)

	outputPath := filepath.Join(m.tempDir, outputFile)
	args = append(args, outputPath)

	cmd := exec.Command("java", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Running merge command: java %s\n", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("merge output file not created: %s", outputPath)
	}

	return nil
}

func (m *Merger) GetOutputPath(outputFile string) string {
	return filepath.Join(m.tempDir, outputFile)
}
