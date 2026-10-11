package odoo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// reportServer is Odoo's web client side: the version probe, a session login that sets a cookie for
// the right password, a report route served only to that session, and logout.
type reportServer struct {
	password  string
	redirect  bool // the report route sends the session to /web/login
	html      bool // the report route answers with a page, not a PDF
	mu        sync.Mutex
	logins    int
	logouts   int
	reports   []string
	passwords []string
}

func (s *reportServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var call struct {
			Params map[string]interface{} `json:"params"`
		}
		_ = json.Unmarshal(payload, &call)
		reply := func(result interface{}, failure map[string]interface{}) {
			answer := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result}
			if failure != nil {
				answer = map[string]interface{}{"jsonrpc": "2.0", "id": 1, "error": map[string]interface{}{"message": "Odoo Server Error", "data": failure}}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(answer)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == "/web/webclient/version_info", r.URL.Path == "/jsonrpc" && call.Params["method"] == "version":
			reply(map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}, nil)
		case r.URL.Path == "/web/session/authenticate":
			s.logins++
			password, _ := call.Params["password"].(string)
			s.passwords = append(s.passwords, password)
			if password != s.password {
				reply(nil, map[string]interface{}{"name": "odoo.exceptions.AccessDenied", "message": "Access Denied"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "live", Path: "/"})
			reply(map[string]interface{}{"uid": 2.0}, nil)
		case r.URL.Path == "/web/session/destroy":
			s.logouts++
			reply(true, nil)
		case strings.HasPrefix(r.URL.Path, "/report/pdf/"):
			s.reports = append(s.reports, r.URL.Path)
			if cookie, err := r.Cookie("session_id"); err != nil || cookie.Value != "live" || s.redirect {
				http.Redirect(w, r, "/web/login?redirect="+r.URL.Path, http.StatusSeeOther)
				return
			}
			if s.html {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<!DOCTYPE html><html>login</html>"))
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 report"))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func reportClient(t *testing.T, s *reportServer, credential string, protocol Protocol) *RPCClient {
	t.Helper()
	server := httptest.NewServer(s.handler(t))
	t.Cleanup(server.Close)
	return NewSingleAttemptClient(server.URL, "lab", "admin", credential, protocol)
}

// A report is rendered the way the web client's Print menu does it: a web session logs in, asks for
// the report and logs out.
func TestAReportIsRenderedThroughAWebSession(t *testing.T) {
	s := &reportServer{password: "secret"}
	pdf, err := reportClient(t, s, "secret", ProtocolJSONRPC).RenderReport(context.Background(), "sale.report_saleorder", []int64{7})
	if err != nil {
		t.Fatal(err)
	}
	if string(pdf) != "%PDF-1.4 report" || s.logins != 1 || s.logouts != 1 || strings.Join(s.reports, ",") != "/report/pdf/sale.report_saleorder/7" {
		t.Fatalf("pdf %q logins %d logouts %d reports %v", pdf, s.logins, s.logouts, s.reports)
	}

	// A JSON-2 client holds an API key, which Odoo refuses for a web session: it needs a web password,
	// and without one it sends nothing.
	keyOnly := &reportServer{password: "secret"}
	_, err = reportClient(t, keyOnly, "api-key", ProtocolJSON2).RenderReport(context.Background(), "sale.report_saleorder", []int64{7})
	var session *WebSessionError
	if !errors.As(err, &session) || keyOnly.logins != 0 {
		t.Fatalf("err %v after %d logins: want a refusal before any login", err, keyOnly.logins)
	}
	withWeb := &reportServer{password: "secret"}
	client := reportClient(t, withWeb, "api-key", ProtocolJSON2)
	client.SetWebPassword("secret")
	if _, err := client.RenderReport(context.Background(), "sale.report_saleorder", []int64{7}); err != nil || strings.Join(withWeb.passwords, ",") != "secret" {
		t.Fatalf("err %v, logged in with %v: want the web password, never the API key", err, withWeb.passwords)
	}
}

// Anything but the report is refused: a refused login, the login page, a page instead of a PDF, a
// report name that could leave the report route.
func TestAReportIsRefusedWhenOdooDoesNotAnswerWithIt(t *testing.T) {
	for name, s := range map[string]*reportServer{
		"wrong password": {password: "other"},
		"login page":     {password: "secret", redirect: true},
		"a page":         {password: "secret", html: true},
	} {
		_, err := reportClient(t, s, "secret", ProtocolJSONRPC).RenderReport(context.Background(), "sale.report_saleorder", []int64{7})
		var session *WebSessionError
		if !errors.As(err, &session) || session.ErrorClass() != string(ErrClassAccessDenied) {
			t.Fatalf("%s: want a web session refusal, got %v", name, err)
		}
	}
	s := &reportServer{password: "secret"}
	if _, err := reportClient(t, s, "secret", ProtocolJSONRPC).RenderReport(context.Background(), "../../web/database/manager", []int64{7}); err == nil || s.logins != 0 {
		t.Fatalf("a report name outside the report route was asked for (err %v, logins %d)", err, s.logins)
	}
}

// renderingTransport is a fakeTransport that renders reports.
type renderingTransport struct {
	fakeTransport
	rendered []string
}

func (r *renderingTransport) RenderReport(_ context.Context, reportName string, ids []int64) ([]byte, error) {
	r.rendered = append(r.rendered, reportName)
	return []byte("%PDF-1.4 invoice"), nil
}

// A print renders the operation's report for an eligible target, named after the record.
func TestAPrintReturnsTheRecordsReport(t *testing.T) {
	tr := &renderingTransport{}
	tr.searchRead = func(string, interface{}, []string) ([]map[string]interface{}, error) {
		return []map[string]interface{}{{"id": 165.0}}, nil
	}
	tr.read = func(string, []int64) ([]map[string]interface{}, error) {
		return []map[string]interface{}{{"id": 165.0, "display_name": "INV/2026/00017"}}, nil
	}
	res, err := surfaceRun(tr, "finance.invoice.print", `{"target_id":165}`)
	if err != nil {
		t.Fatal(err)
	}
	data := res.Data.(map[string]interface{})
	if data["filename"] != "INV_2026_00017.pdf" || data["content"] != "JVBERi0xLjQgaW52b2ljZQ==" || data["size"] != 16 || strings.Join(tr.rendered, ",") != "account.report_invoice_with_payments" {
		t.Fatalf("printed %v via %v", data, tr.rendered)
	}
	outside := &renderingTransport{}
	if _, err := surfaceRun(outside, "finance.invoice.print", `{"target_id":173}`); err == nil || len(outside.rendered) != 0 {
		t.Fatal("a record outside the invoices was printed")
	}
}
