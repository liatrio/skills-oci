package scm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// pointResolveAt swaps the package-level resolver URL builder so the
// ls-remote targets a fixture repo instead of github.com for the duration
// of the test. Production always pins github.com; only tests reach
// file:// fixtures, and only via this seam. The original builder is
// restored on cleanup.
func pointResolveAt(t *testing.T, url string) {
	t.Helper()
	orig := remoteURLForResolve
	remoteURLForResolve = func(repo string) string { return url }
	t.Cleanup(func() { remoteURLForResolve = orig })
}

func TestResolveTag_LightweightTag(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := ResolveTag(ctx, "fixture/fixture", "v1.0.0")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	if got != fixture.V100Commit {
		t.Errorf("ResolveTag returned %q, want commit %q", got, fixture.V100Commit)
	}
}

func TestResolveTag_AnnotatedTagPeeled(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := ResolveTag(ctx, "fixture/fixture", "v2.0.0")
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
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ResolveTag(ctx, "fixture/fixture", "v9.9.9")
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
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sha, immutable, err := ResolveRef(ctx, "fixture/fixture", "v1.0.0")
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
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sha, immutable, err := ResolveRef(ctx, "fixture/fixture", "v2.0.0")
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
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The fixture's default branch is `master`, sitting on V200Commit (the
	// most recent commit). A branch resolution must return the head SHA
	// and immutable=false so the orchestrator knows to record the SHA in
	// the catalog row's `version` field instead of the mutable branch name.
	sha, immutable, err := ResolveRef(ctx, "fixture/fixture", "master")
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

func TestResolveRef_TagWinsOverSameNamedBranch(t *testing.T) {
	// When refs/tags/shared and refs/heads/shared point at different
	// commits, ResolveRef must return the tag's commit with immutable=true,
	// matching Git's own refs/tags/* over refs/heads/* preference.
	fixture := newFixtureRepoAmbiguousRef(t)
	pointResolveAt(t, fixture.url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sha, immutable, err := ResolveRef(ctx, "fixture/fixture", "shared")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if sha != fixture.tagCommit {
		t.Errorf("sha = %q, want tag commit %q (got branch commit? %v)", sha, fixture.tagCommit, sha == fixture.headCommit)
	}
	if !immutable {
		t.Error("immutable = false, want true (tag should win over branch)")
	}
}

func TestResolveRef_UnknownRefErrors(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointResolveAt(t, fixture.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := ResolveRef(ctx, "fixture/fixture", "no-such-ref-anywhere")
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
	// The slug -> github.com rewrite is the production seam. Inspect the
	// builder directly with zero network I/O (a real outbound call would
	// violate the "no live network in unit tests" rule).
	got := remoteURLForResolve("anthropics/skills")
	const want = "https://github.com/anthropics/skills.git"
	if got != want {
		t.Errorf("remoteURLForResolve(%q) = %q, want %q", "anthropics/skills", got, want)
	}

	// A `.git` suffix on the slug must not be doubled.
	if got := remoteURLForResolve("anthropics/skills.git"); got != want {
		t.Errorf("remoteURLForResolve trimmed .git incorrectly: got %q, want %q", got, want)
	}
}

func TestResolveTag_RejectsNonSlugRepo(t *testing.T) {
	// A repo that is not a clean <owner>/<repo> slug must be rejected
	// before any URL is constructed: this is the SSRF / file:// / host-
	// smuggling guard. None of these reach the network.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cases := []string{
		"file:///etc/passwd",
		"https://evil.com/anthropics/skills",
		"anthropics/skills@evil.com",
		"anthropics/skills/extra",
		"git@github.com:anthropics/skills",
		"anthropics",
	}
	for _, repo := range cases {
		t.Run(repo, func(t *testing.T) {
			if _, err := ResolveTag(ctx, repo, "v1.0.0"); err == nil {
				t.Errorf("ResolveTag accepted non-slug repo %q, want error", repo)
			}
			if _, _, err := ResolveRef(ctx, repo, "v1.0.0"); err == nil {
				t.Errorf("ResolveRef accepted non-slug repo %q, want error", repo)
			}
		})
	}
}
