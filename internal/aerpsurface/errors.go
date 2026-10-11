package aerpsurface

import (
	"errors"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// ClassifyOperation maps the errors that belong to the CANONICAL AERP surface — the ones about the
// operation itself, about its target, and about what the provider answered — onto the CLI error
// contract.
//
// It returns nil for anything it does not own. That is the seam: each product then maps its OWN
// way of choosing a provider (governed ProviderConnection routing / standalone local profiles) on
// top, and neither has to know the other exists. What both DO share is everything a caller can
// learn about the operation, which is why AERP_TARGET_REQUIRED means exactly one thing on this
// platform rather than one thing per binary.
//
// It also does not own the Edge refusals: an Edge is a governed transport, has no standalone
// counterpart, and its classification (packages/cli/internal/commands' classifyEdge) stays where the
// governed product's routing errors live. A standalone composition cannot produce one — it has no
// Edge adapter to build (see packages/odoo/direct).
func (s Surface) ClassifyOperation(err error) *clicore.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, direct.ErrUnknownOperation):
		return clicore.NewError(clicore.CategoryUsage, "AERP_UNKNOWN_OPERATION", err.Error()).
			WithRemediation("Run `" + s.CatalogListCommand + " --output json` for every operation this platform defines.")
	case errors.Is(err, direct.ErrTargetRequired):
		return clicore.NewError(clicore.CategoryUsage, "AERP_TARGET_REQUIRED", err.Error()).
			WithRemediation("This operation acts on an existing record. " + s.TargetHint)
	case errors.Is(err, direct.ErrTargetNotAccepted):
		return clicore.NewError(clicore.CategoryUsage, "AERP_TARGET_NOT_ACCEPTED", err.Error()).
			WithRemediation("This operation creates a record and has no existing target. Drop --provider-target-id.")
	}
	return nil
}

// ClassifyProviderAnswer maps a classified failure from the provider (or from the semantic layer
// immediately above it) onto the CLI error contract, preserving its structured detail unflattened —
// the accepted field names, enum values and destructive impact are what let a caller correct itself.
//
// It returns nil when err is not an *direct.OperationError, and it returns nil for an
// edge-boundary class, which is the governed product's to present: an edge failure is a TRANSPORT
// fact, and rendering it as "AERP_EDGE_UNAVAILABLE" would tell a caller the ERP said something when
// nothing was ever contacted.
func (s Surface) ClassifyProviderAnswer(err error) *clicore.Error {
	var operationErr interface {
		OperationFailure() (string, string, string, map[string]interface{})
	}
	if !errors.As(err, &operationErr) {
		return nil
	}
	operation, class, message, detail := operationErr.OperationFailure()
	if IsEdgeClass(class) {
		return nil
	}
	return clicore.NewError(providerCategory(class), "AERP_"+class, message).
		WithResource("aerp.operation", operation).
		WithContext(detail).
		// PROVIDER_UNREACHABLE is the one AERP class it is safe to retry unchanged — the transport
		// never delivered the request, so no side effect can have occurred. Every other class means
		// the provider WAS reached, and for a mutation that outcome may already be real; retrying it
		// automatically is not safe and must stay the accepted default (false). Do not broaden this
		// beyond PROVIDER_UNREACHABLE.
		WithRetryable(class == "PROVIDER_UNREACHABLE").
		WithRemediation(s.providerRemediation(class))
}

// IsEdgeClass reports whether a provider error class is actually an Edge-transport fact rather than
// a provider answer. It is the one list, consumed by the governed presenter and by this package's
// refusal to present such a class as an ERP answer.
func IsEdgeClass(class string) bool {
	switch class {
	case "EDGE_NOT_FOUND", "EDGE_NOT_ACTIVE", "EDGE_UNAVAILABLE", "EDGE_PROVIDER_UNAVAILABLE",
		"EDGE_COMMAND_EXPIRED", "EDGE_RECONCILIATION_REQUIRED", "EDGE_ROUTING_NOT_CONFIGURED",
		"EDGE_VERIFICATION_NOT_PERFORMED":
		return true
	}
	return false
}

