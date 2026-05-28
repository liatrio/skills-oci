package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/liatrio/skills-oci/pkg/catalog"
	"github.com/liatrio/skills-oci/pkg/scm"
	"github.com/liatrio/skills-oci/pkg/skill"
	"github.com/spf13/cobra"
)

// semverTagPattern matches a SemVer 2.0.0 tag with an optional leading
// `v`. The leading `v` is stripped before persistence so the catalog's
// latest_version field passes the platform validator (which is strict
// SemVer per frontend/src/lib/contract/semver.ts).
var semverTagPattern = regexp.MustCompile(
	`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?` +
		`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

// addOpts is the resolved set of inputs for `catalog add`. The Cobra
// layer parses flags + positional into this struct so the orchestration
// logic in runCatalogAddWithDeps stays clean and testable.
type addOpts struct {
	URL         string // positional arg, may be empty when using flag form
	Repo        string
	Subpath     string
	Version     string
	Name        string
	InternalRef string
	Namespace   string
	CatalogPath string
	// DetailDir, when non-empty, opts into writing a per-skill detail
	// file at <DetailDir>/<namespace>/<name>.json (the shape the
	// skills-platform frontend's validateSkillDetail expects). Empty
	// means skip detail writes entirely — `catalog add` is then purely
	// a catalog.json mutator with no side effects on other paths.
	DetailDir string
	DryRun    bool
}

// fetcher and resolver are minimal interfaces over the package-level
// functions in pkg/scm. Production code uses the real package functions;
// tests can supply doubles when they want to avoid go-git overhead.
type fetcher interface {
	Fetch(ctx context.Context, ref scm.SourceRef, dst string) error
}

// resolver resolves a user-supplied ref (tag, branch, or SHA) to a
// commit SHA. The immutable bool reports whether the input ref is an
// immutable label safe to persist in the catalog row's `version` field:
// true for tags and SHAs, false for branches. The orchestrator overwrites
// the captured ref string with the SHA when immutable is false so the
// catalog never carries a mutable branch name.
type resolver interface {
	ResolveRef(ctx context.Context, repo, ref string) (sha string, immutable bool, err error)
}

type realFetcher struct{}

func (realFetcher) Fetch(ctx context.Context, ref scm.SourceRef, dst string) error {
	return scm.Fetch(ctx, ref, dst)
}

type realResolver struct{}

func (realResolver) ResolveRef(ctx context.Context, repo, ref string) (string, bool, error) {
	return scm.ResolveRef(ctx, repo, ref)
}

func newCatalogAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [URL]",
		Short: "Add a third-party skill entry to catalog.json",
		Long:  "Resolves an upstream GitHub URL (or component flags) to an immutable commit SHA, verifies the upstream subpath contains SKILL.md, and appends the entry to catalog.json. Never contacts the destination registry.",
		Example: `  # URL form
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill

  # Flag form
  skills-oci catalog add --repo anthropics/skills --subpath skills/create-skill --version v1.0.0

  # Dry run prints the resolved entry without writing catalog.json
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCatalogAdd,
	}
	cmd.Flags().String("repo", "", "Upstream <owner>/<repo> slug (mutually exclusive with positional URL)")
	cmd.Flags().String("subpath", "", "Path within the upstream repo to the skill directory")
	cmd.Flags().String("version", "", "Upstream tag or 40-hex commit SHA")
	cmd.Flags().String("name", "", "Local catalog entry name (defaults to last segment of --subpath)")
	cmd.Flags().String("internal-ref", "", "Destination OCI ref without tag (overrides --namespace derivation)")
	cmd.Flags().String("namespace", "", "Destination namespace prefix; combined with --name to derive --internal-ref")
	cmd.Flags().String("catalog", "catalog.json", "Path to catalog.json")
	cmd.Flags().String("detail-dir", "", "When set, also write a per-skill detail file at <detail-dir>/<namespace>/<name>.json (the shape the skills-platform frontend consumes). Empty (default) means do not touch any path besides --catalog.")
	cmd.Flags().Bool("dry-run", false, "Print the would-be entry and exit without writing catalog.json")
	return cmd
}

func runCatalogAdd(cmd *cobra.Command, args []string) error {
	opts, err := parseAddOpts(cmd, args)
	if err != nil {
		return err
	}
	return runCatalogAddWithDeps(cmd.Context(), cmd.OutOrStdout(), opts, configFromContextAccessor(cmd.Context()), realResolver{}, realFetcher{})
}

