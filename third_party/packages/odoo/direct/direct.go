package direct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/google/uuid"
)

var (
	ErrTargetIncomplete         = errors.New("odoo direct: target is incomplete")
	ErrCredentialMaterial       = errors.New("odoo direct: could not resolve odoo credential material")
	ErrEndpointDisallowed       = errors.New("odoo direct: endpoint is invalid or disallowed")
	ErrTargetRequired           = errors.New("odoo direct: operation requires a target")
	ErrTargetNotAccepted        = errors.New("odoo direct: operation does not accept a target")
	ErrOperationNotReleaseReady = errors.New("odoo direct: operation is not release ready")
)

// Target identifies one Odoo. Password is resolved by the caller at construction
// and is retained only by the RPC client, never by diagnostic route metadata.
type Target struct {
	ProfileName string
	Endpoint    string
	Database    string
	Login       string
	Password    string `json:"-"`
	// Protocol is how to speak to Odoo: "jsonrpc" (the default) or "json2", whose Password is an
	// API key (odoo.ProtocolJSON2).
	Protocol string
}

// Request accepts provider-native targeting only. It carries no governed identity.
type Request struct {
	Operation        string
	ProviderTargetID int64
	Values           map[string]interface{}
	Domain           []interface{}
	Fields           []string
	Limit            int
	Order            string
	// Offset pages a read; GroupBy and Aggregates make it answer provider-side groups
	// (odoo.OperationInput).
	Offset          int
	GroupBy         []string
	Aggregates      []string
	ConfirmImpact   bool
	ProviderContext map[string]interface{}
	Verify          bool
	AllowUnverified bool
}

// EndpointChecker is mandatory. The CLI supplies shared providerendpoint.CheckHost.
type EndpointChecker func(context.Context, string) error

const ResolutionLocalProfile = "LOCAL_PROFILE"

// ProviderRoute preserves the CLI wire contract: connectionId is a local profile
// name, explicitly distinguished from a governed connection by resolution.
type ProviderRoute struct {
	ConnectionID string `json:"connectionId"`
	Route        string `json:"route"`
	Resolution   string `json:"resolution"`
}

type Plan struct {
	Operation            string                 `json:"operation"`
	Provider             string                 `json:"provider"`
	Resource             string                 `json:"providerResource,omitempty"`
	Effect               string                 `json:"expectedEffect,omitempty"`
	Readiness            string                 `json:"readiness,omitempty"`
	Input                map[string]interface{} `json:"proposedInput,omitempty"`
	ProviderRoute        ProviderRoute          `json:"providerRoute"`
	RequiredCapability   string                 `json:"requiredCapability"`
	Risk                 string                 `json:"risk"`
	ApprovalRequired     bool                   `json:"approvalRequired"`
	VerificationStrategy string                 `json:"verificationStrategy,omitempty"`
	TargetMediation      string                 `json:"targetMediation"`
	EvidenceClass        string                 `json:"evidenceClass"`
}

type Outcome struct {
	Operation         string        `json:"operation"`
	Provider          string        `json:"provider"`
	Status            string        `json:"status"`
	Readiness         string        `json:"readiness,omitempty"`
	Verification      string        `json:"verificationStatus"`
	ProviderReference string        `json:"providerReference,omitempty"`
	Data              interface{}   `json:"data,omitempty"`
	ProviderRoute     ProviderRoute `json:"providerRoute"`
	TargetMediation   string        `json:"targetMediation"`
	EvidenceClass     string        `json:"evidenceClass"`
	// ProviderSeries is the Odoo series that answered. Readiness is that series' readiness.
	ProviderSeries string `json:"providerSeries,omitempty"`
	// ProviderFit is what the provider-fit check found where it is worth saying: an operation run
	// on an environment it was not verified on, or notes about the Odoo it ran on (DEC-2026-0157).
	ProviderFit *odoo.OperationFit `json:"providerFit,omitempty"`
}

type OperationError struct {
	Operation string
	Class     string
	Message   string
	Detail    map[string]interface{}
	cause     error
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("aerp operation %s failed (%s): %s", e.Operation, e.Class, e.Message)
}

func (e *OperationError) Unwrap() error {
	return e.cause
}

// OperationFailure permits shared CLI rendering without a service import.
func (e *OperationError) OperationFailure() (string, string, string, map[string]interface{}) {
	return e.Operation, e.Class, e.Message, e.Detail
}

// Executor delegates all semantic execution to the parent adapter.
type Executor struct {
	client  *odoo.RPCClient
	adapter *odoo.Adapter
	route   ProviderRoute
	// endpoint is where the profile points; the provider-fit check reads the hosting from it.
	endpoint string
}

