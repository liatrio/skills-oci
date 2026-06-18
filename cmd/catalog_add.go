package cmd

import (
	"bufio"
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

// repoSegmentPattern is the allow-list a single owner or repo path segment
// must match in the flag-form `--repo <owner>/<repo>` input. It rejects
// anything outside GitHub's owner/repo charset — crucially `:`, `@`, and
// `/` — so a value like `http://169.254.169.254/latest/meta-data` cannot be
// smuggled through as owner/repo and reach the resolver (SSRF / host
// smuggling). Validated before any network call.
var repoSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

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
	// VendoredPath is the output vendored.json path (default vendored.json).
	// catalog add is purely a vendored.json mutator: it touches no other file.
	VendoredPath string
	// Yes is the non-interactive overwrite override. When an entry already
	// exists, Yes proceeds without prompting; it is required to overwrite in
	// any non-interactive context (--plain / no TTY).
	Yes bool
	// Plain mirrors the global --plain flag. It marks a non-interactive
	// (scripting/CI) run: the overwrite prompt must never block such a run, so
	// an overwrite under --plain requires Yes.
	Plain  bool
	DryRun bool
	// Timeout bounds the two network-bound steps (ResolveRef + Fetch).
	// cmd.Context() has no deadline of its own, so the orchestrator wraps
	// it with context.WithTimeout(Timeout) around those steps.
	Timeout time.Duration
}

// puller and resolver are minimal interfaces over the package-level
// functions in pkg/scm. Production code uses the real package functions;
// tests can supply doubles when they want to avoid go-git overhead.
//
// puller clones an upstream (sub)tree at a commit into a temp dir and
// discovers the skills inside it. The two steps are one interface because
// discovery always runs over a checkout the puller just produced.
type puller interface {
	Checkout(ctx context.Context, ref scm.SourceRef, dst string) error
	Discover(dst, relRoot string) (subpaths []string, err error)
}

// resolver resolves a user-supplied ref (tag, branch, or SHA) to a
// commit SHA. The immutable bool reports whether the input ref is an
// immutable label (true for tags and SHAs, false for branches); the vendored
// row always pins the resolved SHA, so the ref's mutability does not affect
// what is persisted. ResolveHEAD resolves the default-branch tip for
// bare-repo inputs that carry no ref.
type resolver interface {
	ResolveRef(ctx context.Context, repo, ref string) (sha string, immutable bool, err error)
	ResolveHEAD(ctx context.Context, repo string) (sha string, err error)
}

type realPuller struct{}

func (realPuller) Checkout(ctx context.Context, ref scm.SourceRef, dst string) error {
	return scm.Checkout(ctx, ref, dst)
}

func (realPuller) Discover(dst, relRoot string) ([]string, error) {
	return scm.DiscoverSkills(dst, relRoot)
}

type realResolver struct{}

func (realResolver) ResolveRef(ctx context.Context, repo, ref string) (string, bool, error) {
	return scm.ResolveRef(ctx, repo, ref)
}

func (realResolver) ResolveHEAD(ctx context.Context, repo string) (string, error) {
	return scm.ResolveHEAD(ctx, repo)
}

func newCatalogAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [URL]",
		Short: "Vendor one or more third-party skills into vendored.json",
		Long: "Resolves an upstream GitHub URL (or component flags) to an immutable commit SHA and upserts source-pin entries into vendored.json. " +
			"A URL pointing at a skill directory vendors that one skill; a URL pointing at a container directory or a whole repo " +
			"recursively discovers every directory containing a SKILL.md and vendors each. A bare repo URL (no /tree/<ref>) resolves the " +
			"default branch. Never contacts the destination registry.",
		Example: `  # Single skill, URL form (tag)
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill

  # Single skill, URL form (branch) — the branch's head commit SHA is recorded as the entry's commit pin
  skills-oci catalog add https://github.com/anthropics/skills/tree/main/skills/skill-creator

  # Many skills — repo root at a commit: discovers and vendors every skill
  skills-oci catalog add https://github.com/anthropics/skills/tree/da20c92503b2e8ff1cf28ca81a0df4673debdbf7

  # Many skills — bare repo: resolves the default branch, then discovers all skills
  skills-oci catalog add https://github.com/vercel-labs/agent-skills

  # Flag form (single skill)
  skills-oci catalog add --repo anthropics/skills --subpath skills/create-skill --version v1.0.0

  # Overwrite existing entries non-interactively (CI / --plain)
  skills-oci catalog add --plain -y https://github.com/anthropics/skills/tree/v1.1.0

  # Dry run prints the would-be entries without writing vendored.json
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCatalogAdd,
	}
	cmd.Flags().String("repo", "", "Upstream <owner>/<repo> slug (mutually exclusive with positional URL)")
	cmd.Flags().String("subpath", "", "Path within the upstream repo; a skill directory vendors one skill, a container directory discovers and vendors all skills beneath it")
	cmd.Flags().String("version", "", "Upstream tag, branch, or 40-hex commit SHA to vendor (resolved to an immutable commit SHA, which is recorded as the entry's commit pin)")
	cmd.Flags().String("name", "", "Local entry name for a single skill (default: last segment of the subpath); not allowed when multiple skills are discovered")
	cmd.Flags().String("internal-ref", "", "Destination OCI ref without tag for a single skill (overrides --namespace derivation); not allowed when multiple skills are discovered")
	cmd.Flags().String("namespace", "", "Destination namespace prefix; combined with --name to derive --internal-ref")
	cmd.Flags().String("vendored", "vendored.json", "Path to vendored.json")
	cmd.Flags().BoolP("yes", "y", false, "Overwrite an existing entry without prompting (required to overwrite in non-interactive/--plain mode)")
	cmd.Flags().Bool("dry-run", false, "Print the would-be entry and exit without writing vendored.json")
	cmd.Flags().Duration("timeout", 60*time.Second, "Maximum time for the network-bound resolve + fetch steps")
	return cmd
}

func runCatalogAdd(cmd *cobra.Command, args []string) error {
	opts, err := parseAddOpts(cmd, args)
	if err != nil {
		return err
	}
	return runCatalogAddWithDeps(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(), opts, configFromContextAccessor(cmd.Context()), realResolver{}, realPuller{})
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
	o.VendoredPath, _ = cmd.Flags().GetString("vendored")
	o.Yes, _ = cmd.Flags().GetBool("yes")
	o.DryRun, _ = cmd.Flags().GetBool("dry-run")
	o.Timeout, _ = cmd.Flags().GetDuration("timeout")
	// --plain is a persistent flag on the root command; tolerate its absence
	// (e.g. when the subcommand is built standalone in a unit test).
	o.Plain, _ = cmd.Flags().GetBool("plain")

	upstreamFlagsSet := o.Repo != "" || o.Subpath != "" || o.Version != ""
	if o.URL != "" && upstreamFlagsSet {
		return addOpts{}, fmt.Errorf("ambiguous input: provide either a URL or --repo/--subpath/--version, not both")
	}
	if o.URL == "" && !upstreamFlagsSet {
		return addOpts{}, fmt.Errorf("missing input: provide either a URL or --repo/--subpath/--version")
	}
	return o, nil
}

// plannedEntry is one discovered skill's resolved vendored entry plus
// whether writing it would overwrite an existing (namespace, name) row.
type plannedEntry struct {
	entry      catalog.VendoredEntry
	overwrites bool
}

// runCatalogAddWithDeps is the orchestration layer. It takes the resolver
// and puller as interfaces, and the prompt's input as an io.Reader, so tests
// can swap them out without a network or a TTY. The flow is unified across
// single- and multi-skill adds: it always clones the (sub)tree, discovers
// every SKILL.md directory inside it, and vendors each. A URL/flag subpath
// that is itself a skill discovers exactly itself (the classic single-skill
// case). Any error before the final write leaves vendored.json untouched.
func runCatalogAddWithDeps(ctx context.Context, out io.Writer, in io.Reader, o addOpts, cfg interface {
	GetDefaultNamespace() string
}, res resolver, pul puller) error {
	// Step 1: parse URL or flag form into the upstream-side fields. Subpath
	// and version may be empty for the repo-root and bare-repo URL forms.
	owner, repo, rootSubpath, version, err := resolveUpstreamInputs(o)
	if err != nil {
		return err
	}

	// The two network-bound steps (resolve, checkout) share a deadline so a
	// hung resolver or clone cannot block indefinitely. cmd.Context() has no
	// deadline of its own; a non-positive Timeout means "no deadline".
	netCtx := ctx
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		netCtx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	// Step 2: resolve ref → commit SHA. An empty version is a bare-repo input
	// with no ref: resolve the default-branch HEAD instead.
	commit, err := resolveCommit(netCtx, out, res, owner, repo, version)
	if err != nil {
		return err
	}

	// Step 3: clone the (sub)tree at the commit and discover the skills in it.
	tmp, err := os.MkdirTemp("", "skills-oci-catalog-add-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if rootSubpath != "" {
		fmt.Fprintf(out, "fetching subpath %s\n", rootSubpath)
	} else {
		fmt.Fprintf(out, "fetching repo %s/%s\n", owner, repo)
	}
	ref := scm.SourceRef{Owner: owner, Repo: repo, Subpath: rootSubpath, Commit: commit}
	if err := pul.Checkout(netCtx, ref, tmp); err != nil {
		return fmt.Errorf("fetching subpath %s: %w", rootSubpath, err)
	}
	subpaths, err := pul.Discover(tmp, rootSubpath)
	if err != nil {
		return err
	}

	// singleTarget is the classic case: the URL/flag subpath is itself a
	// skill. Only then do --name / --internal-ref overrides apply. Any other
	// shape (a container directory, a repo root, or more than one discovered
	// skill) is discovery mode, where each skill is auto-named from its
	// directory and only --namespace governs the destination.
	singleTarget := len(subpaths) == 1 && rootSubpath != "" && subpaths[0] == rootSubpath
	if !singleTarget {
		if o.Name != "" || o.InternalRef != "" {
			return fmt.Errorf("--name and --internal-ref apply only to a single skill, but %d were discovered under %q; use --namespace instead", len(subpaths), rootSubpath)
		}
		where := "repo " + owner + "/" + repo
		if rootSubpath != "" {
			where = "subpath " + rootSubpath
		}
		fmt.Fprintf(out, "discovered %d skill(s) under %s\n", len(subpaths), where)
	}

	// Step 4: load existing vendored.json (empty if absent).
	cur, err := loadVendoredFile(o.VendoredPath)
	if err != nil {
		return err
	}

	// Step 5: build one planned entry per discovered skill, parsing each
	// upstream SKILL.md. A parse failure aborts the whole add (no partial
	// write).
	planned := make([]plannedEntry, 0, len(subpaths))
	for _, subpath := range subpaths {
		name := path.Base(subpath)
		if singleTarget && o.Name != "" {
			name = o.Name
		}
		internalRef, err := resolveInternalRef(o, cfg, name)
		if err != nil {
			return err
		}
		v2Namespace, err := extractV2Namespace(internalRef)
		if err != nil {
			return err
		}

		verifyLabel := "SKILL.md"
		if !singleTarget {
			verifyLabel = subpath + "/SKILL.md"
		}
		fmt.Fprintf(out, "verifying %s\n", verifyLabel)
		parsed, err := skill.Parse(filepath.Join(tmp, filepath.FromSlash(subpath)))
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

		planned = append(planned, plannedEntry{
			entry: catalog.VendoredEntry{
				Name:        name,
				Namespace:   v2Namespace,
				Repo:        owner + "/" + repo,
				Subpath:     subpath,
				Commit:      commit,
				InternalRef: internalRef,
				License:     parsed.Config.License,
			},
			overwrites: vendoredHasEntry(cur, v2Namespace, name),
		})
	}

	// Step 6: --dry-run short-circuit — print the would-be entries, write
	// nothing. Dry-run is exempt from the overwrite prompt.
	if o.DryRun {
		return printDryRun(out, planned, singleTarget)
	}

	// Step 7: per-skill overwrite confirmation. Declined skills are dropped;
	// the rest proceed.
	accepted, err := decideOverwrites(out, in, o, planned)
	if err != nil {
		return err
	}
	if len(accepted) == 0 {
		fmt.Fprintln(out, "aborted; vendored.json unchanged")
		return nil
	}

	// Step 8: upsert every accepted entry, then one atomic write.
	next := cur
	for _, p := range accepted {
		next, _ = catalog.UpsertVendored(next, p.entry)
	}
	if err := catalog.WriteVendoredAtomic(o.VendoredPath, next); err != nil {
		return err
	}
	if len(accepted) == 1 {
		fmt.Fprintf(out, "catalog add: wrote entry %q to %s\n", accepted[0].entry.Name, o.VendoredPath)
	} else {
		fmt.Fprintf(out, "catalog add: wrote %d entries to %s\n", len(accepted), o.VendoredPath)
	}
	return nil
}

// resolveCommit turns a user ref into an immutable commit SHA. An empty
// version means a bare-repo input with no ref: resolve the default-branch
// HEAD instead. Otherwise resolve the tag/branch/SHA. Either way the row is
// pinned to the resolved commit, so a mutable ref (branch, default HEAD) is
// fine — only the SHA is persisted.
func resolveCommit(ctx context.Context, out io.Writer, res resolver, owner, repo, version string) (commit string, err error) {
	slug := owner + "/" + repo
	if version == "" {
		fmt.Fprintf(out, "resolving %s@HEAD (default branch)\n", slug)
		commit, err = res.ResolveHEAD(ctx, slug)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(out, "  → commit %s\n", commit)
		return commit, nil
	}
	fmt.Fprintf(out, "resolving %s@%s\n", slug, version)
	commit, _, err = res.ResolveRef(ctx, slug, version)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "  → commit %s\n", commit)
	return commit, nil
}

// printDryRun reports the would-be entries without writing. The single-skill
// case keeps the historical "would add entry:" wording; discovery mode lists
// each entry with its name and per-skill action.
func printDryRun(out io.Writer, planned []plannedEntry, singleTarget bool) error {
	action := func(p plannedEntry) string {
		if p.overwrites {
			return "overwrite"
		}
		return "add"
	}
	if singleTarget {
		body, err := json.MarshalIndent(planned[0].entry, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling dry-run entry: %w", err)
		}
		fmt.Fprintf(out, "would %s entry:\n%s\n", action(planned[0]), body)
		return nil
	}
	fmt.Fprintf(out, "would write %d entries:\n", len(planned))
	for _, p := range planned {
		body, err := json.MarshalIndent(p.entry, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling dry-run entry: %w", err)
		}
		fmt.Fprintf(out, "would %s entry %q:\n%s\n", action(p), p.entry.Name, body)
	}
	return nil
}

// decideOverwrites filters planned entries by overwrite confirmation.
// Non-overwriting entries always pass. For overwriting entries: --yes passes
// all; --plain without --yes is a non-zero error naming every conflict (so a
// scripted run never blocks); otherwise it prompts per skill and keeps only
// the confirmed ones. The input reader is shared across prompts.
func decideOverwrites(out io.Writer, in io.Reader, o addOpts, planned []plannedEntry) ([]plannedEntry, error) {
	if !o.Yes && o.Plain {
		var conflicts []string
		for _, p := range planned {
			if p.overwrites {
				conflicts = append(conflicts, p.entry.Namespace+"/"+p.entry.Name)
			}
		}
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("entry %s already exists in vendored.json; pass -y/--yes to overwrite in non-interactive mode", strings.Join(conflicts, ", "))
		}
	}

	reader := bufio.NewReader(in)
	accepted := make([]plannedEntry, 0, len(planned))
	for _, p := range planned {
		if !p.overwrites || o.Yes {
			accepted = append(accepted, p)
			continue
		}
		fmt.Fprintf(out, "entry %s/%s already exists in vendored.json; overwrite? [y/N] ", p.entry.Namespace, p.entry.Name)
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans == "y" || ans == "yes" {
			accepted = append(accepted, p)
		} else {
			fmt.Fprintf(out, "  skipping %s/%s\n", p.entry.Namespace, p.entry.Name)
		}
	}
	return accepted, nil
}

// loadVendoredFile reads vendored.json from path. A missing file bootstraps an
// empty Vendored{SchemaVersion: 1} so the first add in a repo works; any other
// read or parse error is wrapped with the path for context.
func loadVendoredFile(path string) (catalog.Vendored, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog.Vendored{SchemaVersion: 1}, nil
		}
		return catalog.Vendored{}, fmt.Errorf("reading %s: %w", path, err)
	}
	v, err := catalog.LoadVendored(data)
	if err != nil {
		return catalog.Vendored{}, err
	}
	return v, nil
}

// vendoredHasEntry reports whether v already contains an entry keyed by
// (namespace, name) — the same identity UpsertVendored uses.
func vendoredHasEntry(v catalog.Vendored, namespace, name string) bool {
	for _, e := range v.Skills {
		if e.Namespace == namespace && e.Name == name {
			return true
		}
	}
	return false
}

// extractV2Namespace pulls the single-segment v2 namespace out of an
// internal_ref of the form `<registry>/<namespace>/skills/<name>` (the
// `skills-oci` convention). The registry host is always the first
// segment, the v2 namespace is always the second. The ref must be a
// well-formed registry reference (`<registry>/<namespace>/<name>`, at
// least three non-empty segments) — catalog.ValidateInternalRef is the
// single source of that grammar, shared with the vendored.json contract
// gate. Rejecting an under-qualified ref here (e.g. `liatrio/<name>` from
// `--namespace liatrio`) fails the add fast instead of writing a row the
// downstream catalog-sync build would reject.
func extractV2Namespace(internalRef string) (string, error) {
	if err := catalog.ValidateInternalRef(internalRef); err != nil {
		return "", fmt.Errorf("cannot derive v2 namespace from internal_ref %q (expected <registry>/<namespace>/skills/<name>)", internalRef)
	}
	return strings.Split(internalRef, "/")[1], nil
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
	// Validate both segments against the owner/repo charset allow-list
	// before they are recombined and handed to the resolver. This rejects
	// `:`, `@`, embedded slashes, and other host-smuggling characters so a
	// value like `http://169.254.169.254/latest/meta-data` cannot reach a
	// network call (SSRF).
	if !repoSegmentPattern.MatchString(parts[0]) || !repoSegmentPattern.MatchString(parts[1]) {
		return "", "", "", "", fmt.Errorf("--repo must be a bare <owner>/<repo> slug matching %s, got %q", repoSegmentPattern.String(), o.Repo)
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
// following precedence chain:
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
