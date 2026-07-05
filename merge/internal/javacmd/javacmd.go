// Package javacmd holds the JAVA_OPTS handling and java invocation sequence
// shared by every package in this service that shells out to a JAR
// (internal/merge, internal/transform, internal/pairmerge): split
// JAVA_OPTS's leading JVM flags onto the argv, run `java`, stream its
// output, and verify the expected output file was actually created.
package javacmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// OptsArgs returns the leading JVM flags from the JAVA_OPTS environment
// variable, split on whitespace, or nil if it is unset. Every java
// invocation in this service honors JAVA_OPTS the same way: leading flags,
// ahead of -jar.
func OptsArgs() []string {
	javaOpts := os.Getenv("JAVA_OPTS")
	if javaOpts == "" {
		return nil
	}
	return strings.Fields(javaOpts)
}

// Run executes `java` with args, logging the invocation to stdout (prefixed
// by label, e.g. "merge", "transform", "pair-merge") before running.
// stdout/stderr are always streamed live to this process's own
// stdout/stderr; when extraStdout or extraStderr is non-nil, that stream is
// additionally tee'd through it (used by merge.Merger to capture "duplicate
// entity:" lines without holding the JAR's entire console output in
// memory). It returns cmd.Run()'s raw, unwrapped error for the caller to
// wrap with its own message.
func Run(label string, args []string, extraStdout, extraStderr io.Writer) error {
	cmd := exec.Command("java", args...)

	cmd.Stdout = os.Stdout
	if extraStdout != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, extraStdout)
	}
	cmd.Stderr = os.Stderr
	if extraStderr != nil {
		cmd.Stderr = io.MultiWriter(os.Stderr, extraStderr)
	}

	fmt.Printf("Running %s command: java %s\n", label, strings.Join(args, " "))

	return cmd.Run()
}

// VerifyOutputExists checks that outputPath exists (was written by the java
// invocation Run just ran), returning an error of the form "<label> output
// file not created: <outputPath>" if not, or nil if it does.
func VerifyOutputExists(label, outputPath string) error {
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("%s output file not created: %s", label, outputPath)
	}
	return nil
}
