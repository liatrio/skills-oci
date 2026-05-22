package scm

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestResolveTag_LightweightTag(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := ResolveTag(ctx, fixture.URL, "v1.0.0")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	if got != fixture.V100Commit {
		t.Errorf("ResolveTag returned %q, want commit %q", got, fixture.V100Commit)
	}
}

func TestResolveTag_AnnotatedTagPeeled(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := ResolveTag(ctx, fixture.URL, "v2.0.0")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	// Annotated tag must resolve to the *commit* it points at, not the
	// tag object itself. This is the load-bearing peel logic.
	if got != fixture.V200Commit {
		t.Errorf("ResolveTag returned %q, want peeled commit %q (got tag-object hash? %v)", got, fixture.V200Commit, got == fixture.V200TagObject)
	}
}

func TestResolveTag_FortyHexSHAPassesThrough(t *testing.T) {
	// A 40-hex SHA must return unchanged with zero network calls. The
	// repo URL we pass is deliberately unreachable to prove no fetch
	// happens.
	const sha = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	got, err := ResolveTag(ctx, "file:///does/not/exist/should/not/be/touched", sha)
	if err != nil {
		t.Fatalf("ResolveTag returned error for SHA passthrough: %v", err)
	}
	if got != sha {
		t.Errorf("ResolveTag returned %q, want %q", got, sha)
	}
}

func TestResolveTag_TagNotFound(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ResolveTag(ctx, fixture.URL, "v9.9.9")
	if err == nil {
		t.Fatal("ResolveTag accepted nonexistent tag, want error")
	}
	if !strings.Contains(err.Error(), "v9.9.9") {
		t.Errorf("error %q should mention the missing tag", err.Error())
	}
}

func TestResolveTag_EmptyTagRejects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := ResolveTag(ctx, "anthropics/skills", "")
	if err == nil {
		t.Fatal("ResolveTag accepted empty tag")
	}
	if !strings.Contains(err.Error(), "tag") {
		t.Errorf("error %q lacks 'tag' context", err.Error())
	}
}

func TestResolveTag_EmptyRepoRejects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := ResolveTag(ctx, "", "v1.0.0")
	if err == nil {
		t.Fatal("ResolveTag accepted empty repo")
	}
	if !strings.Contains(err.Error(), "repo") {
		t.Errorf("error %q lacks 'repo' context", err.Error())
	}
}

func TestResolveTag_RepoSlugBuildsGitHubURL(t *testing.T) {
	// When repo is a bare slug (no scheme), ResolveTag should target
	// github.com. Without network access in tests we can only assert the
	// failure mode: the call returns an error mentioning the slug, not
	// the empty-tag or empty-repo guard.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := ResolveTag(ctx, "this-org-does-not-exist/and-neither-does-this", "v9.9.9")
	if err == nil {
		t.Fatal("ResolveTag accepted unreachable slug")
	}
	if !strings.Contains(err.Error(), "this-org-does-not-exist") {
		t.Errorf("error %q should mention the failing repo", err.Error())
	}
}
