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

func TestResolveRef_LightweightTagReturnsImmutable(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sha, immutable, err := ResolveRef(ctx, fixture.URL, "v1.0.0")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if sha != fixture.V100Commit {
		t.Errorf("sha = %q, want %q", sha, fixture.V100Commit)
	}
	if !immutable {
		t.Error("immutable = false, want true for tag ref")
	}
}

func TestResolveRef_AnnotatedTagPeeledImmutable(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sha, immutable, err := ResolveRef(ctx, fixture.URL, "v2.0.0")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if sha != fixture.V200Commit {
		t.Errorf("sha = %q, want peeled commit %q", sha, fixture.V200Commit)
	}
	if !immutable {
		t.Error("immutable = false, want true for annotated tag ref")
	}
}

func TestResolveRef_FortyHexSHAPassesThroughImmutable(t *testing.T) {
	const sha = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	got, immutable, err := ResolveRef(ctx, "file:///does/not/exist/should/not/be/touched", sha)
	if err != nil {
		t.Fatalf("ResolveRef returned error for SHA passthrough: %v", err)
	}
	if got != sha {
		t.Errorf("sha = %q, want %q", got, sha)
	}
	if !immutable {
		t.Error("immutable = false, want true for SHA passthrough")
	}
}

func TestResolveRef_BranchResolvesNotImmutable(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The fixture's default branch is `master`, sitting on V200Commit (the
	// most recent commit). A branch resolution must return the head SHA
	// and immutable=false so the orchestrator knows to record the SHA in
	// the catalog row's `version` field instead of the mutable branch name.
	sha, immutable, err := ResolveRef(ctx, fixture.URL, "master")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if sha != fixture.V200Commit {
		t.Errorf("sha = %q, want %q (HEAD of master)", sha, fixture.V200Commit)
	}
	if immutable {
		t.Error("immutable = true, want false for branch ref")
	}
}

func TestResolveRef_UnknownRefErrors(t *testing.T) {
	fixture := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := ResolveRef(ctx, fixture.URL, "no-such-ref-anywhere")
	if err == nil {
		t.Fatal("ResolveRef accepted unknown ref, want error")
	}
	if !strings.Contains(err.Error(), "no-such-ref-anywhere") {
		t.Errorf("error %q should mention the missing ref", err.Error())
	}
}

func TestResolveRef_EmptyRefRejects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, _, err := ResolveRef(ctx, "anthropics/skills", "")
	if err == nil {
		t.Fatal("ResolveRef accepted empty ref")
	}
}

func TestResolveRef_EmptyRepoRejects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, _, err := ResolveRef(ctx, "", "v1.0.0")
	if err == nil {
		t.Fatal("ResolveRef accepted empty repo")
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
