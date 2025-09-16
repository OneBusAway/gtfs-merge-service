package validate

import (
	"archive/zip"
	"fmt"
	"path/filepath"
	"strings"
)

var requiredFiles = []string{
	"agency.txt",
	"stops.txt",
	"routes.txt",
	"trips.txt",
	"stop_times.txt",
}

var optionalFiles = []string{
	"calendar.txt",
	"calendar_dates.txt",
	"fare_attributes.txt",
	"fare_rules.txt",
	"shapes.txt",
	"frequencies.txt",
	"transfers.txt",
	"pathways.txt",
	"levels.txt",
	"feed_info.txt",
	"translations.txt",
	"attributions.txt",
}

type Validator struct{}

func New() *Validator {
	return &Validator{}
}

func (v *Validator) ValidateFeed(feedPath string) error {
	reader, err := zip.OpenReader(feedPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()

	filesInZip := make(map[string]bool)
	for _, file := range reader.File {
		fileName := strings.ToLower(filepath.Base(file.Name))
		filesInZip[fileName] = true
	}

	missingRequired := []string{}
	for _, reqFile := range requiredFiles {
		if !filesInZip[reqFile] {
			missingRequired = append(missingRequired, reqFile)
		}
	}

	if len(missingRequired) > 0 {
		return fmt.Errorf("missing required GTFS files: %v", missingRequired)
	}

	foundFiles := []string{}
	for _, reqFile := range requiredFiles {
		if filesInZip[reqFile] {
			foundFiles = append(foundFiles, reqFile)
		}
	}
	for _, optFile := range optionalFiles {
		if filesInZip[optFile] {
			foundFiles = append(foundFiles, optFile)
		}
	}

	fmt.Printf("GTFS feed validation passed. Found %d files: %v\n",
		len(foundFiles), foundFiles)

	if len(reader.File) == 0 {
		return fmt.Errorf("zip file is empty")
	}

	totalSize := int64(0)
	for _, file := range reader.File {
		totalSize += int64(file.CompressedSize64)
	}

	fmt.Printf("Feed size: %.2f MB (%d files total)\n",
		float64(totalSize)/(1024*1024), len(reader.File))

	return nil
}
