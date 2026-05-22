package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/salaboy/skills-oci/pkg/catalog"
	"github.com/salaboy/skills-oci/pkg/config"
	"github.com/salaboy/skills-oci/pkg/oci"
	"github.com/salaboy/skills-oci/pkg/scm"
	"github.com/salaboy/skills-oci/pkg/skill"
	"github.com/salaboy/skills-oci/pkg/telemetry"
	"github.com/spf13/cobra"
)

// syncExitCode encodes the documented exit-code semantics of `catalog sync`:
// 0 = all entries synced or skipped, 1 = at least one failure, 2 = lockfile
// write failed (registry diverged from lockfile, manual reconciliation
// required). The data contract names exit 2 as the only state where the
// registry is ahead of the lockfile.
type syncExitCode int

const (
	syncExitOK        syncExitCode = 0
	syncExitEntryFail syncExitCode = 1
	syncExitLockFail  syncExitCode = 2
)

func newCatalogSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile catalog.json with the destination OCI registry",
		Long:  "Clones each entry's upstream at its pinned commit, pushes to the destination registry with provenance annotations, and writes catalog-lock.json atomically. See docs/skills-catalog-data-contract.md for the exit-code semantics and the canonical CI workflow.",
		RunE:  runCatalogSync,
	}
	cmd.Flags().Bool("dry-run", false, "Fetch and validate every entry but skip the push and the lockfile write")
	cmd.Flags().String("only", "", "Comma-separated list of catalog entry names to sync (default: all)")
	cmd.Flags().String("catalog", "catalog.json", "Path to catalog.json")
	cmd.Flags().String("lock", "catalog-lock.json", "Path to catalog-lock.json")
	cmd.Flags().Int("concurrency", 0, "Bounded parallel worker count (default from .skills-oci.yaml, else 4)")
	cmd.Flags().Bool("allow-missing-license", false, "Permit entries whose upstream SKILL.md has no license field")
	return cmd
}

// syncOpts is the resolved set of inputs for `catalog sync`. The Cobra
// layer parses flags + project config into this struct so the
// orchestration logic in runCatalogSyncWithDeps stays pure and testable.
type syncOpts struct {
	Plain               bool
	PlainHTTP           bool
	DryRun              bool
	Only                []string
	CatalogPath         string
	LockPath            string
	Concurrency         int
	AllowMissingLicense bool

	// Now is injectable so tests can pin the lockfile's generated_at
	// timestamp. nil means catalog.Sync uses time.Now().UTC().
	Now func() time.Time
}

func runCatalogSync(cmd *cobra.Command, _ []string) error {
	cfg := configFromContext(cmd.Context())
	opts := parseSyncOpts(cmd, cfg)
	emitter := EmitterFromContext(cmd.Context())
	cliVersion := CLIVersionFromContext(cmd.Context())

	code, err := runCatalogSyncWithDeps(
		cmd.Context(),
		cmd.OutOrStdout(),
		opts,
		scmFetcherAdapter{},
		skillLicenseReader{},
		ociPusherAdapter{},
		emitter,
		cliVersion,
	)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "catalog sync:", err)
	}
	if code != syncExitOK {
		os.Exit(int(code))
	}
	return nil
}

