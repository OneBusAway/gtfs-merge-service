package transform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteRulesFile(t *testing.T) {
	tests := []struct {
		name  string
		rules []json.RawMessage
		want  string
	}{
		{
			name: "single rule",
			rules: []json.RawMessage{
				json.RawMessage(`{"op":"remove_unused_routes"}`),
			},
			want: "{\"op\":\"remove_unused_routes\"}\n",
		},
		{
			name: "multiple rules, one JSON object per line in order",
			rules: []json.RawMessage{
				json.RawMessage(`{"op":"remove_unused_routes"}`),
				json.RawMessage(`{"op":"update","match":{"file":"agency.txt","agency_id":"1"},"update":{"agency_id":"97"}}`),
			},
			want: "{\"op\":\"remove_unused_routes\"}\n" +
				"{\"op\":\"update\",\"match\":{\"file\":\"agency.txt\",\"agency_id\":\"1\"},\"update\":{\"agency_id\":\"97\"}}\n",
		},
		{
			name:  "no rules writes an empty file",
			rules: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "rules.txt")

			if err := writeRulesFile(path, tt.rules); err != nil {
				t.Fatalf("writeRulesFile() error: %v", err)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read rules file: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("rules file content =\n%q\nwant\n%q", string(got), tt.want)
			}
		})
	}
}

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name       string
		jarPath    string
		rulesPath  string
		inputPath  string
		outputPath string
		javaOpts   string
		want       []string
	}{
		{
			name:       "no JAVA_OPTS",
			jarPath:    "transformer-cli.jar",
			rulesPath:  "/tmp/work/rules_everett.txt",
			inputPath:  "/tmp/work/everett.zip",
			outputPath: "/tmp/work/transformed_everett.zip",
			want: []string{
				"-jar", "transformer-cli.jar",
				"--transform=/tmp/work/rules_everett.txt",
				"/tmp/work/everett.zip",
				"/tmp/work/transformed_everett.zip",
			},
		},
		{
			name:       "JAVA_OPTS is prepended before -jar",
			jarPath:    "transformer-cli.jar",
			rulesPath:  "/tmp/work/rules_everett.txt",
			inputPath:  "/tmp/work/everett.zip",
			outputPath: "/tmp/work/transformed_everett.zip",
			javaOpts:   "-Xmx2g -Xms512m",
			want: []string{
				"-Xmx2g", "-Xms512m",
				"-jar", "transformer-cli.jar",
				"--transform=/tmp/work/rules_everett.txt",
				"/tmp/work/everett.zip",
				"/tmp/work/transformed_everett.zip",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JAVA_OPTS", tt.javaOpts)

			tr := New(tt.jarPath, "/tmp/work")
			got := tr.buildArgs(tt.rulesPath, tt.inputPath, tt.outputPath)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestTransformSkipsWhenNoRules(t *testing.T) {
	dir := t.TempDir()
	tr := New("transformer-cli.jar", dir)

	inputPath := filepath.Join(dir, "everett.zip")
	got, err := tr.Transform("everett", inputPath, nil)
	if err != nil {
		t.Fatalf("Transform() error: %v", err)
	}
	if got != inputPath {
		t.Errorf("Transform() = %q, want input path unchanged %q", got, inputPath)
	}

	// No rules file or transformed output should have been created; this is
	// a no-op that never shells out to java.
	if _, err := os.Stat(filepath.Join(dir, "rules_everett.txt")); !os.IsNotExist(err) {
		t.Errorf("expected no rules file to be written, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "transformed_everett.zip")); !os.IsNotExist(err) {
		t.Errorf("expected no output file to be written, stat err = %v", err)
	}
}
