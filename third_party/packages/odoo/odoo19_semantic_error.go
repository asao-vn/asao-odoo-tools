package odoo

import (
	"errors"
	"fmt"
	"strings"
)

// FieldValidationError is a VALIDATION_FAILED that says which field was
// wrong and what would have been accepted.
//
// The previous contract surfaced a bare "VALIDATION_FAILED" string, which
// tells an autonomous caller that it failed but not how to succeed: it has
// to go read the catalog source to discover that catalog.product.create
// takes sale_price rather than standard_price. Carrying the offending field
// and the accepted set turns a dead end into a correctable response.
type FieldValidationError struct {
	Operation string
	Field     string
	Accepted  []string
}

func (e *FieldValidationError) Error() string {
	return fmt.Sprintf("field %q is not accepted by %s (accepted: %s)",
		e.Field, e.Operation, strings.Join(e.Accepted, ", "))
}

// ErrorClass keeps this error inside the existing stable-class vocabulary
// instead of introducing a parallel one.
func (e *FieldValidationError) ErrorClass() string { return "VALIDATION_FAILED" }

// StructuredDetail is the machine-readable payload carried alongside the
// stable class, for JSON consumers.
func (e *FieldValidationError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"field":           e.Field,
		"accepted_fields": e.Accepted,
	}
}

// LinesError reports a lines field (LineInputs) that is not a list of line objects, or the line that
// failed, by position: line_ids[1]. Its class is the failing line's (VALIDATION_FAILED for a field
// the line does not accept, NOT_FOUND for an account no line can name).
type LinesError struct {
	Operation string
	Field     string
	Line      int // -1: the field as a whole
	Cause     error
}

func (e *LinesError) Error() string {
	if e.Line < 0 {
		return fmt.Sprintf("%s: %s takes a list of line objects: %v", e.Operation, e.Field, e.Cause)
	}
	return fmt.Sprintf("%s: %s[%d]: %v", e.Operation, e.Field, e.Line, e.Cause)
}

func (e *LinesError) Unwrap() error { return e.Cause }

func (e *LinesError) ErrorClass() string {
	var classed interface{ ErrorClass() string }
	if e.Line >= 0 && errors.As(e.Cause, &classed) {
		return classed.ErrorClass()
	}
	return "VALIDATION_FAILED"
}

func (e *LinesError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"field": e.Field, "line": e.Line}
}

// GroupSpecError is a VALIDATION_FAILED raised before the provider is contacted, when a grouped read
// names a field the read does not show, an unknown granularity or an unknown aggregate function.
type GroupSpecError struct {
	Operation string
	Spec      string
	Reason    string
	Accepted  []string
}

func (e *GroupSpecError) Error() string {
	return fmt.Sprintf("%s cannot group or aggregate by %q: %s", e.Operation, e.Spec, e.Reason)
}

func (e *GroupSpecError) ErrorClass() string { return "VALIDATION_FAILED" }

func (e *GroupSpecError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"spec": e.Spec, "reason": e.Reason, "accepted_fields": e.Accepted}
}

// PrefixValueError is a VALIDATION_FAILED raised before the provider is contacted, when a value
// falls outside the prefix the operation holds its field to (Odoo19Operation.InputPrefixes).
type PrefixValueError struct {
	Operation string
	Field     string
	Value     string
	Prefix    string
}

func (e *PrefixValueError) Error() string {
	return fmt.Sprintf("field %q of %s must be a clean value under %q, got %q", e.Field, e.Operation, e.Prefix, e.Value)
}

func (e *PrefixValueError) ErrorClass() string { return "VALIDATION_FAILED" }

func (e *PrefixValueError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"field": e.Field, "value": e.Value, "required_prefix": e.Prefix}
}

// StructuredError is implemented by errors that can explain themselves to a
// machine. Anything not implementing it degrades to class-only, which is the
// pre-existing behaviour.
type StructuredError interface {
	error
	ErrorClass() string
	StructuredDetail() map[string]interface{}
}

// MissingFieldError is a VALIDATION_FAILED raised BEFORE the provider is
// contacted, when a create omits fields the contract marks required.
//
// Previously this surfaced as the provider's own opaque refusal
// ("The provider rejected this operation.") which named nothing, so an
// agent that guessed wrong had no path forward except permuting flags.
type MissingFieldError struct {
	Operation string
	Missing   []string
	Accepted  []string
	// Call is set for a value a method call or a wizard requires, rather than a create.
	Call bool
}

func (e *MissingFieldError) Error() string {
	if e.Call {
		return fmt.Sprintf("%s requires %s", e.Operation, strings.Join(e.Missing, ", "))
	}
	return fmt.Sprintf("%s requires %s on create", e.Operation, strings.Join(e.Missing, ", "))
}

func (e *MissingFieldError) ErrorClass() string { return "VALIDATION_FAILED" }

