package cmd

import (
	"context"

	"github.com/liatrio/skills-oci/pkg/oci"
	"github.com/liatrio/skills-oci/pkg/telemetry"
	"github.com/spf13/cobra"
)

const (
	defaultSkillsDir = ".agents/skills"
)

// additionalSkillsDirs lists the tool-specific directories that receive
// symlinks pointing back to the primary .agents/skills installation.
var additionalSkillsDirs = []string{
	".claude/skills",
	".codex/skills",
	".cursor/skills",
	".gemini/skills",
}

// telemetryEmitterKey is the context key for the per-process telemetry
// emitter. Subcommands fetch the emitter from cmd.Context() instead of
// constructing their own, which keeps emission ordering and Wait() correct.
type telemetryEmitterKey struct{}

// EmitterFromContext returns the telemetry emitter stored on ctx, or a nil
// pointer if none was attached. Callers must tolerate nil — both
// SkillEmitterAdapter and the underlying *telemetry.Emitter are safe to use
// nil-receiver-style.
func EmitterFromContext(ctx context.Context) *telemetry.Emitter {
	if v, ok := ctx.Value(telemetryEmitterKey{}).(*telemetry.Emitter); ok {
		return v
	}
	return nil
}

// SkillEmitterAdapter bridges between oci.SkillDownloadEmitter (the narrow
// callback shape pkg/oci expects) and *telemetry.Emitter (the orchestrator
// in pkg/telemetry). It is the only file in this codebase that knows both
// types — pkg/oci and pkg/telemetry remain mutually unaware.
type SkillEmitterAdapter struct {
	Emitter *telemetry.Emitter
}

// OnSkillDownloaded converts an oci.SkillDownloadInfo into a
// telemetry.SkillDownloadedInput and forwards it.
func (a *SkillEmitterAdapter) OnSkillDownloaded(info oci.SkillDownloadInfo) {
	if a == nil || a.Emitter == nil {
		return
	}
	a.Emitter.EmitSkillDownloaded(telemetry.SkillDownloadedInput{
		CLIVersion: info.CLIVersion,
		Namespace:  info.Namespace,
		Name:       info.Name,
		Version:    info.Version,
		Digest:     info.Digest,
		Registry:   info.Registry,
		OCIRef:     info.OCIRef,
		Command:    info.Command,
		Trigger:    info.Trigger,
	})
}

// NewRootCmd creates the root command for skills-oci.
//
// For the runtime entry point (main.go) prefer ExecuteWithWait, which guarantees
// the telemetry emitter is awaited even when the subcommand returns an error.
func NewRootCmd(version string) *cobra.Command {
	cmd, _ := newRootCmdWithEmitter(version)
	return cmd
}

// ExecuteWithWait runs the root command and then blocks until every in-flight
// telemetry emission has settled (success, buffered, or 2s timeout), so a
// quick subcommand never races process exit against a goroutine.
func ExecuteWithWait(version string) error {
	cmd, emitter := newRootCmdWithEmitter(version)
	defer emitter.Wait()
	return cmd.Execute()
}

func newRootCmdWithEmitter(version string) (*cobra.Command, *telemetry.Emitter) {
	emitter := telemetry.New(telemetry.LoadConfig())
	cliVersion := version

	cmd := &cobra.Command{
		Use:     "skills-oci",
		Short:   "Manage agent skills as OCI artifacts",
		Long:    "A CLI tool for packaging, pushing, and pulling agent skills as OCI artifacts following the Agent Skills OCI Artifacts Specification.",
		Version: version,
		PersistentPreRun: func(c *cobra.Command, _ []string) {
			c.SetContext(context.WithValue(c.Context(), telemetryEmitterKey{}, emitter))
			c.SetContext(context.WithValue(c.Context(), cliVersionKey{}, cliVersion))
		},
	}

	cmd.PersistentFlags().Bool("plain", false, "Disable interactive TUI (plain text output)")
	cmd.PersistentFlags().Bool("plain-http", false, "Use plain HTTP instead of HTTPS for registry connections")

	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newRemoveCmd())
	cmd.AddCommand(newCleanCmd())
	cmd.AddCommand(newInstallCmd())
	cmd.AddCommand(newVerifyCmd())
	cmd.AddCommand(newRegisterCmd())
	cmd.AddCommand(newCatalogCmd())
	cmd.AddCommand(newCollectionCmd())

	return cmd, emitter
}

// cliVersionKey is the context key for the CLI version string used to
// populate telemetry events' client.version field.
type cliVersionKey struct{}

// CLIVersionFromContext returns the CLI version stored on ctx, or "" if none
// is attached.
func CLIVersionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(cliVersionKey{}).(string); ok {
		return v
	}
	return ""
}
