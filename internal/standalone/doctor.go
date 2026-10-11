package standalone

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// doctorCommand answers "is this tool actually able to run an operation, and if not, what exactly is
// missing" — in one call, as a document, without printing anything secret and without mutating
// anything.
//
// It is the command an agent should run first after configuration, and it is deliberately
// NON-FATAL: an unconfigured or half-configured machine gets a full report of what is and is not
// ready rather than a refusal at the first missing piece, because the second missing piece is the
// one that would otherwise cost another round trip to discover.
func doctorCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:       "doctor",
		Summary:    "Diagnose this tool's local configuration: profiles, credentials and endpoint policy.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Description: `Reports, for every configured profile, whether it is valid, whether its credential
reference resolves, and whether its endpoint is permitted by the shared provider-endpoint policy.

It never prints credential material — every credential answer is a boolean plus the name of the
variable that would supply it. With --probe it additionally performs one READ against the selected
profile's Odoo to prove the credential actually works; without it, nothing is contacted.`,
		Flags: []clicore.Flag{
			profileFlag,
			{Name: "probe", Kind: clicore.KindBool, Description: "Additionally contact the SELECTED profile's Odoo with one authenticated read, to prove the credential works end to end. Mutates nothing."},
		},
		Examples: []string{
			"aerp doctor --output json",
			"aerp doctor --profile lab-a --probe --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			report := map[string]interface{}{
				"program": program,
				"product": "STANDALONE_AERP",
				"trust":   TrustBoundary(),
				"catalog": map[string]interface{}{
					"operationCount": len(operationIDs()),
					"source":         "the canonical ASAO AERP catalog, compiled into this binary — there is no second catalog",
					"provenance":     direct.CatalogProvenance(),
					"readiness":      direct.CatalogProvenance().Readiness,
				},
				"endpointPolicy": map[string]interface{}{
					"variable":                     providerendpoint.AllowPrivateEnvVar,
					"privateEndpointsAllowed":      providerendpoint.Allowed(),
					"appliesEquallyToBothProducts": true,
				},
			}

			store, loadErr := localprofile.Load()
			if loadErr != nil {
				// Not having configured anything yet is a STATE, and doctor's whole job is to report
				// states. It says what is missing and how to fix it, and still exits 0.
				report["profileFile"] = map[string]interface{}{
					"status":      "ABSENT",
					"detail":      loadErr.Error(),
					"remediation": "Create a profile with `" + program + " profile set --name <name> --endpoint <url> --database <db> --login <user>`.",
					"variable":    localprofile.FileVariable,
				}
				report["ready"] = false
				return report, nil
			}

			profiles := []interface{}{}
			for _, name := range store.Names() {
				profile := store.Profiles[name]
				entry := describeProfile(profile)
				entry["endpointPermitted"] = providerendpoint.CheckHost(ctx, profile.Endpoint) == nil
				profiles = append(profiles, entry)
			}
			report["profileFile"] = map[string]interface{}{
				"status":         "PRESENT",
				"path":           store.Path(),
				"defaultProfile": store.Default,
				"variable":       localprofile.FileVariable,
			}
			report["profiles"] = profiles
			report["profileCount"] = len(profiles)

			selection, selectErr := store.Resolve(invocation.String("profile"))
			if selectErr != nil {
				report["selection"] = map[string]interface{}{
					"status": "UNRESOLVED",
					"detail": selectErr.Error(),
					"precedence": []string{
						"--profile", localprofile.SelectorVariable, "the file's defaultProfile",
						"the sole profile, only when there is exactly one",
					},
				}
				report["ready"] = false
				return report, nil
			}
			selected := map[string]interface{}{
				"status": "RESOLVED", "profile": selection.Profile.Name, "selectedBy": selection.Source,
			}
			report["selection"] = selected

			ready := true
			if _, source, err := localprofile.ResolveCredentialWithSource(selection.Profile); err != nil {
				selected["credentialStatus"] = "UNAVAILABLE"
				selected["credentialDetail"] = err.Error()
				selected["credentialVariable"] = localprofile.EnvVariableFor(selection.Profile.CredentialRef)
				selected["credentialResolves"] = false
				selected["credentialSource"] = ""
				ready = false
			} else {
				selected["credentialStatus"] = "RESOLVED"
				selected["credentialResolves"] = true
				selected["credentialSource"] = source
			}
			if err := providerendpoint.CheckHost(ctx, selection.Profile.Endpoint); err != nil {
				selected["endpointStatus"] = "REFUSED_BY_POLICY"
				selected["endpointDetail"] = err.Error()
				selected["endpointRemediation"] = "Set " + providerendpoint.AllowPrivateEnvVar + "=true if this is a local or private-network Odoo you intend to reach."
				ready = false
			} else {
				selected["endpointStatus"] = "PERMITTED"
			}

			probe, err := invocation.Bool("probe")
			if err != nil {
				return nil, err
			}
			if probe && ready {
				selected["probe"] = probeSelected(ctx, selection)
				if status, _ := selected["probe"].(map[string]interface{})["status"].(string); !strings.HasPrefix(status, "OK") {
					ready = false
				}
			} else if probe {
				selected["probe"] = map[string]interface{}{
					"status": "SKIPPED",
					"detail": "the profile is not ready, so nothing was contacted — fix what is reported above first",
				}
			}
			report["ready"] = ready
			return report, nil
		},
	})
}