func (e *MissingFieldError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"missing_fields":  e.Missing,
		"accepted_fields": e.Accepted,
	}
}

// EnumValueError is a VALIDATION_FAILED raised BEFORE the provider is
// contacted, when a caller supplies a value outside a closed-value field's
// declared set.
//
// This replaces the provider's opaque refusal for selection fields. Guessing
// an enum value at a create previously meant writing junk records until one
// stuck, because the failure named neither the field's legal values nor even
// that the field was closed-valued.
type EnumValueError struct {
	Operation string
	Field     string
	Value     string
	Allowed   []string
}

func (e *EnumValueError) Error() string {
	return fmt.Sprintf("field %q of %s does not accept %q (allowed: %s)",
		e.Field, e.Operation, e.Value, strings.Join(e.Allowed, ", "))
}

func (e *EnumValueError) ErrorClass() string { return "VALIDATION_FAILED" }

func (e *EnumValueError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"field":          e.Field,
		"supplied_value": e.Value,
		"allowed_values": e.Allowed,
	}
}

// TargetRequirementError is a VALIDATION_FAILED raised BEFORE the provider is
// contacted, when the presence or absence of a target id contradicts the
// operation's declared WriteMode.
//
// This is what stops an UPDATE from degrading into a CREATE. Previously
// `people.employee.update` with no target id created a brand-new employee and
// reported VERIFIED, so a caller could not distinguish a successful edit from
// silent duplication of live business data.
type TargetRequirementError struct {
	Operation string
	Mode      string
	// TargetSupplied records what the caller actually did, so the message can
	// state the contradiction rather than only the rule.
	TargetSupplied bool
}

func (e *TargetRequirementError) Error() string {
	if e.Mode == string(WriteModeUpdate) {
		return fmt.Sprintf("%s updates an existing record and requires target_id; refusing to create a new record instead", e.Operation)
	}
	return fmt.Sprintf("%s creates a new record and does not accept target_id; use the matching update operation to modify an existing record", e.Operation)
}

func (e *TargetRequirementError) ErrorClass() string { return "VALIDATION_FAILED" }

func (e *TargetRequirementError) StructuredDetail() map[string]interface{} {
	detail := map[string]interface{}{
		"write_mode":      e.Mode,
		"target_supplied": e.TargetSupplied,
	}
	if e.Mode == string(WriteModeUpdate) {
		detail["missing_fields"] = []string{"target_id"}
	}
	return detail
}

// SingleRecordNotFoundError is a NOT_FOUND raised for a READ that supplied a
// target id and got back zero records.
//
// Previously every read — including an explicit id lookup — reported
// SUCCESS with an empty result set on a missing record, indistinguishable
// from "this collection legitimately has nothing in it". A caller doing
// read-back verification after a write could not tell "the record exists
// and is empty" from "the record does not exist": both looked like success.
// A collection read with no target id is unaffected — an empty list is
// still exactly what "what matches this filter?" can validly answer.
type SingleRecordNotFoundError struct {
	Operation     string
	ProviderModel string
	TargetID      int64
}

func (e *SingleRecordNotFoundError) Error() string {
	return fmt.Sprintf("%s: no %s record found with id %d", e.Operation, e.ProviderModel, e.TargetID)
}

func (e *SingleRecordNotFoundError) ErrorClass() string { return string(ErrClassNotFound) }

func (e *SingleRecordNotFoundError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_model": e.ProviderModel,
		"target_id":      e.TargetID,
	}
}

// RelationNotFoundError is a NOT_FOUND raised when a caller-supplied value
// for a relation field (an id or a name) does not identify any record in
// that field's target provider model.
//
// This replaces two failure modes empirically observed for a bad relation
// value: (1) a nonexistent id blamed on --target-id even when target_id was
// perfectly valid, because the executor had no field-level knowledge of
// WHICH id was wrong, and (2) an opaque PROVIDER_ERROR with no diagnostic
// content when the provider's own foreign-key rejection did not match any
// known error-text pattern. Both now report the actual offending field.
type RelationNotFoundError struct {
	Operation   string
	Field       string // semantic field name, e.g. "stage", "department"
	Supplied    string // what the caller actually passed
	TargetModel string
}

func (e *RelationNotFoundError) Error() string {
	return fmt.Sprintf("%s: %q does not identify any %s (field %q)", e.Operation, e.Supplied, e.TargetModel, e.Field)
}

func (e *RelationNotFoundError) ErrorClass() string { return string(ErrClassNotFound) }

func (e *RelationNotFoundError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"field":          e.Field,
		"supplied_value": e.Supplied,
		"target_model":   e.TargetModel,
	}
}

