package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type longCallKey struct{}

// WithLongCall returns a context configured with a per-call timeout override (up to 110s max).
// only the module_install strategy uses this; every other operation keeps the 20s bound.
func WithLongCall(ctx context.Context, d time.Duration) context.Context {
	if d > 110*time.Second {
		d = 110 * time.Second
	}
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < d {
			d = remaining
		}
	}
	return context.WithValue(ctx, longCallKey{}, d)
}

func longCallTimeout(ctx context.Context) (time.Duration, bool) {
	if ctx == nil {
		return 0, false
	}
	d, ok := ctx.Value(longCallKey{}).(time.Duration)
	return d, ok
}

// Protocol is how a client speaks to Odoo.
type Protocol string

const (
	// ProtocolJSONRPC is Odoo's classic external API: /jsonrpc, execute_kw, a login with a password
	// or an API key. Odoo 19.0 deprecated it; Odoo Online removes it in 21.1 and on-premise Odoo in 22.
	ProtocolJSONRPC Protocol = "jsonrpc"
	// ProtocolJSON2 is the API that replaces it, from Odoo 19.0 on: /json/2/<model>/<method>, an API
	// key as a bearer token, the database in the X-Odoo-Database header, named arguments only.
	ProtocolJSON2 Protocol = "json2"
)

// ParseProtocol reads a protocol name; empty means the classic JSON-RPC.
func ParseProtocol(name string) (Protocol, error) {
	switch Protocol(strings.TrimSpace(name)) {
	case "", ProtocolJSONRPC:
		return ProtocolJSONRPC, nil
	case ProtocolJSON2:
		return ProtocolJSON2, nil
	}
	return "", fmt.Errorf("odoo: unknown protocol %q (want %s or %s)", name, ProtocolJSONRPC, ProtocolJSON2)
}

// RPCClient is a self-contained classic Odoo JSON-RPC (execute_kw) client.
//
// It exists instead of depending on
// internal/infrastructure/odoo.Client for two independent reasons:
//
//  1. Filesystem ACL: that package's directory is owned by a different OS
//     account in this worktree and this session has read-only access to it
//     (see the AERP Odoo19 milestone final report, section N).
//  2. A real defect discovered empirically while building this milestone:
//     that client's error path only surfaces rpcResp.Error.Message, which
//     Odoo populates with the generic string "Odoo Server Error" for
//     internal exceptions — the actual business-rule text (e.g. "How about
//     archiving the record instead?", "zero quantity transfer") lives in
//     rpcResp.Error.Data.Message/Arguments, which that client discards.
//     Governed error classification (domain.ClassifyOdooError) is useless
//     without that detail, so this client preserves it.
type RPCClient struct {
	url, db, login, password string
	httpClient               *http.Client
	mu                       sync.RWMutex
	uid                      int64
	singleAttempt            bool
	// versionInfo is the provider's answer to common.version, asked once per client and before
	// the first login (provider_series.go): a server does not change series under a live session.
	versionInfo *ProviderVersionInfo
	// admitCandidates lets this client execute against a candidate series; see AdmitCandidateSeries.
	admitCandidates bool
	// protocol is how this client speaks to Odoo (rpc_json2.go for JSON-2); the password is then the
	// API key.
	protocol Protocol
	// webPassword is what a web session logs in with to render a report (rpc_report.go): Odoo refuses
	// API keys there. Empty, a JSON-RPC client logs in with its own password.
	webPassword string
}

func NewRPCClient(url, db, login, password string) *RPCClient {
	return &RPCClient{url: url, db: db, login: login, password: password, httpClient: &http.Client{Timeout: 20 * time.Second}, protocol: ProtocolJSONRPC}
}

// NewSingleAttemptRPCClient uses the shared RPC implementation for direct clients.
// It never repeats an execute_kw call, rejects redirects, and redacts credential
// material from provider diagnostics and returned data. Governed clients retain
// their existing constructor and behavior.
func NewSingleAttemptRPCClient(url, db, login, password string) *RPCClient {
	c := NewRPCClient(url, db, login, password)
	c.singleAttempt = true
	c.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return fmt.Errorf("odoo direct: redirects are not allowed")
	}
	return c
}

// NewSingleAttemptClient is NewSingleAttemptRPCClient speaking the given protocol. With JSON-2 the
// credential is an API key.
func NewSingleAttemptClient(url, db, login, credential string, protocol Protocol) *RPCClient {
	c := NewSingleAttemptRPCClient(url, db, login, credential)
	c.protocol = protocol
	return c
}

// Protocol reports how this client speaks to Odoo.
func (c *RPCClient) Protocol() Protocol { return c.protocol }

// redactValue walks decoded JSON, including keys, before it leaves the client.
func (c *RPCClient) redactValue(v interface{}) interface{} {
	switch value := v.(type) {
	case string:
		return strings.ReplaceAll(value, c.password, "[REDACTED]")
	case []interface{}:
		for i := range value {
			value[i] = c.redactValue(value[i])
		}
		return value
	case map[string]interface{}:
		out := make(map[string]interface{}, len(value))
		for k, item := range value {
			out[strings.ReplaceAll(k, c.password, "[REDACTED]")] = c.redactValue(item)
		}
		return out
	default:
		return v
	}
}