// probeSelected performs ONE read against the selected Odoo, through the canonical execution layer,
// to turn "the credential resolves" into "the credential works".
//
// THE OPERATION IS CHOSEN FROM THE CATALOG, never hard-coded — a probe naming an operation literally
// would be a second, tiny catalog, and it would break the day that operation is renamed.
//
// WHAT COUNTS AS SUCCESS, AND WHY IT IS NOT "the operation succeeded". The catalog spans the whole
// Odoo feature surface, and a real Odoo installs only the modules its owner needs; a probe operation
// whose model belongs to an uninstalled module will be REFUSED BY THE PROVIDER even though the
// credential is perfect. Reading that as a credential failure would send an operator hunting a
// password problem they do not have.
//
// So the question this probe actually answers is the one it can answer honestly: did this profile's
// credential get us far enough that ODOO ITSELF ANSWERED? An error class of ACCESS_DENIED or
// PROVIDER_UNREACHABLE means no. Anything else — including "invalid field on that model" — means the
// login succeeded and the provider processed the request, which is exactly what a credential check
// is for, and the report says so in a distinct status rather than pretending the read worked.
func probeSelected(ctx context.Context, selection localprofile.Selection) map[string]interface{} {
	operation, ok := firstTargetlessRead()
	if !ok {
		return map[string]interface{}{"status": "UNAVAILABLE", "detail": "the catalog defines no target-free read operation to probe with"}
	}
	material, err := localprofile.ResolveCredential(selection.Profile)
	if err != nil {
		return map[string]interface{}{"status": "FAILED", "detail": err.Error()}
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
		return map[string]interface{}{"status": "FAILED", "detail": err.Error()}
	}
	capability := capabilityProbe(ctx, executor)
	outcome, err := executor.Execute(ctx, direct.Request{Operation: operation, Limit: 1})
	if err == nil {
		return map[string]interface{}{
			"status": "OK", "operation": operation, "providerStatus": outcome.Status,
			"detail":          "one catalog READ succeeded against this profile's odoo; the credential works",
			"capabilityProbe": capability,
		}
	}
	var operationErr *direct.OperationError
	if !errors.As(err, &operationErr) {
		return map[string]interface{}{"status": "FAILED", "operation": operation, "detail": err.Error(), "capabilityProbe": capability}
	}
	switch operationErr.Class {
	case "ACCESS_DENIED":
		return map[string]interface{}{
			"status": "FAILED", "operation": operation, "class": operationErr.Class, "detail": operationErr.Message,
			"remediation":     "The odoo refused this profile's credential. Check the material behind its credentialRef.",
			"capabilityProbe": capability,
		}
	case "PROVIDER_UNREACHABLE":
		return map[string]interface{}{
			"status": "FAILED", "operation": operation, "class": operationErr.Class, "detail": operationErr.Message,
			"remediation":     "The odoo did not accept a connection. Check that it is running and that this profile's endpoint is correct; the request was never delivered.",
			"capabilityProbe": capability,
		}
	case "PROVIDER_VERSION_UNSUPPORTED":
		// Refused before login: the credential was never sent, so this says nothing about it
		// and must not fall through to OK_CREDENTIAL_ACCEPTED.
		return map[string]interface{}{
			"status": "FAILED", "operation": operation, "class": operationErr.Class, "detail": operationErr.Message,
			"providerVersion": operationErr.Detail["provider_version"],
			"providerSeries":  operationErr.Detail["provider_series"],
			"supportedSeries": operationErr.Detail["supported_series"],
			"remediation":     "This profile's odoo runs a series this build has no catalog for. Nothing was executed and the credential was not sent, so it is not verified either. Point the profile at a supported odoo.",
			"capabilityProbe": capability,
		}
	default:
		return map[string]interface{}{
			"status": "OK_CREDENTIAL_ACCEPTED", "operation": operation, "class": operationErr.Class,
			"detail":          operationErr.Message,
			"capabilityProbe": capability,
			"note": "The odoo authenticated this profile and answered — the credential works. It refused this " +
				"particular probe operation, which usually means the odoo module that operation's model " +
				"belongs to is not installed there. That is a fact about that odoo's configuration, not " +
				"about this profile.",
		}
	}
}

