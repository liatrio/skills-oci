package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/salaboy/skills-oci/pkg/catalog"
	"github.com/salaboy/skills-oci/pkg/scm"
	"github.com/salaboy/skills-oci/pkg/skill"
	"github.com/spf13/cobra"
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
	DryRun      bool
}

// fetcher and resolver are minimal interfaces over the package-level
// functions in pkg/scm. Production code uses the real package functions;
// tests can supply doubles when they want to avoid go-git overhead.
type fetcher interface {
	Fetch(ctx context.Context, ref scm.SourceRef, dst string) error
}
type resolver interface {
	ResolveTag(ctx context.Context, repo, tag string) (string, error)
}

type realFetcher struct{}

func (realFetcher) Fetch(ctx context.Context, ref scm.SourceRef, dst string) error {
	return scm.Fetch(ctx, ref, dst)
}

type realResolver struct{}

func (realResolver) ResolveTag(ctx context.Context, repo, tag string) (string, error) {
	return scm.ResolveTag(ctx, repo, tag)
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

	// Step 3: resolve tag → commit SHA. Passthrough for 40-hex inputs.
	fmt.Fprintf(out, "resolving %s/%s@%s\n", owner, repo, version)
	commit, err := res.ResolveTag(ctx, owner+"/"+repo, version)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  → commit %s\n", commit)

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

	// Step 6: load existing catalog.json (or zero-value if absent).
	cur, err := loadCatalogFile(o.CatalogPath)
	if err != nil {
		return err
	}

	// Step 7: append entry via the pure AddEntry helper.
	entry := catalog.Entry{
		Name:        name,
		Repo:        owner + "/" + repo,
		Subpath:     subpath,
		Version:     version,
		Commit:      commit,
		InternalRef: internalRef,
	}
	next, err := catalog.AddEntry(cur, entry)
	if err != nil {
		return err
	}

	// Step 8: --dry-run short-circuit.
	if o.DryRun {
		body, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Fprintf(out, "would add entry:\n%s\n", body)
		return nil
	}

	// Step 9: atomic write.
	if err := catalog.WriteCatalogAtomic(o.CatalogPath, next); err != nil {
		return err
	}
	fmt.Fprintf(out, "catalog add: appended entry %q to %s\n", name, o.CatalogPath)
	return nil
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
// exist a zero-value v1 Catalog is returned so the first `catalog add`
// in a repo bootstraps cleanly.
func loadCatalogFile(path string) (catalog.Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog.Catalog{SchemaVersion: 1}, nil
		}
		return catalog.Catalog{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return catalog.Load(data)
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
