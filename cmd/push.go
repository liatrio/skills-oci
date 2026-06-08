package cmd

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/liatrio/skills-oci/pkg/oci"
	"github.com/liatrio/skills-oci/pkg/tui/push"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [OPTIONS] NAME[:TAG] PATH",
		Short: "Package and push a skill to an OCI registry",
		Long:  "Validates a skill directory, packages it as an OCI artifact, and pushes it to a remote container registry.",
		Example: `  # Push the skill in the current directory
  skills-oci push ghcr.io/myorg/skills/my-skill:1.0.0 .

  # Push a skill from a specific directory
  skills-oci push ghcr.io/myorg/skills/my-skill:1.0.0 ./my-skill

  # Push to a local registry (plain HTTP)
  skills-oci push localhost:5000/my-skill:1.0.0 . --plain-http

  # Stamp a provenance annotation onto the manifest (repeatable)
  skills-oci push ghcr.io/myorg/skills/my-skill:1.0.0 . --annotation org.opencontainers.image.revision=<sha>`,
		Args: cobra.ExactArgs(2),
		RunE: runPush,
	}

	cmd.Flags().StringArray("annotation", nil, "Manifest annotation as key=value (repeatable)")

	return cmd
}

// parseAnnotations converts repeatable --annotation key=value pairs into a map.
// It splits on the first '=' only, so annotation values may themselves contain
// '='. Pairs without a '=' or with an empty key are rejected. A later duplicate
// key overrides an earlier one (last-wins), matching how pkg/oci overlays
// caller-supplied annotation keys. Empty input yields a nil map (a no-op for
// oci.PushOptions.Annotations).
func parseAnnotations(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		key, value, found := strings.Cut(p, "=")
		if !found || key == "" {
			return nil, fmt.Errorf("invalid --annotation %q: want key=value", p)
		}
		out[key] = value
	}
	return out, nil
}

func runPush(cmd *cobra.Command, args []string) error {
	ref := args[0]
	path := args[1]
	plain, _ := cmd.Flags().GetBool("plain")
	plainHTTP, _ := cmd.Flags().GetBool("plain-http")
	annPairs, _ := cmd.Flags().GetStringArray("annotation")

	annotations, err := parseAnnotations(annPairs)
	if err != nil {
		return err
	}

	if plain {
		return runPushPlain(ref, path, plainHTTP, annotations)
	}

	m := push.NewModel(ref, path, plainHTTP, annotations)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Check if the final model has an error
	if fm, ok := finalModel.(push.Model); ok {
		if fm.Err() != nil {
			return fm.Err()
		}
	}

	return nil
}

func runPushPlain(ref, path string, plainHTTP bool, annotations map[string]string) error {
	result, err := oci.Push(context.Background(), oci.PushOptions{
		Reference:   ref,
		SkillDir:    path,
		PlainHTTP:   plainHTTP,
		Annotations: annotations,
		OnStatus: func(phase string) {
			fmt.Printf("  %s\n", phase)
		},
	})
	if err != nil {
		return err
	}

	fmt.Printf("\nSuccessfully pushed!\n")
	fmt.Printf("  Reference: %s:%s\n", result.Reference, result.Tag)
	fmt.Printf("  Digest:    %s\n", result.Digest)
	fmt.Printf("  Size:      %d bytes\n", result.Size)
	return nil
}
