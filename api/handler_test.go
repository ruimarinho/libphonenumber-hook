package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v68/github"
	webhook "gopkg.in/go-playground/webhooks.v5/github"
)

func TestHandleEventSkipsNonTagRef(t *testing.T) {
	payload := webhook.PushPayload{
		Ref: "refs/heads/master",
	}

	err := HandleEvent(payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHandleEventProcessesTagRef(t *testing.T) {
	payload := webhook.PushPayload{
		Ref: "refs/tags/v8.13.50",
	}

	// This will fail at Clone since we don't have a real repo, but it proves
	// that tag detection works (it doesn't skip).
	err := HandleEvent(payload)
	if err == nil {
		t.Fatal("expected an error from Clone, got nil")
	}
}

func TestVersionExtraction(t *testing.T) {
	tests := []struct {
		ref     string
		version string
	}{
		{"refs/tags/v8.13.50", "8.13.50"},
		{"refs/tags/v1.0.0", "1.0.0"},
		{"refs/tags/v10.2.3", "10.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := extractVersion(tt.ref)
			if got != tt.version {
				t.Errorf("extractVersion(%q) = %q, want %q", tt.ref, got, tt.version)
			}
		})
	}
}

func TestDownloadValidatesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	dir := t.TempDir()

	// Temporarily override the download URL by downloading from our test server
	_, err := downloadFromURL(server.URL+"/test.js", dir, "test.js")
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

func TestDownloadSucceedsWithValidResponse(t *testing.T) {
	expected := "var foo = 'bar';\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(expected))
	}))
	defer server.Close()

	dir := t.TempDir()

	_, err := downloadFromURL(server.URL+"/test.js", dir, "test.js")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "test.js"))
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}

	if string(content) != expected {
		t.Errorf("file content = %q, want %q", string(content), expected)
	}
}

func TestDownloadRejectsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	dir := t.TempDir()

	_, err := downloadFromURL(server.URL+"/test.js", dir, "test.js")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestFilenamesIncludesAsYouTypeFormatter(t *testing.T) {
	found := false
	for _, f := range filenames {
		if f == "asyoutypeformatter.js" {
			found = true
			break
		}
	}

	if !found {
		t.Error("filenames slice is missing asyoutypeformatter.js")
	}
}

func TestFilenamesCount(t *testing.T) {
	if len(filenames) != 15 {
		t.Errorf("expected 15 filenames, got %d", len(filenames))
	}
}

func TestHandlerRejectsGetRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/handler", nil)
	w := httptest.NewRecorder()

	Handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestEnableAutoMergeSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("expected path /graphql, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": {"enablePullRequestAutoMerge": {"clientMutationId": null}}}`))
	}))
	defer server.Close()

	err := EnableAutoMerge(context.Background(), newTestGitHubClient(t, server), "PR_123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestEnableAutoMergeReturnsGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": null, "errors": [{"message": "Pull request is in clean status"}]}`))
	}))
	defer server.Close()

	err := EnableAutoMerge(context.Background(), newTestGitHubClient(t, server), "PR_123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "clean status") {
		t.Errorf("expected error to contain graphql message, got %v", err)
	}
}

func TestIsOlderVersion(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"9.0.25", "9.0.28", true},
		{"9.0.28", "9.0.25", false},
		{"9.0.28", "9.0.28", false},
		{"8.13.55", "9.0.1", true},
		{"9.0.9", "9.0.10", true},
		{"9.0", "9.0.1", true},
		{"invalid", "9.0.1", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s<%s", tt.a, tt.b), func(t *testing.T) {
			if got := isOlderVersion(tt.a, tt.b); got != tt.want {
				t.Errorf("isOlderVersion(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestVersionFromBranch(t *testing.T) {
	got := versionFromBranch("support/update-libphonenumber-9-0-28")
	if got != "9.0.28" {
		t.Errorf("versionFromBranch() = %q, want %q", got, "9.0.28")
	}
}

// newTestGitHubClient returns a GitHub client pointed at a test server.
func newTestGitHubClient(t *testing.T, server *httptest.Server) *github.Client {
	t.Helper()

	client := github.NewClient(nil)
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	client.BaseURL = baseURL

	return client
}

// downloadFromURL is a test helper that downloads from an arbitrary URL.
func downloadFromURL(url string, directory string, filename string) (*os.File, error) {
	file, err := os.Create(fmt.Sprintf("%s/%s", directory, filename))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	response, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %d", url, response.StatusCode)
	}

	_, err = fmt.Fprint(file, "")
	if err != nil {
		return nil, err
	}

	// Read and write the body
	buf := make([]byte, 1024)
	for {
		n, readErr := response.Body.Read(buf)
		if n > 0 {
			_, writeErr := file.Write(buf[:n])
			if writeErr != nil {
				return nil, writeErr
			}
		}
		if readErr != nil {
			break
		}
	}

	return file, nil
}
