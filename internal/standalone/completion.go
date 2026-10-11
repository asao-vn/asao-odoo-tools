package standalone

import (
	"context"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func completionGroup(root *clicore.Command) *clicore.Command {
	bash := local(&clicore.Command{
		Name:        "bash",
		Summary:     "Generate bash completion script.",
		Description: "Outputs a bash completion script for this CLI. Default text output is a raw pipeable script.",
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		Examples:    []string{root.Program + " completion bash", "source <(" + root.Program + " completion bash)"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return clicore.CompletionResult{
				Shell:  "bash",
				Script: clicore.GenerateBashCompletion(root),
			}, nil
		},
	})

	zsh := local(&clicore.Command{
		Name:        "zsh",
		Summary:     "Generate zsh completion script.",
		Description: "Outputs a zsh completion script for this CLI. Default text output is a raw pipeable script.",
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		Examples:    []string{root.Program + " completion zsh", "source <(" + root.Program + " completion zsh)"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return clicore.CompletionResult{
				Shell:  "zsh",
				Script: clicore.GenerateZshCompletion(root),
			}, nil
		},
	})

	fish := local(&clicore.Command{
		Name:        "fish",
		Summary:     "Generate fish completion script.",
		Description: "Outputs a fish completion script for this CLI. Default text output is a raw pipeable script.",
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		Examples:    []string{root.Program + " completion fish", root.Program + " completion fish | source"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return clicore.CompletionResult{
				Shell:  "fish",
				Script: clicore.GenerateFishCompletion(root),
			}, nil
		},
	})

	return &clicore.Command{
		Name:        "completion",
		Summary:     "Generate shell completion scripts (bash, zsh, fish).",
		Description: "Outputs completion scripts for bash, zsh, and fish. In default text mode, the output is raw and pipeable. In JSON mode, standard command envelope is returned. No credentials or profiles are accessed.",
		Children:    []*clicore.Command{bash, zsh, fish},
	}
}