func capabilityProbe(ctx context.Context, executor *direct.Executor) map[string]interface{} {
	inventory, err := executor.ModuleInventory(ctx)
	if err != nil {
		class := "CAPABILITY_PROBE_FAILED"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			class = "PROVIDER_UNREACHABLE"
		}
		failed := map[string]interface{}{
			"status": "FAILED",
			"class":  class,
			"detail": "the Odoo module inventory could not be read; the credential probe is reported separately",
		}
		var unsupported *odoo.ProviderVersionUnsupportedError
		if errors.As(err, &unsupported) {
			failed["class"] = unsupported.ErrorClass()
			failed["providerVersion"] = unsupported.ProviderVersion
			failed["detail"] = "the Odoo series is not supported by this build, so its module inventory was not read"
		}
		return failed
	}

	available := 0
	for _, operation := range direct.Operations() {
		availability, availabilityErr := odoo.AvailabilityForOperation(operation.ID, inventory)
		if availabilityErr == nil && availability.Available {
			available++
		}
	}
	return map[string]interface{}{
		"status":                    "OK",
		"providerVersion":           inventory.ProviderVersion,
		"installedModules":          inventory.Installed,
		"catalogOperationCount":     len(direct.Operations()),
		"availableOperationCount":   available,
		"unavailableOperationCount": len(direct.Operations()) - available,
		"providerFit":               providerFitReport(ctx, executor),
	}
}

// providerFitReport is what the provider-fit check observes about this Odoo, and its verdict on
// every catalog operation (DEC-2026-0157): OK, UNVERIFIED_HERE (it changes records where this Odoo
// differs from the verified environment; it runs only with --allow-unverified), or REFUSED (its
// modules are not installed). Reasons are counted by the operations they hold back.
func providerFitReport(ctx context.Context, executor *direct.Executor) map[string]interface{} {
	fit, verdicts, err := executor.ProviderFit(ctx)
	if err != nil {
		return map[string]interface{}{"status": "FAILED", "detail": err.Error()}
	}
	counts := map[string]int{odoo.FitOK: 0, odoo.FitUnverifiedHere: 0, odoo.FitRefused: 0}
	held := map[string]int{}
	for _, verdict := range verdicts {
		counts[verdict.Verdict]++
		for _, reason := range verdict.Reasons {
			held[reason]++
		}
	}
	reasons := make([]map[string]interface{}, 0, len(held))
	for reason, operations := range held {
		reasons = append(reasons, map[string]interface{}{"reason": reason, "operations": operations})
	}
	sort.Slice(reasons, func(i, j int) bool {
		a, b := reasons[i]["operations"].(int), reasons[j]["operations"].(int)
		if a != b {
			return a > b
		}
		return reasons[i]["reason"].(string) < reasons[j]["reason"].(string)
	})
	changed := map[string]odoo.ModelFit{}
	for model, mf := range fit.Models {
		if len(mf.ExtendedBy)+len(mf.Automations)+len(mf.ManualFields)+len(mf.Unreadable) > 0 {
			changed[model] = mf
		}
	}
	report := map[string]interface{}{
		"status":               "OK",
		"series":               fit.ProviderSeries,
		"edition":              fit.Edition,
		"hosting":              fit.Hosting,
		"standardModules":      fit.StandardModules,
		"enterpriseModules":    fit.EnterpriseModules,
		"thirdPartyModules":    fit.ThirdPartyModules,
		"automationsInstalled": fit.Automations,
		"changedModels":        changed,
		"operations": map[string]interface{}{
			"ok": counts[odoo.FitOK], "unverifiedHere": counts[odoo.FitUnverifiedHere], "refused": counts[odoo.FitRefused],
		},
		"reasons": reasons,
	}
	// An Odoo that is not the stock image carries ASAO's notice: what it cannot verify or guarantee.
	if fit.Edition != odoo.EditionCommunity || len(fit.ThirdPartyModules) > 0 || fit.ModulesUnreadable || counts[odoo.FitUnverifiedHere] > 0 {
		report["notice"] = odoo.CustomisedOdooNotice
	}
	return report
}
