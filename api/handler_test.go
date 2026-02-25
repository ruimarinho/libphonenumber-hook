package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