// parseAddOpts is split out for testability — it has no IO and no
// network, so failure modes for "URL+flags both given" / "missing
// required field" can be unit-tested without spinning up Cobra.
func parseAddOpts(cmd *cobra.Command, args []string) (addOpts, error) {
	o := addOpts{}
	if len(args) == 1 {
		o.URL = args[0]
	}
	o.Repo, _ = cmd.Flags().GetString("repo")
	o.Subpath, _ = cmd.Flags().GetString("subpath")
	o.Version, _ = cmd.Flags().GetString("version")
	o.Name, _ = cmd.Flags().GetString("name")
	o.InternalRef, _ = cmd.Flags().GetString("internal-ref")
	o.Namespace, _ = cmd.Flags().GetString("namespace")
	o.CatalogPath, _ = cmd.Flags().GetString("catalog")
	o.DetailDir, _ = cmd.Flags().GetString("detail-dir")
	o.DryRun, _ = cmd.Flags().GetBool("dry-run")

	upstreamFlagsSet := o.Repo != "" || o.Subpath != "" || o.Version != ""
	if o.URL != "" && upstreamFlagsSet {
		return addOpts{}, fmt.Errorf("ambiguous input: provide either a URL or --repo/--subpath/--version, not both")
	}
	if o.URL == "" && !upstreamFlagsSet {
		return addOpts{}, fmt.Errorf("missing input: provide either a URL or --repo/--subpath/--version")
	}
	return o, nil
}

