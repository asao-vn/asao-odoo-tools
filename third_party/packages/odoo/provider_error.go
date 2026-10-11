package odoo

import (
	"errors"
	"net"
	"strings"
	"syscall"
)

// ProviderErrorClass is the stable, AERP-facing classification of a
// provider-level failure. Callers get this, plus a redacted diagnostic
// message; they never get raw Odoo traceback text as the contract.
type ProviderErrorClass string

const (
	ErrClassAccessDenied                     ProviderErrorClass = "ACCESS_DENIED"
	ErrClassNotFound                         ProviderErrorClass = "NOT_FOUND"
	ErrClassValidationFailed                 ProviderErrorClass = "VALIDATION_FAILED"
	ErrClassBusinessPreconditionFailed       ProviderErrorClass = "BUSINESS_PRECONDITION_FAILED"
	ErrClassReferentialConstraint            ProviderErrorClass = "REFERENTIAL_CONSTRAINT"
	ErrClassInsufficientStockOrReservation   ProviderErrorClass = "INSUFFICIENT_STOCK_OR_RESERVATION_FAILED"
	ErrClassDestructiveImpactRequiresConfirm ProviderErrorClass = "DESTRUCTIVE_IMPACT_REQUIRES_CONFIRMATION"
	ErrClassIdempotencyConflict              ProviderErrorClass = "IDEMPOTENCY_CONFLICT"
	ErrClassModuleNotInstalled               ProviderErrorClass = "MODULE_NOT_INSTALLED"
	ErrClassProviderError                    ProviderErrorClass = "PROVIDER_ERROR"
	ErrClassProviderUnreachable              ProviderErrorClass = "PROVIDER_UNREACHABLE"
	ErrClassVerificationFailed               ProviderErrorClass = "VERIFICATION_FAILED"
	// ErrClassProviderVersionUnsupported: the provider answered, but reports an Odoo series this
	// build has no catalog for. Nothing was executed and no credential was sent (provider_series.go).
	ErrClassProviderVersionUnsupported ProviderErrorClass = "PROVIDER_VERSION_UNSUPPORTED"
	// ErrClassOperationUnavailableOnSeries: the provider's series no longer has the model the
	// operation acts on (provider_dialect.go). Nothing was sent to the provider.
	ErrClassOperationUnavailableOnSeries ProviderErrorClass = "OPERATION_UNAVAILABLE_ON_SERIES"
	// ErrClassProviderNotOdoo: the endpoint answered, but not as Odoo answers its version handshake.
	// Nothing was sent, not even the credential (provider_fit.go).
	ErrClassProviderNotOdoo ProviderErrorClass = "PROVIDER_NOT_ODOO"
	// ErrClassEnvironmentUnverified: the operation changes records where this Odoo differs from the
	// environment its evidence was recorded on, and the caller did not accept an unverified result.
	// Nothing was executed (provider_fit.go).
	ErrClassEnvironmentUnverified ProviderErrorClass = "ENVIRONMENT_UNVERIFIED"
)

func isProviderUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && strings.EqualFold(opErr.Op, "dial") {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused",
		"no such host",
		"network is unreachable",
		"no route to host",
		"host is unreachable",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// ClassifyOdooError maps a raw Odoo/RPC error string to a stable class.
// Every pattern here traces to error text empirically captured in the
// odoo19-research corpus. The originally cited working file
// (odoo19-business-workflows.json) is not part of this repository; see
// docs/architecture/provider-research/odoo19/empirical-research/aerp-odoo19-conformance/FINAL_REPORT.md
// (Section "I. Error Normalization") and docs/architecture/provider-research/odoo19/ODOO_58_PROVENANCE_MATRIX.md.
// This is not speculative regex-golfing, it is provider truth absorbed into the
// governed layer so callers never have to parse Odoo's wording themselves.
func ClassifyOdooError(raw string) ProviderErrorClass {
	if isProviderUnreachableError(errors.New(raw)) {
		return ErrClassProviderUnreachable
	}
	l := strings.ToLower(raw)

	switch {
	case strings.Contains(l, "cannot be called remotely"),
		strings.Contains(l, "access denied"),
		strings.Contains(l, "authorizationdenied"),
		strings.Contains(l, "does not have access"),
		// Odoo's AccessError, from an access right or a record rule: "You are not allowed to access
		// 'Fields' (ir.model.fields) records." The quoted model keeps business refusals that also
		// say "not allowed" out of this class.
		strings.Contains(l, "you are not allowed to access '"),
		strings.Contains(l, "you are not allowed to modify '"),
		strings.Contains(l, "you are not allowed to create '"),
		strings.Contains(l, "you are not allowed to delete '"),
		strings.Contains(l, "due to security restrictions, you are not allowed"):
		return ErrClassAccessDenied

	case strings.Contains(l, "how about archiving the record instead"),
		strings.Contains(l, "the troublemaker is"),
		strings.Contains(l, "foreignkeyviolation"),
		strings.Contains(l, "violates foreign key constraint"):
		// Empirically this single Odoo-wide error shape covers BOTH true
		// delete-blocked-by-dependents cases AND the confusing case where a
		// CREATE references an already-deleted id (crm.lead -> deleted
		// partner_id) — Odoo reuses delete-conflict wording for both. We
		// classify both as REFERENTIAL_CONSTRAINT since the root cause is
		// the same FK relationship, and let the caller's context (were we
		// creating or deleting?) disambiguate for the human/agent.
		return ErrClassReferentialConstraint

	case strings.Contains(l, "zero quantity"),
		strings.Contains(l, "not moving invisible goods"),
		strings.Contains(l, "insufficient"):
		return ErrClassInsufficientStockOrReservation

	case strings.Contains(l, "invalid field"),
		strings.Contains(l, "validationerror"),
		strings.Contains(l, "malformed"),
		strings.Contains(l, "is not in the governed allowlist"):
		// The allowlist case never reaches Odoo at all — it's rejected by
		// the FIELD_WRITE strategy's own remapFields check before any RPC
		// call is made — but it's still a caller-input validation failure,
		// not an opaque provider error.
		return ErrClassValidationFailed

	case strings.Contains(l, "does not exist"),
		strings.Contains(l, "no such record"),
		strings.Contains(l, "record does not exist"):
		return ErrClassNotFound

	case strings.Contains(l, "required to validate this document"),
		strings.Contains(l, "usererror"),
		strings.Contains(l, "you can't delete a posted"),
		strings.Contains(l, "not eligible"),
		strings.Contains(l, "don't play games with your accounting"):
		return ErrClassBusinessPreconditionFailed

	default:
		return ErrClassProviderError
	}
}
