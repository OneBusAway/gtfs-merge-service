package merge

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name       string
		feedFiles  []string
		strategies map[string]string
		outputFile string
		javaOpts   string
		want       []string
	}{
		{
			name:       "no strategies",
			feedFiles:  []string{"feed_0.zip", "feed_1.zip"},
			strategies: nil,
			outputFile: "merged.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"feed_0.zip", "feed_1.zip",
				"/tmp/work/merged.zip",
			},
		},
		{
			name:      "strategies are emitted in sorted file order",
			feedFiles: []string{"feed_0.zip", "feed_1.zip"},
			strategies: map[string]string{
				"trips.txt":  "identity",
				"agency.txt": "identity",
				"stops.txt":  "fuzzy",
			},
			outputFile: "merged.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"--file=agency.txt", "--duplicateDetection=identity",
				"--file=stops.txt", "--duplicateDetection=fuzzy",
				"--file=trips.txt", "--duplicateDetection=identity",
				"feed_0.zip", "feed_1.zip",
				"/tmp/work/merged.zip",
			},
		},
		{
			name:       "JAVA_OPTS is prepended before -jar",
			feedFiles:  []string{"feed_0.zip"},
			strategies: nil,
			outputFile: "merged.zip",
			javaOpts:   "-Xmx2g -Xms512m",
			want: []string{
				"-Xmx2g", "-Xms512m",
				"-jar", "merge-cli.jar",
				"feed_0.zip",
				"/tmp/work/merged.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.javaOpts != "" {
				t.Setenv("JAVA_OPTS", tt.javaOpts)
			} else {
				t.Setenv("JAVA_OPTS", "")
			}

			m := New("merge-cli.jar", "/tmp/work")
			got := m.buildArgs(tt.feedFiles, tt.strategies, tt.outputFile)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

// TestBuildArgsDeterministic verifies that repeated calls with the same
// (large, unordered) strategies map always produce byte-identical argv,
// guarding against Go's randomized map iteration order.
func TestBuildArgsDeterministic(t *testing.T) {
	t.Setenv("JAVA_OPTS", "")

	strategies := map[string]string{
		"trips.txt":      "identity",
		"agency.txt":     "identity",
		"stops.txt":      "fuzzy",
		"routes.txt":     "fuzzy",
		"calendar.txt":   "identity",
		"shapes.txt":     "fuzzy",
		"transfers.txt":  "none",
		"stop_times.txt": "identity",
	}

	m := New("merge-cli.jar", "/tmp/work")
	first := m.buildArgs([]string{"a.zip", "b.zip"}, strategies, "out.zip")

	for i := 0; i < 20; i++ {
		got := m.buildArgs([]string{"a.zip", "b.zip"}, strategies, "out.zip")
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d: buildArgs() is nondeterministic:\n  %v\nvs\n  %v", i, got, first)
		}
	}
}

func TestBuildArgsV2(t *testing.T) {
	tests := []struct {
		name              string
		feedFiles         []string
		fileSettings      map[string]FileSetting
		duplicateHandling string
		outputFile        string
		javaOpts          string
		want              []string
	}{
		{
			name:              "no file settings, ignore handling emits no global flag",
			feedFiles:         []string{"feed_a.zip", "feed_b.zip"},
			fileSettings:      nil,
			duplicateHandling: "ignore",
			outputFile:        "gtfs.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"feed_a.zip", "feed_b.zip",
				"/tmp/work/gtfs.zip",
			},
		},
		{
			name:      "file settings emitted as sorted triples",
			feedFiles: []string{"feed_a.zip", "feed_b.zip"},
			fileSettings: map[string]FileSetting{
				"trips.txt": {Detection: "identity", Renaming: "agency"},
				"stops.txt": {Detection: "fuzzy", Renaming: "agency"},
			},
			duplicateHandling: "ignore",
			outputFile:        "gtfs.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"--file=stops.txt", "--duplicateDetection=fuzzy", "--duplicateRenaming=agency",
				"--file=trips.txt", "--duplicateDetection=identity", "--duplicateRenaming=agency",
				"feed_a.zip", "feed_b.zip",
				"/tmp/work/gtfs.zip",
			},
		},
		{
			name:              "log handling emits --logDroppedDuplicates",
			feedFiles:         []string{"feed_a.zip"},
			duplicateHandling: "log",
			outputFile:        "gtfs.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"--logDroppedDuplicates",
				"feed_a.zip",
				"/tmp/work/gtfs.zip",
			},
		},
		{
			name:              "fail handling emits --errorOnDroppedDuplicates",
			feedFiles:         []string{"feed_a.zip"},
			duplicateHandling: "fail",
			outputFile:        "gtfs.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"--errorOnDroppedDuplicates",
				"feed_a.zip",
				"/tmp/work/gtfs.zip",
			},
		},
		{
			name:      "file settings and global flag combine, JAVA_OPTS prepended",
			feedFiles: []string{"feed_a.zip", "feed_b.zip"},
			fileSettings: map[string]FileSetting{
				"agency.txt": {Detection: "identity", Renaming: "context"},
			},
			duplicateHandling: "fail",
			outputFile:        "gtfs.zip",
			javaOpts:          "-Xmx4g",
			want: []string{
				"-Xmx4g",
				"-jar", "merge-cli.jar",
				"--file=agency.txt", "--duplicateDetection=identity", "--duplicateRenaming=context",
				"--errorOnDroppedDuplicates",
				"feed_a.zip", "feed_b.zip",
				"/tmp/work/gtfs.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.javaOpts != "" {
				t.Setenv("JAVA_OPTS", tt.javaOpts)
			} else {
				t.Setenv("JAVA_OPTS", "")
			}

			m := New("merge-cli.jar", "/tmp/work")
			got := m.buildArgsV2(tt.feedFiles, tt.fileSettings, tt.duplicateHandling, tt.outputFile)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgsV2() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestGetOutputPath(t *testing.T) {
	m := New("merge-cli.jar", "/tmp/work")
	got := m.GetOutputPath("gtfs.zip")
	want := "/tmp/work/gtfs.zip"
	if got != want {
		t.Errorf("GetOutputPath() = %q, want %q", got, want)
	}
}