// providerCategory maps the accepted provider error taxonomy onto the CLI's exit-code categories.
// The taxonomy is the semantic layer's, not the CLI's — this only decides which exit code each
// class deserves.
func providerCategory(class string) clicore.Category {
	switch class {
	case "ACCESS_DENIED":
		return clicore.CategoryDenied
	case "PROVIDER_UNREACHABLE":
		// Exit 8. Exit 6 asserts the provider was reached and reported a failure; it was not
		// reached at all.
		return clicore.CategoryUnavailable
	case "NOT_FOUND":
		return clicore.CategoryNotFound
	case "VALIDATION_FAILED":
		return clicore.CategoryUsage
	case "PROVIDER_VERSION_UNSUPPORTED", "OPERATION_UNAVAILABLE_ON_SERIES", "PROVIDER_NOT_ODOO":
		// The connection points at an Odoo series this build has no catalog for, or whose series
		// removed what the operation acts on. Nothing was executed, so it is a configuration fact
		// about the connection, not a provider answer.
		return clicore.CategoryConfig
	case "REFERENTIAL_CONSTRAINT", "IDEMPOTENCY_CONFLICT", "DESTRUCTIVE_IMPACT_REQUIRES_CONFIRMATION":
		return clicore.CategoryConflict
	case "ENVIRONMENT_UNVERIFIED":
		// The same category as an UNVERIFIED operation's refusal, and accepted by the same flag.
		return clicore.CategoryConflict
	case "BUSINESS_PRECONDITION_FAILED", "INSUFFICIENT_STOCK_OR_RESERVATION_FAILED", "VERIFICATION_FAILED":
		return clicore.CategoryProvider
	default:
		return clicore.CategoryProvider
	}
}

// doctorHint points at the provider-fit report when this surface has one.
func (s Surface) doctorHint() string {
	if s.DoctorCommand == "" {
		return ""
	}
	return ". `" + s.DoctorCommand + "` reports what this Odoo runs and how it differs"
}

func (s Surface) providerRemediation(class string) string {
	switch class {
	case "VALIDATION_FAILED":
		return "The error context lists the field names and values this operation accepts. Run `" + s.CatalogDescribeCommand + " --operation <id> --output json` for the full contract."
	case "DESTRUCTIVE_IMPACT_REQUIRES_CONFIRMATION":
		return "The error context reports the dependent records this would affect. Re-run with --confirm-impact once you accept them."
	case "NOT_FOUND":
		return "The named record does not exist in this provider. Re-read it with the matching list operation."
	case "PROVIDER_UNREACHABLE":
		return "The provider did not accept a connection. Check that it is running and that this connection's endpoint is correct; the request was never delivered."
	case "PROVIDER_VERSION_UNSUPPORTED":
		return "This Odoo runs a series this build does not support; the error context names it and the supported series. Nothing was executed and the credential was not sent. Connect to a supported Odoo instead. When the context marks the series as a candidate, this build can translate for it but has no conformance evidence: the standalone tool runs it only with --allow-unverified, and the governed product refuses it."
	case "OPERATION_UNAVAILABLE_ON_SERIES":
		return "This Odoo's series no longer has the model this operation acts on; the error context says what replaced it. Nothing was sent to the provider."
	case "PROVIDER_NOT_ODOO":
		return "The endpoint did not answer as Odoo; the error context says how it answered. Nothing was sent, not even the credential. Check the endpoint's scheme (https or http), host, port and any proxy in front of Odoo."
	case "MODULE_NOT_INSTALLED":
		return "This Odoo lacks the modules the error context names, so the operation was not sent. Install them, or choose an operation this Odoo can run" + s.doctorHint() + "."
	case "ENVIRONMENT_UNVERIFIED":
		return "Where this operation acts, this Odoo differs from the stock image it was verified on: the error context lists how. Nothing was executed. ASAO cannot verify or guarantee operations on a customised Odoo (the context's notice says so); to run this one at your own risk, re-run with --allow-unverified, which does not upgrade its evidence" + s.doctorHint() + "."
	case "ACCESS_DENIED":
		return s.AccessDeniedRemediation
	default:
		return ""
	}
}