type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

type jsonrpcErrorData struct {
	Name      string        `json:"name"`
	Debug     string        `json:"debug"`
	Message   string        `json:"message"`
	Arguments []interface{} `json:"arguments"`
}

type jsonrpcError struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    jsonrpcErrorData `json:"data"`
}

type jsonrpcResponse struct {
	Result interface{}   `json:"result"`
	Error  *jsonrpcError `json:"error"`
}

// OdooError carries the FULL provider error text so
// domain.ClassifyOdooError has something real to match against, plus the
// raw fields for diagnostics/logging.
type OdooError struct {
	Code    int
	Message string
	Detail  string // Data.Message, or Data.Arguments[0], or Message as last resort
}

func (e *OdooError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return e.Message
}

// HTTPStatusError represents an unexpected HTTP status from the Odoo endpoint or reverse proxy.
// It never includes the response body, preventing diagnostic leakage.
type HTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *HTTPStatusError) Error() string {
	if e.Status != "" {
		return fmt.Sprintf("odoo transport: HTTP %d (%s)", e.StatusCode, e.Status)
	}
	return fmt.Sprintf("odoo transport: HTTP %d", e.StatusCode)
}

type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

// guard redacts credential material from what a direct client returns, result and error alike.
func (c *RPCClient) guard(result interface{}, callErr error) (interface{}, error) {
	if !c.singleAttempt {
		return result, callErr
	}
	if callErr != nil {
		var providerErr *OdooError
		var httpErr *HTTPStatusError
		if errors.As(callErr, &providerErr) {
			providerErr.Message = c.redactValue(providerErr.Message).(string)
			providerErr.Detail = c.redactValue(providerErr.Detail).(string)
		} else if errors.As(callErr, &httpErr) {
			// Status error contains only code and status text, no credential material.
		} else {
			callErr = &redactedError{
				msg:   c.redactValue(callErr.Error()).(string),
				cause: callErr,
			}
		}
	}
	return c.redactValue(result), callErr
}

// httpClientFor is the HTTP client for one call: the module install strategy may lengthen it.
func (c *RPCClient) httpClientFor(ctx context.Context) *http.Client {
	client := c.httpClient
	if d, ok := longCallTimeout(ctx); ok && client != nil {
		shallow := *client
		shallow.Timeout = d
		client = &shallow
	}
	return client
}

func (c *RPCClient) call(ctx context.Context, params interface{}) (interface{}, error) {
	return c.guard(c.postJSONRPC(ctx, "/jsonrpc", params))
}

// postJSONRPC sends one JSON-RPC 2.0 call to a route: /jsonrpc, or a web client route such as
// /web/webclient/version_info.
func (c *RPCClient) postJSONRPC(ctx context.Context, route string, params interface{}) (interface{}, error) {
	body, err := json.Marshal(jsonrpcRequest{JSONRPC: "2.0", Method: "call", Params: params, ID: 1})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+route, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClientFor(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	var rpcResp jsonrpcResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&rpcResp); decErr != nil {
		return nil, fmt.Errorf("failed to decode odoo response: %w", decErr)
	}
	if rpcResp.Error != nil {
		detail := rpcResp.Error.Data.Message
		if detail == "" && len(rpcResp.Error.Data.Arguments) > 0 {
			if s, ok := rpcResp.Error.Data.Arguments[0].(string); ok {
				detail = s
			}
		}
		if detail == "" {
			detail = rpcResp.Error.Message
		}
		return nil, &OdooError{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message, Detail: detail}
	}
	// A missing "result" key (None/void Python return) decodes to a nil
	// interface{} with NO error — this is the concrete fix for the
	// empirically-observed defect where a void-returning method (e.g.
	// crm.lead.action_set_lost) broke a stricter response parser elsewhere.
	return rpcResp.Result, nil
}

// ProviderVersionInfo asks the provider which build and series it runs. common.version needs no
// authentication, which is what lets the series gate run before any credential is sent.
func (c *RPCClient) ProviderVersionInfo(ctx context.Context) (ProviderVersionInfo, error) {
	var res interface{}
	var err error
	if c.protocol == ProtocolJSON2 {
		// The web client's own route answers the same without a credential, and is not part of
		// the external API Odoo is removing.
		res, err = c.guard(c.postJSONRPC(ctx, "/web/webclient/version_info", map[string]interface{}{}))
	} else {
		res, err = c.call(ctx, map[string]interface{}{"service": "common", "method": "version", "args": []interface{}{}})
	}
	if err != nil {
		if refusal, ok := notOdoo(err); ok {
			return ProviderVersionInfo{}, refusal
		}
		return ProviderVersionInfo{}, fmt.Errorf("failed to call provider version: %w", err)
	}

	resultMap, ok := res.(map[string]interface{})
	if !ok {
		return ProviderVersionInfo{}, &ProviderNotOdooError{Detail: fmt.Sprintf("the version answer is a %T, not Odoo's version record", res)}
	}

	var info ProviderVersionInfo
	info.ServerVersion, _ = resultMap["server_version"].(string)
	info.ServerSerie, _ = resultMap["server_serie"].(string)
	if strings.TrimSpace(info.ServerVersion) == "" && strings.TrimSpace(info.ServerSerie) == "" {
		return ProviderVersionInfo{}, &ProviderNotOdooError{Detail: "the version answer names no Odoo version"}
	}
	return info, nil
}

