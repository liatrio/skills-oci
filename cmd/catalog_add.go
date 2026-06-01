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

// fetcher and resolver are minimal interfaces over the package-level
// functions in pkg/scm. Production code uses the real package functions;
// tests can supply doubles when they want to avoid go-git overhead.
type fetcher interface {
	Fetch(ctx context.Context, ref scm.SourceRef, dst string) error
}

// resolver resolves a user-supplied ref (tag, branch, or SHA) to a
// commit SHA. The immutable bool reports whether the input ref is an
// immutable label safe to persist in the entry's `version` field:
// true for tags and SHAs, false for branches. The orchestrator overwrites
// the captured ref string with the SHA when immutable is false so the
// vendored row never carries a mutable branch name.
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
		Short: "Vendor a third-party skill into vendored.json",
		Long:  "Resolves an upstream GitHub URL (or component flags) to an immutable commit SHA, verifies the upstream subpath contains SKILL.md, and upserts the source-pin entry into vendored.json. Never contacts the destination registry.",
		Example: `  # URL form (tag)
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill

  # URL form (branch) — the resolver looks up the branch's head commit and records that SHA as the entry's version
  skills-oci catalog add https://github.com/anthropics/skills/tree/main/skills/skill-creator

  # Flag form
  skills-oci catalog add --repo anthropics/skills --subpath skills/create-skill --version v1.0.0

  # Overwrite an existing entry non-interactively (CI / --plain)
  skills-oci catalog add --plain -y https://github.com/anthropics/skills/tree/v1.1.0/skills/create-skill

  # Dry run prints the resolved entry without writing vendored.json
  skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCatalogAdd,
	}
	cmd.Flags().String("repo", "", "Upstream <owner>/<repo> slug (mutually exclusive with positional URL)")
	cmd.Flags().String("subpath", "", "Path within the upstream repo to the skill directory")
	cmd.Flags().String("version", "", "Upstream tag, branch, or 40-hex commit SHA (branches are resolved to the head commit and recorded as a SHA)")
	cmd.Flags().String("name", "", "Local entry name (default: last segment of the upstream subpath, whether from the URL or --subpath)")
	cmd.Flags().String("internal-ref", "", "Destination OCI ref without tag (overrides --namespace derivation)")
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
	return runCatalogAddWithDeps(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(), opts, configFromContextAccessor(cmd.Context()), realResolver{}, realFetcher{})
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

// runCatalogAddWithDeps is the orchestration layer. It takes the resolver
// and fetcher as interfaces, and the prompt's input as an io.Reader, so
// tests can swap them out without a network or a TTY. Steps are ordered
// cheap-and-decisive first, network-bound second, file write last — any
// error before the write leaves vendored.json untouched.
func runCatalogAddWithDeps(ctx context.Context, out io.Writer, in io.Reader, o addOpts, cfg interface {
	GetDefaultNamespace() string
}, res resolver, fet fetcher) error {
	// Step 1: parse URL or flag form into the upstream-side fields.
	owner, repo, subpath, version, err := resolveUpstreamInputs(o)
	if err != nil {
		return err
	}

	// Step 2: derive name + internal_ref + the v2 namespace. None of this
	// needs the network, so it is available before the overwrite check below.
	name := o.Name
	if name == "" {
		name = path.Base(subpath)
	}
	internalRef, err := resolveInternalRef(o, cfg, name)
	if err != nil {
		return err
	}
	v2Namespace, err := extractV2Namespace(internalRef)
	if err != nil {
		return err
	}

	// Step 3: load existing vendored.json (empty if absent) and decide whether
	// this add would overwrite an existing (namespace, name) entry.
	cur, err := loadVendoredFile(o.VendoredPath)
	if err != nil {
		return err
	}
	overwrites := vendoredHasEntry(cur, v2Namespace, name)

	// Step 4: overwrite confirmation. Done before the network steps so a
	// declined overwrite skips the fetch entirely. Dry-run never writes, so it
	// is exempt from the prompt (it reports the would-be action instead).
	if overwrites && !o.DryRun {
		proceed, err := confirmOverwrite(out, in, o, v2Namespace, name)
		if err != nil {
			return err
		}
		if !proceed {
			fmt.Fprintln(out, "aborted; vendored.json unchanged")
			return nil
		}
	}

	// The two network-bound steps (ResolveRef, Fetch) share a deadline so a
	// hung resolver or fetch cannot block indefinitely. cmd.Context() has no
	// deadline of its own; a non-positive Timeout means "no deadline".
	netCtx := ctx
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		netCtx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	// Step 5: resolve ref → commit SHA. Tags, branches, and 40-hex SHAs are
	// all accepted; branches resolve to the head commit and trigger the
	// version-swap below so the row records the SHA instead of the mutable
	// branch name.
	fmt.Fprintf(out, "resolving %s/%s@%s\n", owner, repo, version)
	commit, immutable, err := res.ResolveRef(netCtx, owner+"/"+repo, version)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  → commit %s\n", commit)
	if !immutable {
		fmt.Fprintf(out, "  note: %q is mutable; recording resolved SHA as version for an immutable row\n", version)
		version = commit
	}

	// Step 6: fetch subpath at SHA into temp dir, verify SKILL.md.
	tmp, err := os.MkdirTemp("", "skills-oci-catalog-add-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintf(out, "fetching subpath %s\n", subpath)
	ref := scm.SourceRef{Owner: owner, Repo: repo, Subpath: subpath, Commit: commit}
	if err := fet.Fetch(netCtx, ref, tmp); err != nil {
		return fmt.Errorf("fetching subpath %s: %w", subpath, err)
	}
	fmt.Fprintln(out, "verifying SKILL.md")

	// Step 7: read upstream SKILL.md frontmatter and surface name/version/license.
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

	// Step 8: build the vendored entry from the resolved coordinates.
	entry := catalog.VendoredEntry{
		Name:        name,
		Namespace:   v2Namespace,
		Repo:        owner + "/" + repo,
		Subpath:     subpath,
		Version:     version,
		Commit:      commit,
		InternalRef: internalRef,
	}

	// Step 9: --dry-run short-circuit — print the resolved entry and the
	// would-be action, write nothing.
	if o.DryRun {
		action := "add"
		if overwrites {
			action = "overwrite"
		}
		body, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling dry-run entry: %w", err)
		}
		fmt.Fprintf(out, "would %s entry:\n%s\n", action, body)
		return nil
	}

	// Step 10: upsert + atomic write.
	next, _ := catalog.UpsertVendored(cur, entry)
	if err := catalog.WriteVendoredAtomic(o.VendoredPath, next); err != nil {
		return err
	}
	fmt.Fprintf(out, "catalog add: wrote entry %q to %s\n", name, o.VendoredPath)
	return nil
}

// confirmOverwrite implements the overwrite-confirmation UX. It returns
// (proceed, error). Non-interactive contexts (--plain, or the --yes
// override) never prompt: --yes proceeds, --plain without --yes is a
// non-zero error naming the conflict. Otherwise it prints the prompt and
// reads a single line from in, proceeding only on an explicit y/yes.
func confirmOverwrite(out io.Writer, in io.Reader, o addOpts, namespace, name string) (bool, error) {
	if o.Yes {
		return true, nil
	}
	if o.Plain {
		return false, fmt.Errorf("entry %s/%s already exists in vendored.json; pass -y/--yes to overwrite in non-interactive mode", namespace, name)
	}
	fmt.Fprintf(out, "entry %s/%s already exists in vendored.json; overwrite? [y/N] ", namespace, name)
	line, _ := bufio.NewReader(in).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
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
// segment, the v2 namespace is always the second. Errors when the ref
// has fewer than two path segments.
func extractV2Namespace(internalRef string) (string, error) {
	parts := strings.Split(internalRef, "/")
	if len(parts) < 2 || parts[1] == "" {
		return "", fmt.Errorf("cannot derive v2 namespace from internal_ref %q (expected <registry>/<namespace>/skills/<name>)", internalRef)
	}
	return parts[1], nil
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