// runCatalogAddWithDeps is the orchestration layer. It takes the
// resolver and fetcher as interfaces so tests can swap them out.
// Steps are ordered cheap-and-decisive first, network-bound second,
// file write last — any error before the write leaves catalog.json
// untouched.
func runCatalogAddWithDeps(ctx context.Context, out io.Writer, o addOpts, cfg interface {
	GetDefaultNamespace() string
}, res resolver, fet fetcher) error {
	// Step 1: parse URL or use flag form to populate the upstream-side
	// fields (owner, repo, subpath, version).
	owner, repo, subpath, version, err := resolveUpstreamInputs(o)
	if err != nil {
		return err
	}

	// Step 2: derive name + internal_ref.
	name := o.Name
	if name == "" {
		name = path.Base(subpath)
	}
	internalRef, err := resolveInternalRef(o, cfg, name)
	if err != nil {
		return err
	}

	// Step 3: resolve ref → commit SHA. Tags, branches, and 40-hex SHAs
	// are all accepted; branches resolve to the head commit and trigger
	// the version-swap below so the catalog row records the SHA instead
	// of the mutable branch name.
	fmt.Fprintf(out, "resolving %s/%s@%s\n", owner, repo, version)
	commit, immutable, err := res.ResolveRef(ctx, owner+"/"+repo, version)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  → commit %s\n", commit)
	if !immutable {
		fmt.Fprintf(out, "  note: %q is mutable; recording resolved SHA as version for an immutable catalog row\n", version)
		version = commit
	}

	// Step 4: fetch subpath at SHA into temp dir, verify SKILL.md.
	tmp, err := os.MkdirTemp("", "skills-oci-catalog-add-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintf(out, "fetching subpath %s\n", subpath)
	ref := scm.SourceRef{Owner: owner, Repo: repo, Subpath: subpath, Commit: commit}
	if err := fet.Fetch(ctx, ref, tmp); err != nil {
		return err
	}
	fmt.Fprintln(out, "verifying SKILL.md")

	// Step 5: read upstream SKILL.md frontmatter and surface name/version/license.
	skillDir := filepath.Join(tmp, filepath.FromSlash(subpath))
	parsed, err := skill.Parse(skillDir)
	if err != nil {
		return fmt.Errorf("reading upstream SKILL.md: %w", err)
	}
	fmt.Fprintf(out, "  upstream name: %s\n", parsed.Config.Name)
	if parsed.Config.Version != "" {
		fmt.Fprintf(out, "  upstream version: %s\n", parsed.Config.Version)
	}
	if parsed.Config.License != "" {
		fmt.Fprintf(out, "  upstream license: %s\n", parsed.Config.License)
	}

	// Step 6: derive the v2 namespace from the resolved internal_ref. The
	// platform validator requires a single-segment identifier; the
	// internal_ref format `<registry>/<namespace>/skills/<name>` makes the
	// second segment the canonical source.
	v2Namespace, err := extractV2Namespace(internalRef)
	if err != nil {
		return err
	}

	// Step 7: derive the v2 latest_version through a precedence chain so
	// SHA-pinned vendoring still produces a real SemVer and therefore a
	// published row + writable detail file:
	//
	//   1. Inbound ref is itself a SemVer tag (with optional leading `v`)
	//   2. SKILL.md frontmatter `metadata.version`
	//   3. SKILL.md frontmatter top-level `version:`
	//   4. Synthetic `0.0.0+sha.<commit-short>` build-metadata fallback
	//
	// Status is always `published` because every step produces a SemVer
	// the detail file's contract can hold.
	latestVersion := deriveLatestVersion(version, parsed.Config, commit)
	status := catalog.StatusPublished

	// Truncate to second precision: the platform validator's isRfc3339Utc
	// rejects any fractional-second component on timestamps.
	now := time.Now().UTC().Truncate(time.Second)

	// Step 8: load existing catalog.json (or zero-value if absent).
	cur, err := loadCatalogFile(o.CatalogPath, now)
	if err != nil {
		return err
	}

	// Step 9: append entry via the pure AddEntry helper.
	entry := catalog.Entry{
		Namespace:     v2Namespace,
		Name:          name,
		LatestVersion: latestVersion,
		UpdatedAt:     now,
		Status:        status,
		Visibility:    catalog.VisibilityPublic,
		Repo:          owner + "/" + repo,
		Subpath:       subpath,
		Version:       version,
		Commit:        commit,
		InternalRef:   internalRef,
	}
	next, err := catalog.AddEntry(cur, entry)
	if err != nil {
		return err
	}
	// Re-stamp generated_at to reflect the moment of write, matching the
	// platform indexer's behavior.
	next.GeneratedAt = now

	// Step 10: --dry-run short-circuit.
	if o.DryRun {
		body, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Fprintf(out, "would add entry:\n%s\n", body)
		return nil
	}

	// Step 11: atomic write of the catalog row.
	if err := catalog.WriteCatalogAtomic(o.CatalogPath, next); err != nil {
		return err
	}
	fmt.Fprintf(out, "catalog add: appended entry %q to %s\n", name, o.CatalogPath)

	// Step 12: opt-in per-skill detail file. When --detail-dir is unset
	// catalog add is purely a catalog.json mutator with no side effects
	// on other paths — important for non-platform users vendoring third-
	// party skills into their own workflows. When --detail-dir is set
	// (typically `core/data/skills` against the skills-platform repo)
	// write <detail-dir>/<namespace>/<name>.json in the shape the
	// platform frontend's validateSkillDetail expects.
	//
	// We read the SKILL.md bytes directly off the fetched subpath so the
	// `body` field is byte-identical to upstream (pkg/skill.Parse drops
	// the raw frontmatter bytes; reconstructing from parsed fields would
	// lose `metadata:` and any other YAML the parser didn't surface as a
	// dedicated field).
	if o.DetailDir == "" {
		return nil
	}
	skillMDBytes, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("reading SKILL.md for detail body: %w", err)
	}
	detailPath := filepath.Join(o.DetailDir, v2Namespace, name+".json")
	detail := buildSkillDetail(entry, parsed, string(skillMDBytes), now)
	if err := catalog.WriteSkillDetailAtomic(detailPath, detail); err != nil {
		return err
	}
	fmt.Fprintf(out, "catalog add: wrote detail %q to %s\n", name, detailPath)
	return nil
}

// buildSkillDetail assembles the per-skill detail wire shape from the
// new catalog entry plus the parsed upstream SKILL.md. `oci_ref` is the
// internal_ref with any tag stripped (the detail contract carries the
// untagged ref); `repo_url` points at the exact tree the SKILL.md came
// from so reviewers can verify the vendored content. `body` is the
// verbatim upstream SKILL.md bytes (passed in by the caller so the
// frontmatter is preserved byte-for-byte, including fields the parsed
// SkillConfig doesn't surface like `metadata.*`).
func buildSkillDetail(e catalog.Entry, parsed *skill.SkillDirectory, rawSkillMD string, now time.Time) catalog.SkillDetail {
	ociRef := e.InternalRef
	if idx := strings.IndexAny(ociRef, ":@"); idx != -1 {
		ociRef = ociRef[:idx]
	}
	// Use the original upstream ref (tag or SHA) the user vendored at,
	// not the resolved commit. When a tag was provided this keeps the
	// repo_url human-readable and stable; when a SHA was provided this
	// is a no-op because e.Version == e.Commit in that case.
	repoURL := fmt.Sprintf("https://github.com/%s/tree/%s/%s", e.Repo, e.Version, e.Subpath)
	return catalog.SkillDetail{
		SchemaVersion: 2,
		Namespace:     e.Namespace,
		Name:          e.Name,
		LatestVersion: e.LatestVersion,
		Visibility:    e.Visibility,
		Status:        e.Status,
		Description:   parsed.Config.Description,
		RepoURL:       repoURL,
		OCIRef:        ociRef,
		Versions: []catalog.SkillVersion{{
			Version:     e.LatestVersion,
			PublishedAt: now,
			Body:        rawSkillMD,
		}},
	}
}