// versionInfoLocked returns the cached common.version answer, asking for it on first use. The
// caller holds c.mu.
func (c *RPCClient) versionInfoLocked(ctx context.Context) (ProviderVersionInfo, error) {
	if c.versionInfo == nil {
		info, err := c.ProviderVersionInfo(ctx)
		if err != nil {
			return ProviderVersionInfo{}, err
		}
		c.versionInfo = &info
	}
	return *c.versionInfo, nil
}

// admittedSeriesLocked returns the provider series when this client may execute against it, and
// the series gate's refusal otherwise. The caller holds c.mu.
func (c *RPCClient) admittedSeriesLocked(ctx context.Context) (string, error) {
	info, err := c.versionInfoLocked(ctx)
	if err != nil {
		return "", err
	}
	if err := checkProviderSeriesAdmission(info, c.admitCandidates); err != nil {
		return "", err
	}
	return info.Series(), nil
}

// ProviderSeries returns the series of the provider this client may execute against, asking the
// provider once and without sending the credential. It returns the series gate's refusal for a
// series this client may not execute against.
func (c *RPCClient) ProviderSeries(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.admittedSeriesLocked(ctx)
}

// AdmitCandidateSeries lets this client execute against a candidate series
// (CandidateProviderSeries): one with a dialect but no accepted conformance evidence. Only a
// caller that has made the operator accept unverified results (or that is gathering the
// evidence) may admit candidates. The governed product never does.
func (c *RPCClient) AdmitCandidateSeries(admit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.admitCandidates = admit
}

func (c *RPCClient) ProviderVersion(ctx context.Context) (string, error) {
	c.mu.Lock()
	info, err := c.versionInfoLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return "", err
	}

	if info.ServerVersion != "" {
		return info.ServerVersion, nil
	}

	if info.ServerSerie != "" {
		return info.ServerSerie, nil
	}

	return "", fmt.Errorf("provider version not found in response")
}

func (c *RPCClient) authenticate(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// The series gate runs BEFORE the login call, so a provider this client may not execute
	// against never receives the credential at all (provider_series.go).
	if _, err := c.admittedSeriesLocked(ctx); err != nil {
		return 0, err
	}
	res, err := c.call(ctx, map[string]interface{}{
		"service": "common", "method": "login",
		"args": []interface{}{c.db, c.login, c.password},
	})
	if err != nil {
		return 0, err
	}
	f, ok := res.(float64)
	if !ok || f == 0 {
		return 0, fmt.Errorf("odoo authentication failed for login %q", c.login)
	}
	c.uid = int64(f)
	return c.uid, nil
}

// ExecuteKw is the single generic RPC primitive; every higher-level
// operation in transport.go composes calls to this. kwargs["context"], when
// set, is forwarded to Odoo's execute_kw untouched — empirically verified
// live against this worktree's Odoo 19 instance to propagate correctly
// (archived-record visibility toggled via context.active_test).
func (c *RPCClient) ExecuteKw(ctx context.Context, model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	if c.protocol == ProtocolJSON2 {
		return c.executeJSON2(ctx, model, method, args, kwargs)
	}
	c.mu.RLock()
	uid := c.uid
	c.mu.RUnlock()
	if uid == 0 {
		var err error
		if uid, err = c.authenticate(ctx); err != nil {
			return nil, err
		}
	}
	if kwargs == nil {
		kwargs = map[string]interface{}{}
	}
	if args == nil {
		args = []interface{}{}
	}
	res, err := c.call(ctx, map[string]interface{}{
		"service": "object", "method": "execute_kw",
		"args": []interface{}{c.db, uid, c.password, model, method, args, kwargs},
	})
	if err != nil {
		if !c.singleAttempt && isAuthError(err) {
			// Session/uid may have gone stale; re-authenticate once.
			if newUID, loginErr := c.authenticate(ctx); loginErr == nil {
				return c.call(ctx, map[string]interface{}{
					"service": "object", "method": "execute_kw",
					"args": []interface{}{c.db, newUID, c.password, model, method, args, kwargs},
				})
			}
		}
		return nil, err
	}
	return res, nil
}

func isAuthError(err error) bool {
	var oe *OdooError
	return errors.As(err, &oe) && (oe.Code == 100 || oe.Message == "Odoo Session Expired")
}
