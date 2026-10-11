package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Reports. Odoo renders a record's PDF report only for a logged-in web session (/report/pdf/...,
// auth="user"): an API key, as a bearer token or in basic auth, is answered with the login page, and
// the render method (ir.actions.report._render_qweb_pdf) is private, so no RPC reaches it.
// RenderReport does what the web client's Print menu does: it logs a web session in with the user's
// password, asks for the report, and logs the session out.

// ReportRenderer is a transport that can render a record's PDF report. REPORT operations need it;
// ClientTransport is one.
type ReportRenderer interface {
	RenderReport(ctx context.Context, reportName string, ids []int64) ([]byte, error)
}

// maxReportBytes bounds a rendered report read into memory.
const maxReportBytes = 64 << 20

// reportNamePattern is a report's technical name (module.report_name): nothing that can leave the
// report route.
var reportNamePattern = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_.]+$`)

// WebSessionError is a report that could not be rendered for a reason of the web session: no web
// password, a refused login, or an answer that is not the report (the login page).
type WebSessionError struct {
	Reason string
}

func (e *WebSessionError) Error() string { return "odoo report: " + e.Reason }

func (e *WebSessionError) ErrorClass() string { return string(ErrClassAccessDenied) }

func (e *WebSessionError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"reason": e.Reason}
}

// SetWebPassword sets the password a web session logs in with to render a report.
func (c *RPCClient) SetWebPassword(password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.webPassword = password
}

// RenderReport renders reportName for ids through a web session and returns the PDF.
func (c *RPCClient) RenderReport(ctx context.Context, reportName string, ids []int64) ([]byte, error) {
	if !reportNamePattern.MatchString(reportName) || len(ids) == 0 {
		return nil, fmt.Errorf("odoo report: %q for %v is not a report request", reportName, ids)
	}
	// The series gate runs first, so a provider this client may not execute against never receives
	// a password.
	c.mu.Lock()
	_, err := c.admittedSeriesLocked(ctx)
	password := c.webPassword
	if password == "" && c.protocol != ProtocolJSON2 {
		password = c.password
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, &WebSessionError{Reason: "printing needs a web password: Odoo serves reports only to a logged-in web session and refuses API keys there"}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	web := *c.httpClientFor(ctx)
	web.Jar = jar
	// A redirect from the report route is the login page: the session was not accepted.
	web.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	login, err := webCall(ctx, &web, c.url+"/web/session/authenticate", map[string]interface{}{"db": c.db, "login": c.login, "password": password})
	if err != nil {
		return nil, err
	}
	if uid, _ := login["uid"].(float64); uid <= 0 {
		return nil, &WebSessionError{Reason: "the web login was refused"}
	}
	defer func() {
		_, _ = webCall(context.WithoutCancel(ctx), &web, c.url+"/web/session/destroy", map[string]interface{}{})
	}()

	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/report/pdf/"+url.PathEscape(reportName)+"/"+strings.Join(parts, ","), nil)
	if err != nil {
		return nil, err
	}
	resp, err := web.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReportBytes+1))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, &WebSessionError{Reason: "Odoo sent the report request to its login page: the web session was not accepted"}
	case resp.StatusCode != http.StatusOK:
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	case len(body) > maxReportBytes:
		return nil, fmt.Errorf("odoo report: %s is larger than %d bytes", reportName, maxReportBytes)
	case !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/pdf") || !bytes.HasPrefix(body, []byte("%PDF")):
		return nil, &WebSessionError{Reason: fmt.Sprintf("Odoo answered %s with %q, not a PDF", reportName, resp.Header.Get("Content-Type"))}
	}
	return body, nil
}

// webCall is one JSON-RPC call to a web client route, in the session web holds.
func webCall(ctx context.Context, web *http.Client, route string, params map[string]interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(jsonrpcRequest{JSONRPC: "2.0", Method: "call", Params: params, ID: 1})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, route, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := web.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	var answer struct {
		Result interface{} `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer); err != nil {
		return nil, fmt.Errorf("odoo report: undecodable answer from %s", route)
	}
	if answer.Error != nil {
		if strings.Contains(answer.Error.Data.Name, "AccessDenied") {
			return nil, &WebSessionError{Reason: "the web login was refused"}
		}
		return nil, &OdooError{Code: http.StatusOK, Message: answer.Error.Data.Name, Detail: answer.Error.Data.Message}
	}
	result, _ := answer.Result.(map[string]interface{})
	return result, nil
}

// RenderReport renders a report through the client's web session (ReportRenderer).
func (t *ClientTransport) RenderReport(ctx context.Context, reportName string, ids []int64) ([]byte, error) {
	return t.Client.RenderReport(ctx, reportName, ids)
}