func NewExecutor(ctx context.Context, target Target, check EndpointChecker) (*Executor, error) {
	switch {
	case strings.TrimSpace(target.Endpoint) == "":
		return nil, fmt.Errorf("%w: missing endpoint", ErrTargetIncomplete)
	case strings.TrimSpace(target.Database) == "":
		return nil, fmt.Errorf("%w: missing odoo database", ErrTargetIncomplete)
	case strings.TrimSpace(target.Login) == "":
		return nil, fmt.Errorf("%w: missing login", ErrCredentialMaterial)
	case target.Password == "":
		return nil, fmt.Errorf("%w: missing password", ErrCredentialMaterial)
	}
	// Do not echo parsing/checker errors: they can carry URL credential material.
	u, err := url.Parse(target.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || check == nil {
		return nil, ErrEndpointDisallowed
	}
	if err := check(ctx, target.Endpoint); err != nil {
		return nil, ErrEndpointDisallowed
	}
	protocol, err := odoo.ParseProtocol(target.Protocol)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTargetIncomplete, err)
	}
	client := odoo.NewSingleAttemptClient(strings.TrimRight(target.Endpoint, "/"), target.Database, target.Login, target.Password, protocol)
	adapter := odoo.NewAdapter(odoo.NewClientTransport(client))
	adapter.RetryPolicy = odoo.DefaultRetryPolicy()
	// The standalone executor is the one place surface operations may run (surface_catalog.go).
	adapter.Surface = true
	return &Executor{
		client:   client,
		adapter:  adapter,
		route:    ProviderRoute{ConnectionID: target.ProfileName, Route: "direct_https", Resolution: ResolutionLocalProfile},
		endpoint: target.Endpoint,
	}, nil
}

// SetWebPassword sets the password a web session logs in with to render a REPORT operation's PDF:
// Odoo serves reports only to a logged-in web session and refuses API keys there.
func (e *Executor) SetWebPassword(password string) {
	if e.client != nil {
		e.client.SetWebPassword(password)
	}
}

func (e *Executor) SetRetryPolicy(policy odoo.RetryPolicy) {
	if e.adapter != nil {
		e.adapter.RetryPolicy = policy
	}
}

func (e *Executor) Catalog() []Operation { return Operations() }

func prepare(request Request) (odoo.Odoo19Operation, string, error) {
	entry, ok := odoo.LookupStandaloneOperation(request.Operation)
	if !ok {
		return entry, "", fmt.Errorf("%w: %q", ErrUnknownOperation, request.Operation)
	}
	mediation := "NONE"
	if request.ProviderTargetID != 0 {
		mediation = "UNMEDIATED_PROVIDER_TARGET"
	}
	switch odoo.TargetRequirementOf(entry) {
	case odoo.TargetRequirementRequired:
		if request.ProviderTargetID == 0 {
			return entry, "", fmt.Errorf("%w: %s", ErrTargetRequired, request.Operation)
		}
	case odoo.TargetRequirementNone:
		if request.ProviderTargetID != 0 {
			return entry, "", fmt.Errorf("%w: %s", ErrTargetNotAccepted, request.Operation)
		}
	}
	return entry, mediation, nil
}

func (e *Executor) Plan(ctx context.Context, request Request) (Plan, error) {
	entry, mediation, err := prepare(request)
	if err != nil {
		return Plan{}, err
	}
	readiness, _ := odoo.ReadinessOf(entry.SemanticID)
	input, err := encodeInput(request)
	if err != nil {
		return Plan{}, err
	}
	plan, err := e.adapter.Plan(ctx, &odoo.ExecutionIntent{ID: uuid.NewString(), Operation: entry.SemanticID, Input: input})
	if err != nil {
		return Plan{}, classifyExecutionError(entry.SemanticID, string(readiness), nil, err)
	}
	var proposed map[string]interface{}
	if err := json.Unmarshal([]byte(plan.ProposedInput), &proposed); err != nil {
		return Plan{}, errors.New("odoo direct: invalid planned input")
	}
	evidence, _ := odoo.Odoo19EvidenceClassOf(entry.SemanticID)
	return Plan{Operation: entry.SemanticID, Provider: plan.Provider, Resource: plan.Resource, Effect: plan.ExpectedEffect, Readiness: string(readiness), Input: proposed, ProviderRoute: e.route, RequiredCapability: string(plan.RequiredCapability), Risk: string(plan.Risk), ApprovalRequired: plan.Risk == odoo.RiskHigh || plan.Risk == odoo.RiskCritical || plan.ConfirmationRequired, VerificationStrategy: plan.VerificationStrategy, TargetMediation: mediation, EvidenceClass: string(evidence)}, nil
}

