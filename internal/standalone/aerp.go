package standalone

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/aerpsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// providerEndpointOptIn is the SHARED opt-in that permits a loopback/private provider endpoint. It
// is re-exported by name rather than re-spelled as a literal, so remediation text can never name a
// variable the policy no longer reads.
const providerEndpointOptIn = providerendpoint.AllowPrivateEnvVar

// THE CANONICAL AERP SURFACE, STANDALONE.
//
// Every command in this file is composition. The catalog it lists comes from direct, which
// projects packages/odoo.Catalog(); the flags come from the shared aerpsurface; the request comes from
// the shared BuildRequest; the execution comes from the canonical Executor. There is no operation
// name, no field name, no strategy and no mapping written anywhere in this file — which is what
// makes "remove an operation from the canonical catalog and this surface loses it too" a structural
// fact rather than a promise.

func catalogGroup(config programConfig) *clicore.Command {
	surface := config.surface
	children := []*clicore.Command{
		local(&clicore.Command{
			Name:       "list",
			Summary:    "List every catalog operation.",
			SideEffect: clicore.EffectNone,
			Authority:  localAuthority,
			Description: `Returns every operation with its kind, strategy, risk, target requirement, evidence
	readiness and historical evidence
	class. requiredCapability is published too, and reading it here needs care: it records which
capability GOVERNS this operation in the ASAO platform, and this product evaluates none of them. The
document says so in the same breath — see the "capabilityEnforcement" field.`,
			Flags: aerpsurface.CatalogListFlags(),
			Examples: []string{
				"aerp catalog list --output json",
				"aerp catalog list --domain people --output json",
				"aerp catalog list --evidence NEWER_UNVERIFIED --output json",
				"aerp catalog list --readiness UNVERIFIED --output json",
				"aerp catalog list --origin SURFACE --domain manufacturing --output json",
			},
			Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
				return surface.ListCatalog(invocation, capabilityEnforcementNotice(config.name))
			},
		}),
		local(&clicore.Command{
			Name:       "describe",
			Summary:    "Publish one operation's complete contract.",
			SideEffect: clicore.EffectNone,
			Authority:  localAuthority,
			Description: `Everything needed to call one operation correctly without reading source: every input
field by its caller-facing name, its type, whether it is required, its closed set of accepted values
when it has one, and — for a field that references another provider record — whether a human-facing
name may be supplied instead of an opaque id.`,
			Flags:    aerpsurface.CatalogDescribeFlags(),
			Examples: []string{"aerp catalog describe --operation people.employee.create --output json"},
			Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
				return surface.CatalogDescribe(invocation)
			},
		}),
	}
	if odooFlavour(config.name) {
		children = append(children, local(&clicore.Command{
			Name:       "fields",
			Summary:    "Discover live provider schema fields for an Odoo model.",
			SideEffect: clicore.EffectRead,
			Authority:  localAuthority,
			Description: `Contacts the selected Odoo to discover live model field metadata using fields_get.

This reports raw provider schema facts as discovery only. It does not alter the canonical catalog,
does not generate semantic operations or flags, and asserts no semantic readiness or evidence claim.
The returned schemaHash serves as a drift signal.`,
			Flags: []clicore.Flag{
				profileFlag,
				{
					Name:        "model",
					Kind:        clicore.KindString,
					Required:    true,
					Description: "Odoo model name (e.g. res.partner).",
				},
				{
					Name:        "attribute",
					Kind:        clicore.KindString,
					Repeatable:  true,
					Description: "Field attribute to discover (repeatable, or comma-separated). Defaults to string,type,required,readonly,relation,selection when omitted.",
				},
				{
					Name:        "attributes-json",
					Kind:        clicore.KindJSON,
					Description: "JSON array of field attributes to discover.",
				},
			},
			Examples: []string{
				"aerp catalog fields --profile lab-a --model res.partner --output json",
				"aerp catalog fields --profile lab-a --model res.partner --attribute string --attribute type --output json",
				`aerp catalog fields --profile lab-a --model res.partner --attributes-json '["string","type"]' --output json`,
			},
			Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
				return runCatalogFields(ctx, invocation, config)
			},
		}))
	}
	return &clicore.Command{
		Name:    "catalog",
		Summary: "Discover the canonical ERP operation catalog.",
		Description: `The same catalog the governed ASAO platform publishes, from the same source. An operation
id, its inputs, its typing rules and its meaning are identical in both products; only the trust and
routing envelope around a call differs.

Pure local computation: no profile is resolved and no Odoo is contacted, so this works before
anything is configured.`,
		Children: children,
	}
}

