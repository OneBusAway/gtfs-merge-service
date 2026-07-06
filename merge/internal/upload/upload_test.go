package upload

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUploadBytes exercises the shared upload() core through its public
// UploadBytes entry point against a real HTTP server: New's endpoint param
// is fully caller-configurable (see New's custom EndpointResolverWithOptions
// and HostnameImmutable: true, which keep requests path-style rather than
// virtual-hosted-style), so an httptest.Server can stand in for S3/R2
// without a network call. Asserts the request method, Content-Type, and
// body bytes the shared core sends.
func TestUploadBytes(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		gotBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := New("test-access-key", "test-secret-key", srv.URL, "test-bucket")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	data := []byte(`{"hello":"world"}`)
	if err := u.UploadBytes(data, "reports/report.json", "application/json"); err != nil {
		t.Fatalf("UploadBytes() error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodPut)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
	if string(gotBody) != string(data) {
		t.Errorf("body = %q, want %q", gotBody, data)
	}
	if !strings.HasSuffix(gotPath, "/test-bucket/reports/report.json") {
		t.Errorf("path = %q, want it to end with %q (path-style bucket/key addressing)", gotPath, "/test-bucket/reports/report.json")
	}
}

// ObjectURL must produce the same path-style URL shape the uploader PUTs to
// (endpoint/bucket/key), with no double slashes when the endpoint has a
// trailing slash. These are the per-build URLs written into
// bundle-inputs.json for dry-run inspection; Rails rewrites them to stable
// URLs at publish.
func TestObjectURL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		key      string
		want     string
	}{
		{
			name:     "plain endpoint",
			endpoint: "https://accountid.r2.cloudflarestorage.com",
			key:      "builds/49/feeds/metro.zip",
			want:     "https://accountid.r2.cloudflarestorage.com/test-bucket/builds/49/feeds/metro.zip",
		},
		{
			name:     "endpoint with trailing slash",
			endpoint: "https://accountid.r2.cloudflarestorage.com/",
			key:      "builds/49/bundle-inputs.json",
			want:     "https://accountid.r2.cloudflarestorage.com/test-bucket/builds/49/bundle-inputs.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := New("k", "s", tt.endpoint, "test-bucket")
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			if got := u.ObjectURL(tt.key); got != tt.want {
				t.Errorf("ObjectURL(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