func (e *Executor) Execute(ctx context.Context, request Request) (Outcome, error) {
	entry, mediation, err := prepare(request)
	if err != nil {
		return Outcome{}, err
	}
	readiness, _ := odoo.ReadinessOf(entry.SemanticID)
	if readiness == odoo.ReadinessUnverified && !request.AllowUnverified {
		return Outcome{}, fmt.Errorf("%w: %s is %s", ErrOperationNotReleaseReady, entry.SemanticID, readiness)
	}
	input, err := encodeInput(request)
	if err != nil {
		return Outcome{}, err
	}
	// A candidate series has a dialect but no accepted evidence, so it is admitted on exactly the
	// acceptance that admits unverified operations, and what is reported is its own readiness.
	// The admission lasts for this request only.
	e.client.AdmitCandidateSeries(request.AllowUnverified)
	defer e.client.AdmitCandidateSeries(false)
	series, err := e.client.ProviderSeries(ctx)
	if err != nil {
		return Outcome{}, classifyExecutionError(entry.SemanticID, string(readiness), nil, err)
	}
	fit, err := e.operationFit(ctx, series, entry)
	if err != nil {
		return Outcome{}, classifyExecutionError(entry.SemanticID, string(readiness), nil, err)
	}
	switch {
	case fit.Verdict == odoo.FitRefused:
		return Outcome{}, classifyExecutionError(entry.SemanticID, string(readiness), nil,
			&odoo.RequiredModulesMissingError{Operation: entry.SemanticID, Required: entry.RequiredModules, Missing: fit.MissingModules})
	case fit.Verdict == odoo.FitUnverifiedHere && !request.AllowUnverified:
		return Outcome{}, classifyExecutionError(entry.SemanticID, string(readiness), nil,
			&odoo.EnvironmentUnverifiedError{Operation: entry.SemanticID, Reasons: fit.Reasons})
	}
	evidence, _ := odoo.Odoo19EvidenceClassOf(entry.SemanticID)
	if series != odoo.Odoo19ProviderSeries {
		readiness, _ = odoo.ReadinessFor(series, entry.SemanticID)
		evidence = odoo.Odoo19EvidenceOtherSeries
	}
	intent := &odoo.ExecutionIntent{ID: uuid.NewString(), Operation: entry.SemanticID, Input: input}
	result, err := e.adapter.Execute(ctx, intent)
	if err != nil {
		return Outcome{}, classifyExecutionError(entry.SemanticID, string(readiness), result, err)
	}
	out := Outcome{Operation: entry.SemanticID, Provider: result.Provider, Status: result.Status, Readiness: string(readiness), ProviderRoute: e.route, TargetMediation: mediation, EvidenceClass: string(evidence), Verification: result.VerificationStatus, ProviderReference: result.ProviderReference, Data: result.Data, ProviderSeries: series}
	if fit.Verdict != odoo.FitOK || len(fit.Notes) > 0 {
		out.ProviderFit = &fit
	}
	if request.Verify {
		out.Verification = e.verify(ctx, intent, result)
	}
	return out, nil
}

// operationFit observes the Odoo where the operation acts (DEC-2026-0157). Every operation needs
// the installed modules; one that changes records also reads what was changed on its models.
func (e *Executor) operationFit(ctx context.Context, series string, entry odoo.Odoo19Operation) (odoo.OperationFit, error) {
	version, err := e.client.ProviderVersion(ctx)
	if err != nil {
		return odoo.OperationFit{}, err
	}
	var models []string
	if odoo.ChangesRecords(entry) {
		models = odoo.FitModels(entry)
	}
	fit, err := odoo.FetchProviderFit(ctx, odoo.RetryReads(e.client, e.adapter.RetryPolicy), e.endpoint, odoo.ProviderVersionInfo{ServerVersion: version, ServerSerie: series}, models)
	if err != nil {
		return odoo.OperationFit{}, err
	}
	return fit.Assess(entry), nil
}

