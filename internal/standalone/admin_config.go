package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// AdminTargetInfo reports target instance metadata. The endpoint URL is deliberately omitted
// because profile endpoints may contain embedded userinfo/credentials.
type AdminTargetInfo struct {
	Profile  string `json:"profile"`
	Database string `json:"database"`
}

// ConfigDiffItem is a machine-readable entry describing a single planned or applied configuration item.
type ConfigDiffItem struct {
	Kind             string      `json:"kind"`                       // "user_preference"
	Target           string      `json:"target"`                     // user login (e.g. "admin")
	Current          interface{} `json:"current"`                    // current lang code or "not_found"
	Proposed         interface{} `json:"proposed"`                   // proposed lang code
	Action           string      `json:"action"`                     // "none" | "update" | "refused"
	Refused          bool        `json:"refused"`                    // true if user missing or language inactive
	Reason           string      `json:"reason,omitempty"`           // explanation when refused
	ProviderTargetID int64       `json:"providerTargetId,omitempty"` // Odoo res.users record ID
}

// ConfigPlanSummary aggregates plan counters.
type ConfigPlanSummary struct {
	TotalActions  int  `json:"totalActions"`
	UpdateCount   int  `json:"updateCount"`
	NoChangeCount int  `json:"noChangeCount"`
	RefusedCount  int  `json:"refusedCount"`
	InSync        bool `json:"inSync"`
}

// ConfigPlanResult is the machine-readable outcome of "odoo admin config plan".
type ConfigPlanResult struct {
	Target        AdminTargetInfo    `json:"target"`
	Manifest      ConfigManifestInfo `json:"manifest"`
	Diff          []ConfigDiffItem   `json:"diff"`
	Summary       ConfigPlanSummary  `json:"summary"`
	RequiresApply bool               `json:"requiresApply"`
}

// ConfigManifestInfo holds a non-caller-controlled metadata summary of the evaluated manifest.
// Caller-supplied free-text metadata (name, description) is deliberately omitted to prevent secret disclosure.
type ConfigManifestInfo struct {
	SchemaVersion string `json:"schemaVersion"`
	UserCount     int    `json:"userCount"`
}

var adminManifestFlags = []clicore.Flag{
	{
		Name:        "file",
		Kind:        clicore.KindString,
		Description: "Path to the configuration manifest JSON file.",
	},
	{
		Name:        "manifest",
		Kind:        clicore.KindString,
		Description: "Alias for --file: path to the configuration manifest JSON file.",
	},
	{
		Name:        "manifest-json",
		Kind:        clicore.KindJSON,
		Description: "Inline JSON string containing the configuration manifest document.",
	},
}

func adminGroup(config programConfig) *clicore.Command {
	return &clicore.Command{
		Name:    "admin",
		Summary: "Local-operator instance administration commands for Odoo.",
		Description: `Privileged administration commands for Odoo instances. Unlike imperative business operations,
these commands manage instance-level configuration declaratively.

TRUST BOUNDARY & GOVERNANCE:
This is an explicit local-operator command family. It executes directly under local operator authority
(authentication: NONE, scope: local profile). It does not authenticate an ASAO Actor, does not evaluate
ASAO CapabilityGrants, and writes no central audit trail. Operations execute directly against Odoo and
remain constrained solely by the server-side permissions of the Odoo user configured in the selected profile.
Module installation is not supported here; module lifecycle is governed exclusively through ASAO capability packs.`,
		Children: []*clicore.Command{
			configGroup(config),
		},
	}
}

func configGroup(config programConfig) *clicore.Command {
	return &clicore.Command{
		Name:    "config",
		Summary: "Declarative configuration-as-code management for Odoo user preferences.",
		Description: `Manage Odoo instance configuration declaratively using versioned JSON manifests.

Provides validate, plan, diff, status, and apply for user language preferences.
Removals, uninstalls, module installs, and business domain configurations (tax, accounting, company, etc.)
are strictly excluded. Module lifecycle is governed solely through ASAO capability packs ('asao odoo pack plan/apply').`,
		Children: []*clicore.Command{
			configValidateCommand(config),
			configPlanCommand(config),
			configDiffCommand(config),
			configStatusCommand(config),
			configApplyCommand(config),
		},
	}
}

func configValidateCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "validate",
		Summary:    "Validate an Odoo configuration manifest locally.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Description: `Validates a versioned declarative configuration manifest locally without contacting Odoo.

Checks JSON syntax, exact canonical field names, schemaVersion ("odoo.admin.config/v1"), duplicate user entries,
and user language preference formats. Recursively rejects credential fields anywhere in the document.
Rejects module installation and out-of-scope business domains (tax, accounting, company, products, etc.).`,
		Flags: adminManifestFlags,
		Examples: []string{
			config.name + " admin config validate --file instance.json --output json",
			config.name + ` admin config validate --manifest-json '{"schemaVersion":"odoo.admin.config/v1","users":[{"login":"admin","lang":"en_US"}]}' --output json`,
		},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			manifest, err := loadManifestFromInvocation(invocation)
			if err != nil {
				return nil, err
			}
			// Notice: Metadata free text is deliberately omitted to prevent echoing potential caller secrets.
			return map[string]interface{}{
				"valid":         true,
				"schemaVersion": manifest.SchemaVersion,
				"users":         manifest.Users,
				"summary": map[string]interface{}{
					"userCount": len(manifest.Users),
				},
			}, nil
		},
	})
}

func configPlanCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "plan",
		Summary:    "Plan user language preference changes to an Odoo instance from a declarative manifest.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Description: `Reads the selected Odoo instance and generates a machine-readable diff showing target database,
current values, proposed values, and planned actions.

Contact with Odoo is strictly read-only. Unknown users or inactive languages are reported as refused.
Module installation is not supported here and must be performed via ASAO capability packs.`,
		Flags: append([]clicore.Flag{profileFlag}, adminManifestFlags...),
		Examples: []string{
			config.name + " admin config plan --profile lab-a --file instance.json --output json",
			config.name + ` admin config plan --profile lab-a --manifest-json '{"schemaVersion":"odoo.admin.config/v1","users":[{"login":"admin","lang":"en_US"}]}' --output json`,
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			planResult, _, err := buildPlan(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			return planResult, nil
		},
	})
}

func configDiffCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "diff",
		Summary:    "Show declarative configuration diff against an Odoo instance.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Description: `Compares a declarative manifest against live Odoo state and outputs an explicit diff.
Mutates nothing.`,
		Flags: append([]clicore.Flag{profileFlag}, adminManifestFlags...),
		Examples: []string{
			config.name + " admin config diff --profile lab-a --file instance.json --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			planResult, _, err := buildPlan(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"target":  planResult.Target,
				"inSync":  planResult.Summary.InSync,
				"diff":    planResult.Diff,
				"summary": planResult.Summary,
			}, nil
		},
	})
}

func configStatusCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "status",
		Summary:    "Check sync and drift status of an Odoo instance against a declarative manifest.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Description: `Performs a read-only audit of the target Odoo instance against the configuration manifest,
reporting whether the instance is in-sync or drifted, with details on any drifted items.`,
		Flags: append([]clicore.Flag{profileFlag}, adminManifestFlags...),
		Examples: []string{
			config.name + " admin config status --profile lab-a --file instance.json --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			planResult, _, err := buildPlan(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			drifts := make([]ConfigDiffItem, 0)
			for _, item := range planResult.Diff {
				if item.Action != "none" {
					drifts = append(drifts, item)
				}
			}
			return map[string]interface{}{
				"target":        planResult.Target,
				"inSync":        planResult.Summary.InSync,
				"driftDetected": !planResult.Summary.InSync,
				"drifts":        drifts,
				"summary":       planResult.Summary,
			}, nil
		},
	})
}

func configApplyCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "apply",
		Summary:    "Apply declarative user language preferences to an Odoo instance.",
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Description: `Applies supported configuration changes to the selected Odoo instance.