// extractV2Namespace pulls the single-segment v2 namespace out of an
// internal_ref of the form `<registry>/<namespace>/skills/<name>` (the
// `skills-oci` convention). The registry host is always the first
// segment, the v2 namespace is always the second. Errors when the ref
// has fewer than two path segments — the platform validator would reject
// such a value anyway, so failing early gives a better error.
func extractV2Namespace(internalRef string) (string, error) {
	parts := strings.Split(internalRef, "/")
	if len(parts) < 2 || parts[1] == "" {
		return "", fmt.Errorf("cannot derive v2 namespace from internal_ref %q (expected <registry>/<namespace>/skills/<name>)", internalRef)
	}
	return parts[1], nil
}

// deriveLatestVersion walks the precedence chain documented in step 7
// of runCatalogAddWithDeps and returns the best SemVer label for this
// vendor row. Always returns a non-empty SemVer 2.0.0 string; the
// synthetic build-metadata fallback at the bottom is the contract that
// makes "status: published" honest for SHA-only inputs.
func deriveLatestVersion(versionRef string, cfg skill.SkillConfig, commit string) string {
	// 1. The inbound ref is itself a SemVer (strip optional leading `v`).
	if isSemverTag(versionRef) {
		return strings.TrimPrefix(versionRef, "v")
	}
	// 2. SKILL.md frontmatter `metadata.version` — the convention the
	//    platform's own publish-skill workflow stamps into liatrio skills.
	if v, ok := cfg.Metadata["version"].(string); ok && isSemverTag(v) {
		return strings.TrimPrefix(v, "v")
	}
	// 3. SKILL.md frontmatter top-level `version:` — legacy / non-spec
	//    but present in some skills.
	if isSemverTag(cfg.Version) {
		return strings.TrimPrefix(cfg.Version, "v")
	}
	// 4. Synthetic build-metadata SemVer. Build metadata after `+` does
	//    not affect SemVer precedence (per §10), but the string is valid,
	//    the detail-file contract accepts it, and the commit-short label
	//    is informative.
	return "0.0.0+sha." + shortSHA(commit)
}

// isSemverTag reports whether s is a SemVer 2.0.0 with an optional
// leading `v` (which the caller will strip).
func isSemverTag(s string) bool {
	return semverTagPattern.MatchString(s)
}

// shortSHA returns the first 8 characters of s, or s itself when it is
// shorter than 8. Used by the synthetic SemVer fallback to embed a
// commit prefix as build metadata.
func shortSHA(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

// resolveUpstreamInputs picks values from either the positional URL or
// the flag-form inputs and normalizes them. Validation of well-formed
// URLs happens in pkg/scm.ParseGitHubTreeURL; this function only chooses
// the source.
func resolveUpstreamInputs(o addOpts) (owner, repo, subpath, version string, err error) {
	if o.URL != "" {
		owner, repo, version, subpath, err = scm.ParseGitHubTreeURL(o.URL)
		return
	}
	// Flag form. Split repo into owner + repo segments.
	parts := strings.SplitN(o.Repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("--repo must be a bare <owner>/<repo> slug, got %q", o.Repo)
	}
	if o.Subpath == "" {
		return "", "", "", "", fmt.Errorf("--subpath is required when not using a URL")
	}
	if o.Version == "" {
		return "", "", "", "", fmt.Errorf("--version is required when not using a URL")
	}
	return parts[0], parts[1], strings.Trim(o.Subpath, "/"), o.Version, nil
}

// resolveInternalRef computes the destination OCI ref using the
// precedence chain documented in docs/skills-catalog-data-contract.md:
// --internal-ref > --namespace flag > project config default_namespace
// > SKILLS_OCI_DEFAULT_NAMESPACE env var > error.
func resolveInternalRef(o addOpts, cfg interface{ GetDefaultNamespace() string }, name string) (string, error) {
	if o.InternalRef != "" {
		return o.InternalRef, nil
	}
	ns := o.Namespace
	if ns == "" && cfg != nil {
		ns = cfg.GetDefaultNamespace()
	}
	if ns == "" {
		ns = os.Getenv("SKILLS_OCI_DEFAULT_NAMESPACE")
	}
	if ns == "" {
		return "", fmt.Errorf("no default namespace configured; pass --namespace, set catalog.default_namespace in .skills-oci.yaml, or export SKILLS_OCI_DEFAULT_NAMESPACE")
	}
	return strings.TrimRight(ns, "/") + "/" + name, nil
}

// loadCatalogFile reads catalog.json from path. If the file does not
// exist a zero-value v2 Catalog stamped with `now` as generated_at is
// returned so the first `catalog add` in a repo bootstraps cleanly.
// Files in the legacy v1 shape (or any partially-populated v2 file) are
// migrated to a full v2 shape in-memory so subsequent AddEntry/Validate
// calls succeed and the rewritten file matches the platform contract.
func loadCatalogFile(path string, now time.Time) (catalog.Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog.Catalog{SchemaVersion: 2, GeneratedAt: now}, nil
		}
		return catalog.Catalog{}, fmt.Errorf("reading %s: %w", path, err)
	}
	c, err := catalog.Load(data)
	if err != nil {
		return catalog.Catalog{}, err
	}
	return migrateToV2(c, now), nil
}