func provenanceCommand() *clicore.Command {
	return local(&clicore.Command{
		Name:       "provenance",
		Summary:    "Publish the catalog's evidence lineage — what has live evidence and what has none.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Description: `States exactly what live evidence exists for this semantic surface, and what does not.
Catalog size is not verification, and this document is the same one the governed product publishes,
from the same source — the lineage belongs to the catalog, not to whichever binary is asking.`,
		Examples: []string{"aerp provenance --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return aerpsurface.Provenance(), nil
		},
	})
}

// capabilityEnforcementNotice is the standalone answer to the same question the governed product's
// notice answers, and it is the OPPOSITE answer, stated plainly.
//
// Every catalog operation declares a requiredCapability. In the governed product that value is
// evaluated by the authority guard before Run executes. Here nothing evaluates it, and publishing
// the field without saying so would advertise an authorization contract this product does not
// implement — to exactly the machine readers least able to check it for themselves.
func capabilityEnforcementNotice(program string) map[string]interface{} {
	return map[string]interface{}{
		"status": "NOT_ENFORCED_STANDALONE",
		"note": "requiredCapability records which ASAO capability governs each operation in the GOVERNED " +
			"product. This standalone tool evaluates no capability and consults no grant: a call " +
			"succeeds if the configured Odoo credential is permitted to make it, and fails otherwise. " +
			"Run `" + program + " trust --output json` for the full trust boundary, or use the governed `asao` " +
			"control plane if you need capability enforcement.",
		"enforcedBy": "the odoo credential in the selected profile, and nothing else",
	}
}

// executionFlags are this product's execution flags: the SHARED canonical operation flags plus the
// one flag that is standalone routing and exists nowhere else, --profile.
//
// That is the whole difference between the two products' command lines. Governed contributes
// --organization/--connection; standalone contributes --profile; the rest is one shared definition.
func executionFlags(config programConfig) []clicore.Flag {
	return append(append([]clicore.Flag{profileFlag}, config.surface.OperationFlags()...), clicore.Flag{
		Name: "allow-unverified", Kind: clicore.KindBool,
		Description: "Explicitly allow direct execution of an UNVERIFIED operation. This does not upgrade its readiness or evidence.",
	}, saveFileFlag)
}

// saveFileFlag writes the file an operation returns (a printed PDF, an attachment's content) to a path.
var saveFileFlag = clicore.Flag{
	Name: "save-file", Kind: clicore.KindString,
	Description: "Write the file the operation returns (a printed report, an attachment's content) to this path; the result then names the path instead of carrying the content.",
}

// presentOutcome is an execution's result as the invocation asked for it: the file it returned saved
// to --save-file, a read's records as CSV.
func presentOutcome(invocation *clicore.Invocation, outcome direct.Outcome) (interface{}, error) {
	if path := strings.TrimSpace(invocation.String("save-file")); path != "" {
		if err := saveReturnedFile(outcome, path); err != nil {
			return nil, err
		}
	}
	if invocation.Output() == aerpsurface.CSVOutput {
		return aerpsurface.CSVResult{Result: outcome}, nil
	}
	return outcome, nil
}

// saveReturnedFile writes the base64 file an outcome carries (a report's content, or the datas of the
// one attachment a describe read) to path, and leaves the path in its place.
func saveReturnedFile(outcome direct.Outcome, path string) error {
	holder, field := returnedFile(outcome.Data)
	if holder == nil {
		return clicore.UsageError("NO_FILE_RETURNED", "%s returned no file to save", outcome.Operation).
			WithRemediation("--save-file applies to a print (<entity>.print) or to one attachment (<entity>.attachment.describe).")
	}
	encoded, _ := holder[field].(string)
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return clicore.NewError(clicore.CategoryProvider, "FILE_UNDECODABLE", "the returned file is not base64: "+err.Error())
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return clicore.NewError(clicore.CategoryInternal, "FILE_NOT_SAVED", err.Error()).WithContextValue("path", path)
	}
	delete(holder, field)
	holder["savedTo"] = path
	holder["savedBytes"] = len(content)
	return nil
}

// returnedFile finds the file an outcome's data carries: a report's content, or one record's datas.
func returnedFile(data interface{}) (map[string]interface{}, string) {
	switch value := data.(type) {
	case map[string]interface{}:
		if _, ok := value["content"].(string); ok {
			return value, "content"
		}
	case []map[string]interface{}:
		if len(value) == 1 {
			return returnedFile(value[0])
		}
	case []interface{}:
		if len(value) == 1 {
			if record, ok := value[0].(map[string]interface{}); ok {
				if _, ok := record["datas"].(string); ok {
					return record, "datas"
				}
			}
		}
	}
	if record, ok := data.(map[string]interface{}); ok {
		if _, has := record["datas"].(string); has {
			return record, "datas"
		}
	}
	return nil, ""
}

func planCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "plan",
		Summary:    "Describe what an execution would do, without contacting Odoo.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Description: `A dry run. It resolves the profile, resolves the operation, applies every pre-provider rule
(target requirement, ambiguity, field validity) and reports the effect, risk and verification
strategy — without any Odoo round trip.

	Use it to confirm a destructive or high-risk call is aimed at the Odoo you think it is before running
	it. The result's providerRoute names the profile that would have been used. Planning an UNVERIFIED
	operation is allowed and reports that readiness without contacting Odoo.`,
		Flags: executionFlags(config),
		Examples: []string{
			"aerp plan --profile lab-a --operation people.employee.list --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			executor, request, err := standaloneExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			plan, err := executor.Plan(ctx, request)
			if err != nil {
				return nil, classifyExecution(err, request.Operation, config.surface)
			}
			return plan, nil
		},
	})
}

func executeCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "execute",
		Summary:    "Run one canonical ERP operation against the selected Odoo.",
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Description: `Runs an operation through the canonical AERP execution layer, directly against the Odoo the
selected profile names, using that profile's credential.

THE AUTHORITY THIS CARRIES IS THE PROFILE CREDENTIAL'S. There is no ASAO actor, no capability check
and no central audit record. What the configured Odoo user may do, this command may do.

	Every result reports which profile ran it and how the target was determined, so a transcript can
never be mistaken for a governed one. A READ operation mutates nothing despite this command's
declared side effect, which is the worst case across the catalog; likewise destructive: true on this
command is a worst-case declaration about the command FAMILY, not about a specific call. Check the
	named operation's own strategy with "aerp catalog describe". UNVERIFIED operations are refused
	before provider contact unless the local operator explicitly passes --allow-unverified; the override
	does not change the UNVERIFIED readiness in output.`,
		Flags: append(executionFlags(config), aerpsurface.VerifyFlag()),
		Examples: []string{
			"aerp execute --profile lab-a --operation people.employee.list --limit 5 --output json",
			`aerp execute --profile lab-a --operation identity.contact.create --set name="Synthetic Contact" --output json`,
			"aerp execute --profile lab-a --operation people.employee.describe --provider-target-id 42 --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			executor, request, err := standaloneExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			if request.Verify, err = invocation.Bool("verify"); err != nil {
				return nil, err
			}
			outcome, err := executor.Execute(ctx, request)
			if err != nil {
				return nil, classifyExecution(err, request.Operation, config.surface)
			}
			return presentOutcome(invocation, outcome)
		},
	})
}

// standaloneTargetExecutor builds the executor from the selected profile.
func standaloneTargetExecutor(ctx context.Context, invocation *clicore.Invocation, config programConfig) (*direct.Executor, error) {
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
	return executor, nil
}

// standaloneExecutor builds the request and the executor for one invocation.
//
// ORDERING. The request is parsed and the profile resolved BEFORE anything capable of contacting an
// Odoo is constructed, exactly as the governed path does it — so a mistyped operation, an unknown
// field, an ambiguous profile or an unresolvable credential all refuse having contacted nothing.
//
// ISOLATION. One executor, holding one RPC client, is built per invocation from ONE profile's own
// endpoint, database and credential. Nothing is cached or shared between profiles at any layer, so
// "a call against profile A cannot touch profile B's Odoo" is structural rather than a rule someone
// has to remember.
func standaloneExecutor(ctx context.Context, invocation *clicore.Invocation, config programConfig) (*direct.Executor, direct.Request, error) {
	// The direct request type cannot carry ASAO organization or authorization inputs.
	request, err := config.surface.BuildRequest(invocation)
	if err != nil {
		return nil, direct.Request{}, err
	}
	if request.AllowUnverified, err = invocation.Bool("allow-unverified"); err != nil {
		return nil, direct.Request{}, err
	}
	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, direct.Request{}, err
	}
	if entry, ok := odoo.LookupStandaloneOperation(request.Operation); ok && entry.Strategy == odoo.StrategyReport {
		if err := useWebCredential(invocation, config, executor); err != nil {
			return nil, direct.Request{}, err
		}
	}
	return executor, request, nil
}

// useWebCredential gives a print its web password (WebCredentialRef). Odoo serves reports only to a
// logged-in web session and refuses API keys there: a JSON-RPC profile's password serves, a JSON-2
// profile's API key does not, so a JSON-2 profile without a web password is refused before anything
// is sent.
func useWebCredential(invocation *clicore.Invocation, config programConfig, executor *direct.Executor) error {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return err
	}
	material, err := localprofile.ResolveWebCredential(selection.Profile)
	switch {
	case err == nil:
		executor.SetWebPassword(material.Password)
		return nil
	case !errors.Is(err, localprofile.ErrCredentialUnavailable):
		return classifyProfile(err, selection.Profile.Name, config.name)
	case selection.Profile.Protocol == string(odoo.ProtocolJSON2):
		variable := localprofile.EnvVariableFor(localprofile.WebCredentialRef(selection.Profile))
		return clicore.NewError(clicore.CategoryConfig, "WEB_CREDENTIAL_REQUIRED",
			"printing needs a web password: Odoo serves reports only to a logged-in web session and refuses API keys there").
			WithContextValue("variable", variable).
			WithRemediation(fmt.Sprintf("Set %s (or keyring account %s, or that key in AERP_CREDENTIAL_FILE) to the Odoo user's password.", variable, localprofile.WebCredentialRef(selection.Profile)))
	}
	return nil
}

func runCatalogFields(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	model := strings.TrimSpace(invocation.String("model"))
	if model == "" {
		return nil, clicore.UsageError("MISSING_REQUIRED_FLAG", "--model is required").
			WithContextValue("flag", "model")
	}

	var attributes []string
	if invocation.Has("attribute") {
		for _, raw := range invocation.Raw["attribute"] {
			parts := strings.Split(raw, ",")
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed == "" {
					return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--attribute cannot be blank").
						WithContextValue("flag", "attribute")
				}
				attributes = append(attributes, trimmed)
			}
		}
	}

	if invocation.Has("attributes-json") {
		raw := strings.TrimSpace(invocation.String("attributes-json"))
		if raw == "" {
			return nil, clicore.UsageError("INVALID_JSON", "--attributes-json was provided but is empty").
				WithContextValue("flag", "attributes-json").
				WithRemediation("Pass a valid JSON array or omit the flag.")
		}
		var jsonAttrs []string
		if err := invocation.JSON("attributes-json", &jsonAttrs); err != nil {
			return nil, err
		}
		for _, attr := range jsonAttrs {
			trimmed := strings.TrimSpace(attr)
			if trimmed == "" {
				return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--attributes-json elements cannot be blank").
					WithContextValue("flag", "attributes-json")
			}
			attributes = append(attributes, trimmed)
		}
	}

	if _, err := direct.ValidateFieldAttributes(attributes); err != nil {
		return nil, classifyFieldsError(err, model, config.surface)
	}

	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, err
	}

	res, err := executor.FieldsGet(ctx, model, attributes)
	if err != nil {
		return nil, classifyFieldsError(err, model, config.surface)
	}
	return res, nil
}

func classifyFieldsError(err error, model string, surface aerpsurface.Surface) error {
	if err == nil {
		return nil
	}
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	switch {
	case errors.Is(err, direct.ErrFieldsModelRequired):
		return clicore.UsageError("MISSING_REQUIRED_FLAG", "--model is required").
			WithContextValue("flag", "model")
	case errors.Is(err, direct.ErrFieldsAttributeBlank):
		return clicore.UsageError("INVALID_FLAG_VALUE", "attribute cannot be blank").
			WithContextValue("flag", "attribute")
	case errors.Is(err, direct.ErrFieldsAttributeDuplicate):
		return clicore.UsageError("INVALID_FLAG_VALUE", "%s", err.Error()).
			WithContextValue("flag", "attribute")
	}
	if mapped := surface.ClassifyProviderAnswer(err); mapped != nil {
		return mapped
	}
	return clicore.InternalError(err)
}

// classifyComposition maps a failure to BUILD the executor — the profile did not describe a usable
// Odoo, or its endpoint is refused by the shared policy. Every error here is about a PROFILE, which
// is why it is a separate function from classifyExecution rather than the same one with a differently
// meaning resource id: a refusal that named `aerp.operation: lab-a` would point a caller at a
// catalog operation that does not exist.
func classifyComposition(err error, profileName, program string) error {
	if err == nil {
		return nil
	}
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	switch {
	case errors.Is(err, direct.ErrTargetIncomplete):
		return clicore.NewError(clicore.CategoryConfig, "PROFILE_INVALID", err.Error()).
			WithResource("aerp.profile", profileName).
			WithRemediation("Rewrite the profile with `" + program + " profile set --name " + profileName + " --endpoint <url> --database <db>`.")
	case errors.Is(err, direct.ErrCredentialMaterial):
		return clicore.NewError(clicore.CategoryConfig, "PROVIDER_CREDENTIAL_UNAVAILABLE", err.Error()).
			WithResource("aerp.profile", profileName).
			WithRemediation("Run `" + program + " profile show --output json` for the exact environment variable this profile's credential reference reads from. Nothing was contacted.")
	case errors.Is(err, direct.ErrEndpointDisallowed):
		return clicore.NewError(clicore.CategoryConfig, "PROVIDER_ENDPOINT_DISALLOWED", err.Error()).
			WithResource("aerp.profile", profileName).
			WithRemediation("This endpoint resolves to a loopback/private/link-local address, which the shared provider-endpoint policy refuses by default. A local Odoo is exactly that case: set " +
				providerEndpointOptIn + "=true to allow it.")
	}
	return clicore.InternalError(err)
}

// classifyExecution maps a failure to RUN an operation onto the CLI error contract.
//
// The whole of it is delegated to the shared surface, because the whole of it is canonical AERP
// semantics: an AERP_TARGET_REQUIRED or an AERP_VALIDATION_FAILED here carries the same code, the
// same category and the same exit code it carries on `asao`, from the same code path. This product
// contributes nothing of its own at this layer — its own failures are all about choosing a provider,
// and they happen earlier, in classifyProfile and classifyComposition.
func classifyExecution(err error, operationID string, surface aerpsurface.Surface) error {
	if err == nil {
		return nil
	}
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	if errors.Is(err, direct.ErrOperationNotReleaseReady) {
		return clicore.NewError(clicore.CategoryConflict, "OPERATION_NOT_RELEASE_READY", err.Error()).
			WithResource("aerp.operation", operationID).
			WithContextValue("readiness", string(odoo.ReadinessUnverified)).
			WithRemediation("Review the operation with `" + surface.CatalogDescribeCommand + " --operation " + operationID + " --output json`. To accept the risk for this direct call, re-run with --allow-unverified; the override does not upgrade its evidence.")
	}
	if mapped := surface.ClassifyOperation(err); mapped != nil {
		return mapped.WithResource("aerp.operation", operationID)
	}
	if mapped := surface.ClassifyProviderAnswer(err); mapped != nil {
		return mapped
	}
	return clicore.InternalError(err)
}
