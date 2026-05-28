package scm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The file:// fetch_test.go exercises the happy path through go-git's
// transport layer. These tests cover error paths specific to the HTTPS
// transport — 404 from upstream and context cancellation against a slow
// server. They prove Fetch wraps transport-level errors with useful
// context and honors ctx deadlines.

func TestFetch_HTTPS_404_FromUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	pointFetchAt(t, srv.URL+"/anthropics/skills.git")

	ref := SourceRef{
		Owner:   "anthropics",
		Repo:    "skills",
		Subpath: "skills/example",
		Commit:  "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
	}
	dst := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch accepted 404 upstream, want error")
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Errorf("error %q lacks 'fetch' wrapper context", err.Error())
	}
}

func TestFetch_HTTPS_ContextTimeout(t *testing.T) {
	// Server that holds requests longer than the context deadline. Use a
	// short ctx timeout so the test completes quickly when the cancellation
	// path works correctly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	}))
	t.Cleanup(srv.Close)

	pointFetchAt(t, srv.URL+"/anthropics/skills.git")

	ref := SourceRef{
		Owner:   "anthropics",
		Repo:    "skills",
		Subpath: "skills/example",
		Commit:  "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
	}
	dst := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Fetch(ctx, ref, dst)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Fetch ran to completion against slow server, want timeout")
	}
	// The timeout should fire promptly — well under the 10s the server
	// would otherwise hold.
	if elapsed > 5*time.Second {
		t.Errorf("Fetch took %v to honor ctx timeout; expected < 5s", elapsed)
	}
}