// migrateToV2 fills any missing v2 surface fields on c and its entries
// using values derivable from the v1 source-pin fields already present.
// This is the in-memory shim for legacy v1 catalog.json files left in
// the wild (e.g. produced by earlier skills-oci versions). Already-v2
// catalogs pass through unchanged because each assignment is guarded by
// an "if empty/zero" check, making the migration idempotent.
func migrateToV2(c catalog.Catalog, now time.Time) catalog.Catalog {
	if c.SchemaVersion != 2 {
		c.SchemaVersion = 2
	}
	if c.GeneratedAt.IsZero() {
		c.GeneratedAt = now
	}
	for i := range c.Skills {
		// Per-entry migration only applies to rows that look like
		// vendor-managed entries from a legacy v1 file (i.e. they carry
		// source-pin fields but may be missing the v2 surface fields).
		// Pure indexer-managed rows are already v2-shaped and may
		// intentionally carry the Go zero time on `updated_at` for
		// unpublished status; leaving them untouched preserves that
		// signal.
		if !hasAnySourcePin(c.Skills[i]) {
			continue
		}
		if c.Skills[i].Namespace == "" {
			// Derive from internal_ref; ignore the error here so a
			// genuinely malformed entry still surfaces via Validate
			// with a clearer field-level message.
			ns, _ := extractV2Namespace(c.Skills[i].InternalRef)
			c.Skills[i].Namespace = ns
		}
		if c.Skills[i].Status == "" {
			// Legacy v1 rows always carried a source-pin commit, so we
			// can derive a real SemVer for them through the same chain
			// catalog add uses for fresh rows. SKILL.md isn't available
			// during migration, so steps 2 and 3 of the chain are
			// implicitly skipped (the skill.SkillConfig is its zero
			// value); the synthetic SHA fallback always fires.
			c.Skills[i].LatestVersion = deriveLatestVersion(c.Skills[i].Version, skill.SkillConfig{}, c.Skills[i].Commit)
			c.Skills[i].Status = catalog.StatusPublished
		}
		if c.Skills[i].UpdatedAt.IsZero() {
			c.Skills[i].UpdatedAt = now
		}
		if c.Skills[i].Visibility == "" {
			c.Skills[i].Visibility = catalog.VisibilityPublic
		}
	}
	return c
}

// hasAnySourcePin reports whether the entry has at least one source-pin
// field set — the marker used by migrateToV2 to identify vendor rows.
// Centralizing the check here keeps the migration's "is this a vendor
// row?" question local to its only caller.
func hasAnySourcePin(e catalog.Entry) bool {
	return e.Repo != "" || e.Subpath != "" || e.Version != "" || e.Commit != "" || e.InternalRef != ""
}

// configAccessor adapts config.Config to the small interface
// runCatalogAddWithDeps expects, so the orchestrator does not import
// the config package directly (keeping that boundary clean).
type configAccessor struct {
	defaultNamespace string
}

func (c configAccessor) GetDefaultNamespace() string { return c.defaultNamespace }

// configFromContextAccessor wraps configFromContext into the interface
// shape the orchestrator expects. Used by the production wiring; tests
// pass their own configAccessor directly.
func configFromContextAccessor(ctx context.Context) configAccessor {
	cfg := configFromContext(ctx)
	return configAccessor{defaultNamespace: cfg.Catalog.DefaultNamespace}
}
