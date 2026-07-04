package pairmerge

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name         string
		jarPath      string
		currentPath  string
		upcomingPath string
		outputPath   string
		javaOpts     string
		want         []string
	}{
		{
			name:         "no JAVA_OPTS, current first then upcoming",
			jarPath:      "merge-cli.jar",
			currentPath:  "/tmp/work/feed_everett.zip",
			upcomingPath: "/tmp/work/feed_everett_upcoming.zip",
			outputPath:   "/tmp/work/paired_everett.zip",
			want: []string{
				"-jar", "merge-cli.jar",
				"/tmp/work/feed_everett.zip",
				"/tmp/work/feed_everett_upcoming.zip",
				"/tmp/work/paired_everett.zip",
			},
		},
		{
			name:         "JAVA_OPTS is prepended before -jar",
			jarPath:      "merge-cli.jar",
			currentPath:  "/tmp/work/feed_everett.zip",
			upcomingPath: "/tmp/work/feed_everett_upcoming.zip",
			outputPath:   "/tmp/work/paired_everett.zip",
			javaOpts:     "-Xmx2g -Xms512m",
			want: []string{
				"-Xmx2g", "-Xms512m",
				"-jar", "merge-cli.jar",
				"/tmp/work/feed_everett.zip",
				"/tmp/work/feed_everett_upcoming.zip",
				"/tmp/work/paired_everett.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JAVA_OPTS", tt.javaOpts)

			p := New(tt.jarPath, "/tmp/work")
			got := p.buildArgs(tt.currentPath, tt.upcomingPath, tt.outputPath)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs() =\n  %v\nwant\n  %v", got, tt.want)
			}

			// No --file/--duplicateDetection/--duplicateRenaming flags: the
			// pair-merge always uses the JAR's default settings.
			for _, arg := range got {
				if len(arg) >= 2 && arg[:2] == "--" {
					t.Errorf("buildArgs() emitted a flag %q, want defaults-only invocation", arg)
				}
			}
		})
	}
}