// parseSyncOpts resolves flag + project-config inputs into a syncOpts
// value. Pure: no IO, no network. The precedence chain for concurrency
// and allow-missing-license is: explicit flag > project config > built-in
// default (concurrency = 4; allow-missing-license = false).
func parseSyncOpts(cmd *cobra.Command, cfg config.Config) syncOpts {
	plain, _ := cmd.Flags().GetBool("plain")
	plainHTTP, _ := cmd.Flags().GetBool("plain-http")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	only, _ := cmd.Flags().GetString("only")
	catalogPath, _ := cmd.Flags().GetString("catalog")
	lockPath, _ := cmd.Flags().GetString("lock")
	concurrencyFlag, _ := cmd.Flags().GetInt("concurrency")
	allowFlag, _ := cmd.Flags().GetBool("allow-missing-license")

	concurrency := concurrencyFlag
	if concurrency <= 0 {
		concurrency = cfg.Catalog.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	allowMissingLicense := allowFlag || cfg.Catalog.AllowMissingLicense

	return syncOpts{
		Plain:               plain,
		PlainHTTP:           plainHTTP,
		DryRun:              dryRun,
		Only:                splitOnly(only),
		CatalogPath:         catalogPath,
		LockPath:            lockPath,
		Concurrency:         concurrency,
		AllowMissingLicense: allowMissingLicense,
	}
}

// runCatalogSyncWithDeps is the testable core. Production wraps it with
// real adapters and translates the returned exit code into os.Exit; tests
// inject fakes and assert directly on the (code, err) tuple.
//
// Returns:
//   - (syncExitOK, nil) when all entries synced or were skipped.
//   - (syncExitEntryFail, nil) when one or more entries failed.
//   - (syncExitEntryFail, err) on a setup failure (catalog load/validate).
//   - (syncExitLockFail, err) when the lockfile write itself failed —
//     the registry is ahead of the lockfile and manual reconciliation is
//     required (see docs/skills-catalog-data-contract.md).
//
// The progress summary is always written to out, including on error.
func runCatalogSyncWithDeps(
	ctx context.Context,
	out io.Writer,
	opts syncOpts,
	fet catalog.Fetcher,
	lic catalog.LicenseReader,
	push catalog.Pusher,
	emitter *telemetry.Emitter,
	cliVersion string,
) (syncExitCode, error) {
	onEntry := plainProgressWriter(out)
	onTelemetry := makeCatalogTelemetryCallback(emitter, cliVersion, trigger(opts.Plain))

	res, err := catalog.Sync(ctx, catalog.Opts{
		CatalogPath:         opts.CatalogPath,
		LockPath:            opts.LockPath,
		Concurrency:         opts.Concurrency,
		AllowMissingLicense: opts.AllowMissingLicense,
		DryRun:              opts.DryRun,
		Only:                opts.Only,
		PlainHTTP:           opts.PlainHTTP,
		EntryAnnotations:    sourceAnnotation,
		OnEntry:             onEntry,
		OnTelemetry:         onTelemetry,
		Now:                 opts.Now,
	}, fet, lic, push)

	if err != nil {
		// Lockfile-write failure ⇒ exit 2 per the contract; everything
		// else (catalog load/validate failure) ⇒ exit 1.
		exit := syncExitEntryFail
		if strings.Contains(err.Error(), "writing") && strings.Contains(err.Error(), opts.LockPath) {
			exit = syncExitLockFail
		}
		writeSyncSummary(out, res)
		return exit, err
	}

	writeSyncSummary(out, res)
	if res.AnyFailed() {
		return syncExitEntryFail, nil
	}
	return syncExitOK, nil
}

// plainProgressWriter renders the spec-committed --plain status format:
//
//	[i/N] <name> cloning <repo>@<commit-short>
//	[i/N] <name> pushing <internal_ref>:<version>
//	[i/N] <name> ok <digest-short>
//	[i/N] <name> skipped (already at <commit-short>)
//	[i/N] <name> failed: <error>
//
// The starting "catalog sync starting (N entries)" line and final
// summary line are emitted elsewhere (start: implicit on first
// invocation; summary: writeSyncSummary).
func plainProgressWriter(out io.Writer) func(s catalog.EntryStatus) {
	var (
		startOnce sync.Once
		mu        sync.Mutex // serialize line writes so messages from concurrent workers stay readable
	)
	return func(s catalog.EntryStatus) {
		startOnce.Do(func() {
			fmt.Fprintf(out, "catalog sync starting (%d entries)\n", s.Total)
		})
		mu.Lock()
		defer mu.Unlock()
		prefix := fmt.Sprintf("[%d/%d] %s", s.Index, s.Total, s.Name)
		switch s.Stage {
		case "queued":
			// Don't print queued — it would be noise. The first real line
			// for an entry is "cloning" or "skipped".
			return
		case "cloning":
			fmt.Fprintf(out, "%s cloning @%s\n", prefix, shortSHA(s.Commit))
		case "pushing":
			fmt.Fprintf(out, "%s pushing\n", prefix)
		case "done":
			detail := s.Detail
			if detail == "" {
				detail = shortSHA(s.Digest)
			}
			fmt.Fprintf(out, "%s ok %s\n", prefix, detail)
		case "skipped":
			fmt.Fprintf(out, "%s %s\n", prefix, s.Detail)
		case "failed":
			fmt.Fprintf(out, "%s failed: %v\n", prefix, s.Err)
		}
	}
}

func writeSyncSummary(out io.Writer, res catalog.Result) {
	synced, failed, skipped := res.Counts()
	fmt.Fprintf(out, "catalog sync done: synced=%d skipped=%d failed=%d\n", synced, skipped, failed)
}

func splitOnly(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func trigger(_ bool) string {
	// catalog sync is a manifest-driven workflow whether invoked from CI
	// or a developer machine — the catalog file drives every push.
	return "manifest"
}

func shortSHA(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

// sourceAnnotation builds the canonical org.opencontainers.image.source
// URL pointing at the upstream commit. Exposed as a package-level
// function so tests can swap or assert on it.
func sourceAnnotation(e catalog.Entry) map[string]string {
	return map[string]string{
		"org.opencontainers.image.source": fmt.Sprintf("https://github.com/%s/tree/%s/%s", e.Repo, e.Commit, e.Subpath),
	}
}

// scmFetcherAdapter wraps pkg/scm.Fetch in the catalog.Fetcher interface.
type scmFetcherAdapter struct{}

func (scmFetcherAdapter) Fetch(ctx context.Context, owner, repo, subpath, commit, dst string) error {
	return scm.Fetch(ctx, scm.SourceRef{Owner: owner, Repo: repo, Subpath: subpath, Commit: commit}, dst)
}

// skillLicenseReader reads SKILL.md frontmatter via pkg/skill.Parse.
type skillLicenseReader struct{}

func (skillLicenseReader) ReadLicense(skillDir string) (string, error) {
	sd, err := skill.Parse(skillDir)
	if err != nil {
		return "", err
	}
	return sd.Config.License, nil
}

// ociPusherAdapter wraps pkg/oci.Push in the catalog.Pusher interface.
type ociPusherAdapter struct{}

func (ociPusherAdapter) Push(ctx context.Context, in catalog.PushInput) (string, error) {
	res, err := oci.Push(ctx, oci.PushOptions{
		Reference:        in.InternalRef,
		Tag:              in.Tag,
		SkillDir:         in.SkillDir,
		PlainHTTP:        in.PlainHTTP,
		ExtraAnnotations: in.ExtraAnnotations,
	})
	if err != nil {
		return "", err
	}
	return res.Digest, nil
}

// makeCatalogTelemetryCallback builds the per-entry telemetry sink that
// fires one catalog.synced event per result. Returns a no-op when the
// emitter is nil (telemetry disabled or context missing). The emitter
// itself is nil-safe.
func makeCatalogTelemetryCallback(emitter *telemetry.Emitter, cliVersion, trigger string) func(catalog.EntryResult) {
	return func(r catalog.EntryResult) {
		emitter.EmitCatalogSynced(telemetry.CatalogSyncedInput{
			CLIVersion:   cliVersion,
			Name:         r.Name,
			InternalRef:  r.InternalRef,
			Tag:          r.Tag,
			Commit:       r.Commit,
			Digest:       r.Digest,
			UpstreamRepo: r.UpstreamRepo,
			Outcome:      string(r.Outcome),
			Trigger:      trigger,
		})
	}
}