// AmbiguousReferenceError is a VALIDATION_FAILED raised when a caller
// supplied a business-facing name for a relation field and more than one
// record in the target model matched it exactly. The resolver fails closed
// rather than silently picking one.
type AmbiguousReferenceError struct {
	Operation   string
	Field       string
	Supplied    string
	TargetModel string
	Candidates  []map[string]interface{} // [{"id":..,"display_name":..}, ...]
}

func (e *AmbiguousReferenceError) Error() string {
	return fmt.Sprintf("%s: %q matches more than one %s for field %q; supply the numeric id instead",
		e.Operation, e.Supplied, e.TargetModel, e.Field)
}

func (e *AmbiguousReferenceError) ErrorClass() string { return string(ErrClassValidationFailed) }

func (e *AmbiguousReferenceError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"field":             e.Field,
		"supplied_value":    e.Supplied,
		"target_model":      e.TargetModel,
		"ambiguous":         true,
		"candidate_records": e.Candidates,
	}
}

// PreconditionFailedError is a BUSINESS_PRECONDITION_FAILED raised BEFORE a
// WIZARD or METHOD_CALL strategy contacts the provider's mutating call,
// when the target record does not satisfy the operation's declared
// Precondition. This is what stops a semantic operation that hand-builds a
// provider side-effect (e.g. an account.payment.register wizard) from
// skipping an invariant the provider's own native action would have
// enforced (e.g. "you can only register payment for posted journal
// entries").
type PreconditionFailedError struct {
	Operation     string
	ProviderModel string
	Field         string
	Expect        interface{}
	Got           interface{}
	TargetID      int64
}

func (e *PreconditionFailedError) Error() string {
	return fmt.Sprintf("%s: %s(%d).%s is %v, requires %v", e.Operation, e.ProviderModel, e.TargetID, e.Field, e.Got, e.Expect)
}

func (e *PreconditionFailedError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *PreconditionFailedError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_model": e.ProviderModel,
		"field":          e.Field,
		"expected_value": e.Expect,
		"actual_value":   e.Got,
		"target_id":      e.TargetID,
	}
}

// NoEligibleWizardLinkError is a BUSINESS_PRECONDITION_FAILED raised when a
// WIZARD operation's link-source query (e.g. "find the open receivable line
// for this invoice") comes back empty — most commonly because the document
// already has no open balance (already fully paid, or has no balance to
// begin with). This is the safe, explicit outcome for retrying
// finance.payment.register against an already-settled document: it fails
// loudly with zero provider mutation, rather than silently finding nothing
// to link and erroring opaquely.
type NoEligibleWizardLinkError struct {
	Operation   string
	SourceModel string
	TargetID    int64
}

func (e *NoEligibleWizardLinkError) Error() string {
	return fmt.Sprintf("%s: no open %s records found for target %d — nothing to act on (already settled, or has no eligible balance)",
		e.Operation, e.SourceModel, e.TargetID)
}

func (e *NoEligibleWizardLinkError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *NoEligibleWizardLinkError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"source_model": e.SourceModel,
		"target_id":    e.TargetID,
	}
}

// TargetNotEligibleError is a BUSINESS_PRECONDITION_FAILED raised BEFORE a
// METHOD_CALL strategy contacts the provider, when the target record does
// not match the operation's declared ProviderDomain. The same declarative field
// constrains reads and gates mutations, so an operation cannot be pointed at a
// record outside the business subset it names.
type TargetNotEligibleError struct {
	Operation      string
	ProviderModel  string
	TargetID       int64
	ProviderDomain []interface{}
}

func (e *TargetNotEligibleError) Error() string {
	return fmt.Sprintf("%s: %s(%d) is not eligible for this operation; operation is constrained to its declared business subset (%v)",
		e.Operation, e.ProviderModel, e.TargetID, e.ProviderDomain)
}

func (e *TargetNotEligibleError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *TargetNotEligibleError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"operation":       e.Operation,
		"provider_model":  e.ProviderModel,
		"target_id":       e.TargetID,
		"provider_domain": e.ProviderDomain,
	}
}

type TargetIneligibleError = TargetNotEligibleError

// RequiredInputNames lists the contract fields a create must supply.
func (c OperationContract) RequiredInputNames() []string {
	var out []string
	for _, f := range c.Inputs {
		if f.Required {
			out = append(out, f.Name)
		}
	}
	return out
}

// ModuleInstallStateError is a BUSINESS_PRECONDITION_FAILED raised when a
// module install operation completes but the module is not in "installed" state.
type ModuleInstallStateError struct {
	Operation string
	Module    string
	State     string
}

func (e *ModuleInstallStateError) Error() string {
	return fmt.Sprintf("module %s is in state %s after install", e.Module, e.State)
}

func (e *ModuleInstallStateError) ErrorClass() string {
	return string(ErrClassBusinessPreconditionFailed)
}

func (e *ModuleInstallStateError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"operation": e.Operation,
		"module":    e.Module,
		"state":     e.State,
	}
}