// ProviderFit observes the whole Odoo for the models the record-changing operations of the
// standalone catalog (curated and surface) act on, and assesses every operation against it: the
// report of `doctor --probe`.
func (e *Executor) ProviderFit(ctx context.Context) (odoo.ProviderFit, map[string]odoo.OperationFit, error) {
	series, err := e.client.ProviderSeries(ctx)
	if err != nil {
		return odoo.ProviderFit{}, nil, err
	}
	version, err := e.client.ProviderVersion(ctx)
	if err != nil {
		return odoo.ProviderFit{}, nil, err
	}
	var models []string
	entries := map[string]odoo.Odoo19Operation{}
	for _, operation := range AllOperations() {
		entry, ok := odoo.LookupStandaloneOperation(operation.ID)
		if !ok {
			continue
		}
		entries[operation.ID] = entry
		if odoo.ChangesRecords(entry) {
			for _, model := range odoo.FitModels(entry) {
				if !containsModel(models, model) {
					models = append(models, model)
				}
			}
		}
	}
	sort.Strings(models)
	fit, err := odoo.FetchProviderFit(ctx, odoo.RetryReads(e.client, e.adapter.RetryPolicy), e.endpoint, odoo.ProviderVersionInfo{ServerVersion: version, ServerSerie: series}, models)
	if err != nil {
		return odoo.ProviderFit{}, nil, err
	}
	verdicts := make(map[string]odoo.OperationFit, len(entries))
	for id, entry := range entries {
		verdicts[id] = fit.Assess(entry)
	}
	return fit, verdicts, nil
}

func containsModel(models []string, model string) bool {
	for _, known := range models {
		if known == model {
			return true
		}
	}
	return false
}

func encodeInput(request Request) (string, error) {
	payload := odoo.OperationInput{TargetID: request.ProviderTargetID, Values: request.Values, Domain: request.Domain, Fields: request.Fields, Order: request.Order, Offset: request.Offset, GroupBy: request.GroupBy, Aggregates: request.Aggregates, ConfirmImpact: request.ConfirmImpact, ProviderContext: request.ProviderContext}
	if request.Limit > 0 {
		payload.Limit = request.Limit
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("odoo direct: could not encode operation input")
	}
	return string(encoded), nil
}

func (e *Executor) verify(ctx context.Context, intent *odoo.ExecutionIntent, result *odoo.ExecutionResult) string {
	if !e.adapter.HasVerifiablePostcondition(intent) {
		return "NOT_APPLICABLE"
	}
	verified, err := e.adapter.Verify(ctx, intent, result)
	switch {
	case err != nil:
		return "VERIFICATION_FAILED"
	case verified:
		return "VERIFIED"
	default:
		return "MISMATCH"
	}
}

var missingObjectPattern = regexp.MustCompile(`object [a-z0-9_.]+ doesn't exist`)

func missingProviderModel(raw string) bool {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "record does not exist") || strings.Contains(lower, "no such record") {
		return false
	}
	hasModelReference := strings.Contains(lower, "model") || strings.Contains(lower, "object")
	hasMissingModelShape := strings.Contains(lower, "does not exist in registry") ||
		strings.Contains(lower, "doesn't exist in registry") ||
		strings.Contains(lower, "unknown model") ||
		// Odoo 20 phrasing: "Object account.group doesn't exist".
		missingObjectPattern.MatchString(lower)
	return hasModelReference && hasMissingModelShape
}

func classifyExecutionError(operation, readiness string, result *odoo.ExecutionResult, err error) error {
	raw := err.Error()
	class := string(odoo.ClassifyOdooError(raw))
	unreachable := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(raw), "deadline exceeded") ||
		strings.Contains(strings.ToLower(raw), "context canceled")
	if unreachable {
		class = "PROVIDER_UNREACHABLE"
	}
	structured := &OperationError{
		Operation: operation,
		Class:     class,
		Message:   raw,
		Detail:    map[string]interface{}{"readiness": readiness},
		cause:     err,
	}
	var semantic odoo.StructuredError
	if errors.As(err, &semantic) {
		structured.Class = semantic.ErrorClass()
		for key, value := range semantic.StructuredDetail() {
			structured.Detail[key] = value
		}
		return structured
	}
	if result != nil && result.ErrorClass != nil && *result.ErrorClass != "" {
		structured.Class = *result.ErrorClass
		if result.ProviderReadbackJSON != "" {
			providerDetail := map[string]interface{}{}
			if json.Unmarshal([]byte(result.ProviderReadbackJSON), &providerDetail) == nil {
				for key, value := range providerDetail {
					structured.Detail[key] = value
				}
			}
		}
	}
	if !unreachable && missingProviderModel(raw) {
		if entry, ok := odoo.LookupStandaloneOperation(operation); ok && entry.ProviderModel != "" {
			structured.Class = string(odoo.ErrClassModuleNotInstalled)
			structured.Message = "required Odoo module is not installed"
			structured.Detail["provider_model"] = entry.ProviderModel
			structured.Detail["required_modules"] = append([]string(nil), entry.RequiredModules...)
		}
	}
	return structured
}