Revalidates the manifest locally, rechecks current state for freshness, performs only supported user preference
updates, is idempotent, and verifies postconditions via readback. Never creates users, removes records,
or installs modules. Module lifecycle remains on the governed ASAO capability-pack route.`,
		Flags: append([]clicore.Flag{profileFlag}, adminManifestFlags...),
		Examples: []string{
			config.name + " admin config apply --profile lab-a --file instance.json --output json",
			config.name + ` admin config apply --profile lab-a --manifest-json '{"schemaVersion":"odoo.admin.config/v1","users":[{"login":"admin","lang":"en_US"}]}' --output json`,
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runConfigApply(ctx, invocation, config)
		},
	})
}

func loadManifestFromInvocation(invocation *clicore.Invocation) (*AdminManifest, error) {
	sources := 0
	if invocation.Has("file") {
		sources++
	}
	if invocation.Has("manifest") {
		sources++
	}
	if invocation.Has("manifest-json") {
		sources++
	}

	if sources == 0 {
		return nil, clicore.UsageError("MISSING_REQUIRED_FLAG",
			"exactly one manifest source is required: provide --file, --manifest, or --manifest-json").
			WithRemediation("Provide a path to a manifest JSON file with --file <path> (or --manifest <path>) or inline JSON with --manifest-json '<json>'.")
	}
	if sources > 1 {
		return nil, clicore.UsageError("MUTUALLY_EXCLUSIVE_FLAGS",
			"flags --file, --manifest, and --manifest-json are mutually exclusive; supply exactly one manifest source").
			WithRemediation("Provide only one of --file, --manifest, or --manifest-json.")
	}

	var data []byte
	var err error

	switch {
	case invocation.Has("file"):
		filePath := strings.TrimSpace(invocation.String("file"))
		if filePath == "" {
			return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--file cannot be blank").
				WithContextValue("flag", "file")
		}
		data, err = os.ReadFile(filePath)
		if err != nil {
			return nil, clicore.UsageError("FILE_NOT_FOUND", "failed to read manifest file %q: %s", filePath, err.Error()).
				WithContextValue("path", filePath)
		}
	case invocation.Has("manifest"):
		filePath := strings.TrimSpace(invocation.String("manifest"))
		if filePath == "" {
			return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--manifest cannot be blank").
				WithContextValue("flag", "manifest")
		}
		data, err = os.ReadFile(filePath)
		if err != nil {
			return nil, clicore.UsageError("FILE_NOT_FOUND", "failed to read manifest file %q: %s", filePath, err.Error()).
				WithContextValue("path", filePath)
		}
	case invocation.Has("manifest-json"):
		raw := strings.TrimSpace(invocation.String("manifest-json"))
		if raw == "" {
			return nil, clicore.UsageError("INVALID_JSON", "--manifest-json was provided but is empty").
				WithContextValue("flag", "manifest-json").
				WithRemediation("Pass a non-empty valid JSON string.")
		}
		data = []byte(raw)
	}

	return ParseAndValidateAdminManifest(data)
}

func buildPlan(ctx context.Context, invocation *clicore.Invocation, config programConfig) (*ConfigPlanResult, *direct.Executor, error) {
	manifest, err := loadManifestFromInvocation(invocation)
	if err != nil {
		return nil, nil, err
	}

	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, nil, err
	}

	targetInfo := AdminTargetInfo{
		Profile:  selection.Profile.Name,
		Database: selection.Profile.Database,
	}

	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, nil, err
	}

	diffItems := make([]ConfigDiffItem, 0, len(manifest.Users))

	// Inspect User Language Preferences
	if len(manifest.Users) > 0 {
		// Discover active languages.
		langOutcome, err := executor.Call(ctx, direct.RawRequest{
			Model:  "res.lang",
			Method: "search_read",
			Args: []interface{}{
				[]interface{}{},
			},
			Kwargs: map[string]interface{}{
				"fields": []string{"id", "code", "name", "active"},
			},
		}, false)
		if err != nil {
			return nil, nil, classifyRawError(err, "res.lang", "search_read", config.surface)
		}

		activeLangs := make(map[string]bool)
		if recs, ok := langOutcome.Data.([]interface{}); ok {
			for _, r := range recs {
				if rMap, ok := r.(map[string]interface{}); ok {
					code, _ := rMap["code"].(string)
					isActive := false
					switch a := rMap["active"].(type) {
					case bool:
						isActive = a
					case float64:
						isActive = a != 0
					case int:
						isActive = a != 0
					}
					if code != "" && isActive {
						activeLangs[code] = true
					}
				}
			}
		}

		// Discover user records.
		logins := make([]string, 0, len(manifest.Users))
		for _, u := range manifest.Users {
			logins = append(logins, u.Login)
		}

		userOutcome, err := executor.Call(ctx, direct.RawRequest{
			Model:  "res.users",
			Method: "search_read",
			Args: []interface{}{
				[]interface{}{[]interface{}{"login", "in", logins}},
			},
			Kwargs: map[string]interface{}{
				"fields": []string{"id", "login", "name", "lang"},
			},
		}, false)
		if err != nil {
			return nil, nil, classifyRawError(err, "res.users", "search_read", config.surface)
		}

		type userRec struct {
			id   int64
			lang string
		}
		foundUsers := make(map[string]userRec)
		if recs, ok := userOutcome.Data.([]interface{}); ok {
			for _, r := range recs {
				if rMap, ok := r.(map[string]interface{}); ok {
					login, _ := rMap["login"].(string)
					id := parseGenericID(rMap["id"])
					langStr := ""
					if l, ok := rMap["lang"].(string); ok {
						langStr = l
					}
					if login != "" {
						foundUsers[login] = userRec{id: id, lang: langStr}
					}
				}
			}
		}

		for _, pref := range manifest.Users {
			uRec, found := foundUsers[pref.Login]
			if !found {
				diffItems = append(diffItems, ConfigDiffItem{
					Kind:     "user_preference",
					Target:   pref.Login,
					Current:  "not_found",
					Proposed: pref.Lang,
					Action:   "refused",
					Refused:  true,
					Reason:   "user not found in Odoo res.users (V1 does not create users)",
				})
				continue
			}

			if !activeLangs[pref.Lang] {
				diffItems = append(diffItems, ConfigDiffItem{
					Kind:             "user_preference",
					Target:           pref.Login,
					Current:          uRec.lang,
					Proposed:         pref.Lang,
					Action:           "refused",
					Refused:          true,
					Reason:           "language is not active in Odoo res.lang",
					ProviderTargetID: uRec.id,
				})
				continue
			}

			if uRec.lang == pref.Lang {
				diffItems = append(diffItems, ConfigDiffItem{
					Kind:             "user_preference",
					Target:           pref.Login,
					Current:          uRec.lang,
					Proposed:         pref.Lang,
					Action:           "none",
					Refused:          false,
					ProviderTargetID: uRec.id,
				})
			} else {
				diffItems = append(diffItems, ConfigDiffItem{
					Kind:             "user_preference",
					Target:           pref.Login,
					Current:          uRec.lang,
					Proposed:         pref.Lang,
					Action:           "update",
					Refused:          false,
					ProviderTargetID: uRec.id,
				})
			}
		}
	}

	summary := ConfigPlanSummary{}
	for _, item := range diffItems {
		if item.Refused {
			summary.RefusedCount++
		}
		switch item.Action {
		case "update":
			summary.UpdateCount++
			summary.TotalActions++
		case "none":
			summary.NoChangeCount++
		}
	}
	summary.InSync = summary.TotalActions == 0 && summary.RefusedCount == 0
	requiresApply := summary.TotalActions > 0 && summary.RefusedCount == 0

	planResult := &ConfigPlanResult{
		Target: targetInfo,
		Manifest: ConfigManifestInfo{
			SchemaVersion: manifest.SchemaVersion,
			UserCount:     len(manifest.Users),
		},
		Diff:          diffItems,
		Summary:       summary,
		RequiresApply: requiresApply,
	}

	return planResult, executor, nil
}

func runConfigApply(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	// Re-evaluate freshness and state.
	planResult, executor, err := buildPlan(ctx, invocation, config)
	if err != nil {
		return nil, err
	}

	// Refuse apply if any item is blocked or invalid.
	if planResult.Summary.RefusedCount > 0 {
		return nil, clicore.NewError(clicore.CategoryConflict, "CONFIGURATION_REFUSED",
			fmt.Sprintf("cannot apply configuration: %d item(s) are refused by security gates or provider preconditions", planResult.Summary.RefusedCount)).
			WithContextValue("refusedCount", planResult.Summary.RefusedCount).
			WithRemediation("Run '" + config.name + " admin config plan' to review the refused items, and adjust the manifest or provider state.")
	}

	// Idempotent shortcut: no changes needed.
	if planResult.Summary.TotalActions == 0 {
		return map[string]interface{}{
			"target":                 planResult.Target,
			"status":                 "IN_SYNC",
			"changed":                false,
			"appliedActions":         []interface{}{},
			"postconditionsVerified": true,
			"summary": map[string]interface{}{
				"userPreferencesUpdated": 0,
				"unchanged":              planResult.Summary.NoChangeCount,
			},
		}, nil
	}

	appliedActions := make([]map[string]interface{}, 0, planResult.Summary.TotalActions)

	// Execute user language preference updates sequentially.
	for _, item := range planResult.Diff {
		if item.Action != "update" {
			continue
		}

		targetLang, _ := item.Proposed.(string)

		_, err := executor.Call(ctx, direct.RawRequest{
			Model:  "res.users",
			Method: "write",
			Args: []interface{}{
				[]int64{item.ProviderTargetID},
				map[string]interface{}{"lang": targetLang},
			},
		}, true)
		if err != nil {
			return nil, classifyRawError(err, "res.users", "write", config.surface)
		}

		// Postcondition readback verification on res.users.
		verifyOutcome, err := executor.Call(ctx, direct.RawRequest{
			Model:  "res.users",
			Method: "read",
			Args: []interface{}{
				[]int64{item.ProviderTargetID},
				[]string{"id", "login", "lang"},
			},
		}, false)
		if err != nil {
			return nil, classifyRawError(err, "res.users", "read", config.surface)
		}

		actualLang := ""
		if recs, ok := verifyOutcome.Data.([]interface{}); ok && len(recs) > 0 {
			if rMap, ok := recs[0].(map[string]interface{}); ok {
				if l, ok := rMap["lang"].(string); ok {
					actualLang = l
				}
			}
		}

		if actualLang != targetLang {
			return nil, clicore.NewError(clicore.CategoryProvider, "POSTCONDITION_VERIFICATION_FAILED",
				fmt.Sprintf("user %q language update written but readback observed %q (expected %q)", item.Target, actualLang, targetLang)).
				WithContextValue("login", item.Target).
				WithContextValue("observedLang", actualLang).
				WithContextValue("expectedLang", targetLang)
		}

		appliedActions = append(appliedActions, map[string]interface{}{
			"kind":                  "user_preference",
			"target":                item.Target,
			"from":                  item.Current,
			"to":                    targetLang,
			"action":                "update",
			"postconditionVerified": true,
		})
	}

	return map[string]interface{}{
		"target":                 planResult.Target,
		"status":                 "APPLIED",
		"changed":                len(appliedActions) > 0,
		"appliedActions":         appliedActions,
		"postconditionsVerified": true,
		"summary": map[string]interface{}{
			"userPreferencesUpdated": planResult.Summary.UpdateCount,
			"unchanged":              planResult.Summary.NoChangeCount,
		},
	}, nil
}

func parseGenericID(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
