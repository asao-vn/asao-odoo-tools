package standalone

import (
	"context"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// TrustBoundary is this product's security disclosure, as DATA.
//
// It exists because the most likely operator of this binary is an AI agent, and an agent cannot read
// a paragraph of help text into a policy decision — it can read a document. Every claim here is
// stated positively ("what this does") and negatively ("what it does not provide"), so a caller can
// assert on the absence rather than infer it from silence.
//
// It is also, deliberately, not alarmist. Standalone mode is a legitimate product for a single trust
// domain; the point is that nobody should mistake it for the governed one.
func TrustBoundary() map[string]interface{} {
	return map[string]interface{}{
		"product": "STANDALONE_AERP",
		"summary": "This tool executes canonical AERP operations with the full authority of the Odoo " +
			"credential named by the selected local profile. Anything that can run this binary and " +
			"read that credential can do whatever that Odoo user can do through the canonical AERP surface.",
		"authorityModel": "LOCAL_OPERATOR_CREDENTIAL",
		"provides": []string{
			"the canonical AERP operation catalog, identical to the governed product's",
			"direct execution against one locally configured Odoo per profile",
			"machine-readable discovery, results and errors",
			"catalog-enforced input validation and destructive-impact confirmation, which are properties of the operation and apply here identically",
		},
		"doesNotProvide": []string{
			"ASAO actor authentication",
			"ASAO CapabilityGrant enforcement",
			"ASAO organization-scoped multi-user authorization",
			"ASAO central audit trail",
			"privileged-runtime separation between the caller and the provider credential",
		},
		"absentByDesign": map[string]interface{}{
			"asaoAccountRequired":       false,
			"asaoOrganizationRequired":  false,
			"capabilityGrantEvaluated":  false,
			"asaoAuthorityRuntimeUsed":  false,
			"asaoCanonicalDatabaseUsed": false,
			"edgeRoutingAvailable":      false,
			"managedOdooProvisioning":   false,
		},
		"identicalToGovernedProduct": []string{
			"semantic operation ids", "operation input contracts", "field typing and validation",
			"catalog target rules", "odoo mapping", "normalized result document", "provider error taxonomy",
		},
		"governedCounterpart": map[string]interface{}{
			"binary": "asao",
			"use": "Use asao when an ERP call must be authenticated, authorized against an " +
				"organization-scoped capability, and recorded in a central audit trail.",
			"noFallbackBetweenProducts": "asao never falls back to this tool. A governed operation whose " +
				"authority runtime is unavailable fails closed; it does not execute locally.",
		},
		"credentialHandling": map[string]interface{}{
			"profileStores":        "endpoint, database, login and a credential REFERENCE — never a password",
			"materialResolvedFrom": []string{localprofile.CredentialEnvPrefix + "<REFERENCE>", "OS keyring service " + localprofile.KeyringService, localprofile.CredentialFileVariable},
			"credentialSources":    []string{localprofile.CredentialSourceEnvironment, localprofile.CredentialSourceKeyring, localprofile.CredentialSourceFile},
			"credentialResolves":   "reported as a boolean (true/false) by profile and doctor commands — never echoes secret material",
			"credentialSource":     "reported as environment, keyring or file when resolved",
			"credentialFileMode":   "must be readable by its owner alone (0600); a more permissive file is refused, not read",
			"neverPrinted":         "no command, help text, discovery document or error emitted by this tool contains credential material",
		},
		"endpointPolicy": map[string]interface{}{
			"sharedWith": "the governed product — there is exactly one provider-endpoint policy in this codebase",
			"privateAddressesRequireOptIn": providerendpoint.AllowPrivateEnvVar + "=true is required to dial a " +
				"loopback/private/link-local endpoint, which includes the usual local Odoo at 127.0.0.1",
			"optInActive": providerendpoint.Allowed(),
		},
	}
}

func trustCommand() *clicore.Command {
	return local(&clicore.Command{
		Name:       "trust",
		Summary:    "Publish this tool's trust boundary — what it enforces, and what it does not.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Description: `States, as a machine-readable document, exactly which security properties this product
provides and which it does not. An automated caller should read this before granting this tool access
to anything it cares about.

It contacts nothing and reads no credential. The only environment it inspects is whether the shared
private-endpoint opt-in is set, which is a policy fact rather than a secret.`,
		Examples: []string{"aerp trust --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return TrustBoundary(), nil
		},
	})
}
