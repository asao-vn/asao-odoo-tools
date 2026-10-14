package direct

import (
	"context"
	"errors"
	"strings"

	"github.com/asao-platform/atoc/packages/odoo"
)

var (
	ErrRawModelRequired    = errors.New("odoo direct: model is required")
	ErrRawMethodRequired   = errors.New("odoo direct: method is required")
	ErrRawMethodDisallowed = errors.New("odoo direct: leading-underscore methods are not permitted")
	ErrRawMutationRefused  = errors.New("odoo direct: method requires --allow-mutation")
)

var readOnlyMethods = map[string]bool{
	"search":       true,
	"search_read":  true,
	"read":         true,
	"search_count": true,
	"fields_get":   true,
	"name_search":  true,
}

// IsReadOnlyMethod reports whether method is on the safe read-only allowlist.
func IsReadOnlyMethod(method string) bool {
	return readOnlyMethods[method]
}

// RawRequest represents a low-level Odoo JSON-RPC call.
type RawRequest struct {
	Model  string                 `json:"model"`
	Method string                 `json:"method"`
	Args   []interface{}          `json:"args,omitempty"`
	Kwargs map[string]interface{} `json:"kwargs,omitempty"`
	// AdmitCandidateSeries lets this call reach a candidate series (20.0): a generic read has no
	// catalog translation to rely on, and its answer is labelled unverified by its caller.
	AdmitCandidateSeries bool `json:"-"`
}

// RawOutcome represents the unmediated provider response from a raw call.
// It carries raw: true, provider route metadata, and mutationAllowed.
// It never claims semantic catalog readiness or evidence.
type RawOutcome struct {
	Raw             bool          `json:"raw"`
	Model           string        `json:"model"`
	Method          string        `json:"method"`
	ProviderRoute   ProviderRoute `json:"providerRoute"`
	MutationAllowed bool          `json:"mutationAllowed"`
	Data            interface{}   `json:"data"`
}

// Call executes an unmediated JSON-RPC call against Odoo with strict validation.
// Read-only methods (search, search_read, read, search_count, fields_get, name_search)
// execute without mutation override. All other methods require allowMutation=true.
// Leading-underscore methods and blank values are rejected before provider contact.
func (e *Executor) Call(ctx context.Context, req RawRequest, allowMutation bool) (RawOutcome, error) {
	if strings.TrimSpace(req.Model) == "" {
		return RawOutcome{}, ErrRawModelRequired
	}
	if strings.TrimSpace(req.Method) == "" {
		return RawOutcome{}, ErrRawMethodRequired
	}
	if strings.HasPrefix(strings.TrimSpace(req.Method), "_") {
		return RawOutcome{}, ErrRawMethodDisallowed
	}
	if !IsReadOnlyMethod(req.Method) && !allowMutation {
		return RawOutcome{}, ErrRawMutationRefused
	}
	args := req.Args
	if args == nil {
		args = []interface{}{}
	}
	kwargs := req.Kwargs
	if kwargs == nil {
		kwargs = map[string]interface{}{}
	}
	if req.AdmitCandidateSeries {
		e.client.AdmitCandidateSeries(true)
		defer e.client.AdmitCandidateSeries(false)
	}
	res, err := e.client.ExecuteKw(ctx, req.Model, req.Method, args, kwargs)
	if err != nil {
		return RawOutcome{}, classifyRawExecutionError(req.Model, req.Method, err)
	}
	return RawOutcome{
		Raw:             true,
		Model:           req.Model,
		Method:          req.Method,
		ProviderRoute:   e.route,
		MutationAllowed: allowMutation,
		Data:            res,
	}, nil
}

func classifyRawExecutionError(model, method string, err error) error {
	class := string(odoo.ClassifyOdooError(err.Error()))
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") ||
		strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		class = "PROVIDER_UNREACHABLE"
	}
	return &OperationError{
		Operation: model + "." + method,
		Class:     class,
		Message:   err.Error(),
		Detail:    map[string]interface{}{},
		cause:     err,
	}
}
