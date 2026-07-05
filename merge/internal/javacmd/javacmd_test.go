package javacmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestOptsArgs(t *testing.T) {
	t.Run("unset env returns nil", func(t *testing.T) {
		t.Setenv("JAVA_OPTS", "")
		if got := OptsArgs(); got != nil {
			t.Errorf("OptsArgs() = %#v, want nil", got)
		}
	})

	t.Run("whitespace-only env returns no args", func(t *testing.T) {
		t.Setenv("JAVA_OPTS", "   \t  \n ")
		if got := OptsArgs(); len(got) != 0 {
			t.Errorf("OptsArgs() = %#v, want empty", got)
		}
	})

	t.Run("splits on whitespace, including tabs and newlines", func(t *testing.T) {
		t.Setenv("JAVA_OPTS", "-Xmx2g \t-Xms512m\n-Dfoo=bar")
		want := []string{"-Xmx2g", "-Xms512m", "-Dfoo=bar"}
		got := OptsArgs()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("OptsArgs() = %#v, want %#v", got, want)
		}
	})
}

// installStubJava writes a fake `java` executable into a fresh directory
// and prepends it to PATH for the duration of the test, so Run's
// exec.Command("java", ...) resolves to the stub instead of a real JVM. The
// stub prints a known stdout/stderr line and exits with the code named by
// the STUB_JAVA_EXIT env var (0 if unset).
func installStubJava(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub java script assumes a POSIX shell")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf 'stdout from stub java\\n'\n" +
		"printf 'stderr from stub java\\n' >&2\n" +
		"exit \"${STUB_JAVA_EXIT:-0}\"\n"

	javaPath := filepath.Join(dir, "java")
	if err := os.WriteFile(javaPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write stub java: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRun(t *testing.T) {
	installStubJava(t)

	t.Run("streams both extra writers on success", func(t *testing.T) {
		t.Setenv("STUB_JAVA_EXIT", "0")

		var stdout, stderr bytes.Buffer
		if err := Run("test", []string{"-jar", "x.jar"}, &stdout, &stderr); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if !strings.Contains(stdout.String(), "stdout from stub java") {
			t.Errorf("extraStdout = %q, want it to contain the child's stdout", stdout.String())
		}
		if !strings.Contains(stderr.String(), "stderr from stub java") {
			t.Errorf("extraStderr = %q, want it to contain the child's stderr", stderr.String())
		}
	})

	t.Run("nonzero exit returns an error", func(t *testing.T) {
		t.Setenv("STUB_JAVA_EXIT", "3")

		if err := Run("test", []string{"-jar", "x.jar"}, nil, nil); err == nil {
			t.Fatal("expected an error for a nonzero exit code, got nil")
		}
	})
}

func TestVerifyOutputExists(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file errors", func(t *testing.T) {
		err := VerifyOutputExists("merge", filepath.Join(dir, "missing.zip"))
		if err == nil {
			t.Fatal("expected an error for a missing file, got nil")
		}
		if !strings.Contains(err.Error(), "not created") {
			t.Errorf("error = %q, want it to say the file was not created", err.Error())
		}
	})

	t.Run("present file passes", func(t *testing.T) {
		present := filepath.Join(dir, "present.zip")
		if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerifyOutputExists("merge", present); err != nil {
			t.Errorf("VerifyOutputExists() = %v, want nil", err)
		}
	})

	// A Stat failure that isn't "not exist" (e.g. permission denied while
	// traversing the parent directory) must also fail verification, with a
	// message distinct from the "not created" one, rather than silently
	// passing (see VerifyOutputExists's doc comment).
	t.Run("other stat failure surfaces a distinct error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root; permission bits don't block directory traversal")
		}

		blockedDir := filepath.Join(dir, "blocked")
		if err := os.Mkdir(blockedDir, 0); err != nil {
			t.Fatalf("failed to create permission-blocked dir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o755) })

		target := filepath.Join(blockedDir, "output.zip")
		err := VerifyOutputExists("merge", target)
		if err == nil {
			t.Skip("Stat succeeded despite permission bits (platform-dependent); skipping")
		}
		if strings.Contains(err.Error(), "not created") {
			t.Errorf("error = %q, want a message distinct from the not-created case", err.Error())
		}
	})
}
