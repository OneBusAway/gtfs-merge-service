package download

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Downloader struct {
	tempDir    string
	httpClient *http.Client
}

func New(tempDir string) *Downloader {
	return &Downloader{
		tempDir: tempDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}
}

func (d *Downloader) DownloadFeeds(feedURLs []string) ([]string, error) {
	if err := os.MkdirAll(d.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var downloadedFiles []string
	errors := make(chan error, len(feedURLs))

	for i, url := range feedURLs {
		wg.Add(1)
		go func(index int, feedURL string) {
			defer wg.Done()

			fileName := fmt.Sprintf("feed_%d.zip", index)
			filePath := filepath.Join(d.tempDir, fileName)

			if err := d.downloadFile(feedURL, filePath); err != nil {
				errors <- fmt.Errorf("failed to download %s: %w", feedURL, err)
				return
			}

			mu.Lock()
			downloadedFiles = append(downloadedFiles, filePath)
			mu.Unlock()

			fmt.Printf("Downloaded feed %d/%d: %s\n", index+1, len(feedURLs), feedURL)
		}(i, url)
	}

	wg.Wait()
	close(errors)

	// Collect all errors
	var allErrors []error
	for err := range errors {
		if err != nil {
			allErrors = append(allErrors, err)
		}
	}

	if len(allErrors) > 0 {
		// Combine all errors into a single error message
		var errorMessages []string
		for _, err := range allErrors {
			errorMessages = append(errorMessages, err.Error())
		}
		return nil, fmt.Errorf("download failed with %d errors:\n%s",
			len(allErrors), strings.Join(errorMessages, "\n"))
	}

	if len(downloadedFiles) != len(feedURLs) {
		return nil, fmt.Errorf("failed to download all feeds: got %d, expected %d",
			len(downloadedFiles), len(feedURLs))
	}

	return downloadedFiles, nil
}

func (d *Downloader) downloadFile(url, filepath string) error {
	resp, err := d.httpClient.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (d *Downloader) Cleanup() error {
	if d.tempDir != "" {
		return os.RemoveAll(d.tempDir)
	}
	return nil
}
