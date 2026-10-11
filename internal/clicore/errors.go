package clicore

import (
	"errors"
	"fmt"
)

// Exit codes. Each code means exactly one thing — the historical CLI's most-reported debt was an
// overloaded exit code, where "bad flag", "no such record" and "the provider refused" all came
// back as 1 and a calling agent could not branch on the outcome without parsing English.
//
// A caller may branch on the exit code alone, or read error.code for the specific reason. These
// values are contract: changing one is a breaking change.
const (
	// ExitOK — the command did what it said.
	ExitOK = 0
	// ExitInternal — an unexpected fault inside the CLI itself. Never used for a refusal the CLI
	// meant to make; if you can name the reason, it is not this code.
	ExitInternal = 1
	// ExitUsage — the invocation is malformed: unknown command, unknown flag, missing required
	// flag, wrong value type. Nothing was executed.
	ExitUsage = 2
	// ExitNotFound — a named resource does not exist (or is not visible in this organization,
	// which is the same answer by construction).
	ExitNotFound = 3
	// ExitConflict — the request contradicts existing state or is ambiguous: a conflicting
	// binding, an ambiguous reference, an indeterminate provider operation.
	ExitConflict = 4
	// ExitDenied — an authorization decision refused the request. Distinct from ExitUsage: the
	// request was well-formed and the answer is no.
	ExitDenied = 5
	// ExitProvider — the external provider was reached and reported a failure.
	ExitProvider = 6
	// ExitConfig — the environment is missing or malformed: no database configuration, no
	// credential material, no capability definition file. Nothing was executed.
	ExitConfig = 7
	// ExitUnavailable — a dependency could not be reached at all (database, provider endpoint).
	// Distinct from ExitProvider: nothing answered.
	ExitUnavailable = 8
)

// Category is the coarse, stable classification carried on every error. It maps one-to-one onto an
// exit code, so an agent may branch on either.
type Category string

const (
	CategoryUsage       Category = "USAGE"
	CategoryInternal    Category = "INTERNAL"
	CategoryNotFound    Category = "NOT_FOUND"
	CategoryConflict    Category = "CONFLICT"
	CategoryDenied      Category = "DENIED"
	CategoryProvider    Category = "PROVIDER"
	CategoryConfig      Category = "CONFIG"
	CategoryUnavailable Category = "UNAVAILABLE"
)

func (c Category) exitCode() int {
	switch c {
	case CategoryUsage:
		return ExitUsage
	case CategoryNotFound:
		return ExitNotFound
	case CategoryConflict:
		return ExitConflict
	case CategoryDenied:
		return ExitDenied
	case CategoryProvider:
		return ExitProvider
	case CategoryConfig:
		return ExitConfig
	case CategoryUnavailable:
		return ExitUnavailable
	default:
		return ExitInternal
	}
}

// ResourceRef names what an error is about, in canonical terms. Type is a dotted, stable kind
// ("aerp.operation", "commerce.order", "provider.connection"); ID is the identifier the caller
// used. It exists so a caller can react to WHICH thing failed without regex over a message.
type ResourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// Error is the CLI's single error shape. Everything a caller needs to react is a field:
//
//	Code        — stable, specific, machine-readable (e.g. AERP_TARGET_REQUIRED)
//	Category    — coarse class, one-to-one with the exit code
//	Message     — human-readable, no stack traces, no secret material
//	Resource    — which thing this is about
//	Context     — structured detail from the layer that refused (accepted field names, enum
//	              values, ambiguous candidates, dependent-record counts) — this is what lets an
//	              agent correct itself without reading source
//	Remediation — what to do next, in CLI terms
type Error struct {
	Code        string                 `json:"code"`
	Category    Category               `json:"category"`
	Message     string                 `json:"message"`
	ExitCode    int                    `json:"exitCode"`
	Resource    *ResourceRef           `json:"resource,omitempty"`
	Context     map[string]interface{} `json:"context,omitempty"`
	Retryable   bool                   `json:"retryable"`
	Remediation string                 `json:"remediation,omitempty"`

	// cause is preserved for wrapping/unwrapping but never rendered: a caller gets the fields
	// above, never a nested Go error string it would have to parse.
	cause error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// NewError builds a CLI error with the exit code its category implies. Constructing the exit code
// here rather than at each call site is what stops the two drifting apart.
func NewError(category Category, code, message string) *Error {
	return &Error{Code: code, Category: category, Message: message, ExitCode: category.exitCode()}
}

// WithResource names what the error is about.
func (e *Error) WithResource(kind, id string) *Error {
	e.Resource = &ResourceRef{Type: kind, ID: id}
	return e
}

// WithContext attaches structured detail. Nil and empty maps are ignored so the rendered document
// never carries an empty object.
func (e *Error) WithContext(context map[string]interface{}) *Error {
	if len(context) == 0 {
		return e
	}
	if e.Context == nil {
		e.Context = map[string]interface{}{}
	}
	for key, value := range context {
		e.Context[key] = value
	}
	return e
}

// WithContextValue attaches one structured detail field.
func (e *Error) WithContextValue(key string, value interface{}) *Error {
	return e.WithContext(map[string]interface{}{key: value})
}

// WithRemediation says what to do next.
func (e *Error) WithRemediation(remediation string) *Error {
	e.Remediation = remediation
	return e
}

// WithRetryable marks an error the caller may safely retry unchanged.
func (e *Error) WithRetryable(retryable bool) *Error {
	e.Retryable = retryable
	return e
}

// WithCause preserves the underlying error for errors.Is/As without rendering it.
func (e *Error) WithCause(cause error) *Error {
	e.cause = cause
	return e
}

// UsageError is the most common refusal: the invocation itself is wrong. Nothing was executed.
func UsageError(code, format string, args ...interface{}) *Error {
	return NewError(CategoryUsage, code, fmt.Sprintf(format, args...))
}

// ConfigError reports a missing or malformed environment. Nothing was executed.
func ConfigError(code, format string, args ...interface{}) *Error {
	return NewError(CategoryConfig, code, fmt.Sprintf(format, args...))
}

// InternalError wraps an unexpected fault. Reserved for faults the CLI did not mean to produce.
func InternalError(cause error) *Error {
	return NewError(CategoryInternal, "INTERNAL_ERROR", cause.Error()).WithCause(cause)
}

// AsError extracts a *Error from an error chain, or classifies an unrecognised error as internal.
// Callers that want a specific classification must produce a *Error themselves — this function
// deliberately does not guess from message text.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	return InternalError(err)
}
