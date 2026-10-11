package standalone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/aerpsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

func callCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "call",
		Summary:    "Execute an unmediated JSON-RPC call against Odoo.",
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Description: `Executes a low-level Odoo JSON-RPC execute_kw call directly against the configured Odoo.

This is an unmediated escape hatch outside the canonical semantic catalog. It carries raw: true
in its outcome and makes no claim of semantic catalog readiness or conformance evidence.

Allowed read methods without --allow-mutation: search, search_read, read, search_count, fields_get, name_search.
All other methods require --allow-mutation. Methods beginning with _ are always refused.`,
		Flags: []clicore.Flag{
			profileFlag,
			{
				Name:        "model",
				Kind:        clicore.KindString,
				Required:    true,
				Description: "Odoo model name (e.g. res.partner).",
			},
			{
				Name:        "method",
				Kind:        clicore.KindString,
				Required:    true,
				Description: "Odoo RPC method name (e.g. search_read).",
			},
			{
				Name:        "args-json",
				Kind:        clicore.KindJSON,
				Description: "JSON array of positional arguments for execute_kw. Defaults to [] when omitted.",
			},
			{
				Name:        "kwargs-json",
				Kind:        clicore.KindJSON,
				Description: "JSON object of keyword arguments for execute_kw. Defaults to {} when omitted.",
			},
			{
				Name:        "allow-mutation",
				Kind:        clicore.KindBool,
				Description: "Explicitly allow mutating RPC methods outside the read-only allowlist.",
			},
		},
		Examples: []string{
			config.name + ` call --profile lab-a --model res.partner --method search_read --args-json '[[["name","=","Acme"]]]' --kwargs-json '{"fields":["name"],"limit":5}' --output json`,
			config.name + ` call --profile lab-a --model res.partner --method write --args-json '[[42], {"name": "New Name"}]' --allow-mutation --output json`,
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runCall(ctx, invocation, config)
		},
	})
}

func runCall(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	model := strings.TrimSpace(invocation.String("model"))
	if model == "" {
		return nil, clicore.UsageError("MISSING_REQUIRED_FLAG", "--model is required").
			WithContextValue("flag", "model")
	}

	method := strings.TrimSpace(invocation.String("method"))
	if method == "" {
		return nil, clicore.UsageError("MISSING_REQUIRED_FLAG", "--method is required").
			WithContextValue("flag", "method")
	}
	if strings.HasPrefix(method, "_") {
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", "methods beginning with _ are disallowed: %q", method).
			WithContextValue("flag", "method").
			WithContextValue("method", method)
	}

	allowMutation, err := invocation.Bool("allow-mutation")
	if err != nil {
		return nil, err
	}
	if !direct.IsReadOnlyMethod(method) && !allowMutation {
		return nil, clicore.NewError(clicore.CategoryConflict, "RAW_MUTATION_NOT_ALLOWED", fmt.Sprintf("method %q is not a read-only method and requires --allow-mutation", method)).
			WithContextValue("method", method).
			WithRemediation("Pass --allow-mutation to permit mutating RPC calls.")
	}

	args := []interface{}{}
	if invocation.Has("args-json") {
		rawArgs := strings.TrimSpace(invocation.String("args-json"))
		if rawArgs == "" {
			return nil, clicore.UsageError("INVALID_JSON", "--args-json was provided but is empty").
				WithContextValue("flag", "args-json").
				WithRemediation("Pass a valid JSON array or omit the flag.")
		}
		if err := invocation.JSON("args-json", &args); err != nil {
			return nil, err
		}
	}

	kwargs := map[string]interface{}{}
	if invocation.Has("kwargs-json") {
		rawKwargs := strings.TrimSpace(invocation.String("kwargs-json"))
		if rawKwargs == "" {
			return nil, clicore.UsageError("INVALID_JSON", "--kwargs-json was provided but is empty").
				WithContextValue("flag", "kwargs-json").
				WithRemediation("Pass a valid JSON object or omit the flag.")
		}
		if err := invocation.JSON("kwargs-json", &kwargs); err != nil {
			return nil, err
		}
	}

	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	material, err := localprofile.ResolveCredential(selection.Profile)
	if err != nil {
		return nil, classifyProfile(err, selection.Profile.Name, config.name)
	}
	executor, err := direct.NewExecutor(ctx, direct.Target{
		ProfileName: selection.Profile.Name,
		Endpoint:    selection.Profile.Endpoint,
		Database:    selection.Profile.Database,
		Login:       material.Login,
		Password:    material.Password,
		Protocol:    selection.Profile.Protocol,
	}, providerendpoint.CheckHost)
	if err != nil {
		return nil, classifyComposition(err, selection.Profile.Name, config.name)
	}

	outcome, err := executor.Call(ctx, direct.RawRequest{
		Model:  model,
		Method: method,
		Args:   args,
		Kwargs: kwargs,
	}, allowMutation)
	if err != nil {
		return nil, classifyRawError(err, model, method, config.surface)
	}
	return outcome, nil
}

func classifyRawError(err error, model, method string, surface aerpsurface.Surface) error {
	if err == nil {
		return nil
	}
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	switch {
	case errors.Is(err, direct.ErrRawModelRequired):
		return clicore.UsageError("MISSING_REQUIRED_FLAG", "--model is required").WithContextValue("flag", "model")
	case errors.Is(err, direct.ErrRawMethodRequired):
		return clicore.UsageError("MISSING_REQUIRED_FLAG", "--method is required").WithContextValue("flag", "method")
	case errors.Is(err, direct.ErrRawMethodDisallowed):
		return clicore.UsageError("INVALID_FLAG_VALUE", "methods beginning with _ are disallowed: %q", method).
			WithContextValue("flag", "method").
			WithContextValue("method", method)
	case errors.Is(err, direct.ErrRawMutationRefused):
		return clicore.NewError(clicore.CategoryConflict, "RAW_MUTATION_NOT_ALLOWED", fmt.Sprintf("method %q is not a read-only method and requires --allow-mutation", method)).
			WithContextValue("method", method).
			WithRemediation("Pass --allow-mutation to permit mutating RPC calls.")
	}
	if mapped := surface.ClassifyProviderAnswer(err); mapped != nil {
		return mapped
	}
	return clicore.InternalError(err)
}
