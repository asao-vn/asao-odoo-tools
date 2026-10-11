package standalone_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo"
)

// odoo20Fake is an Odoo 20.0 server as the series gate sees it: it answers common.version and
// records every call, so a test can prove the credential was never sent to it.
type odoo20Fake struct {
	mu      sync.Mutex
	methods []string
	bodies  []string
}

func newOdoo20Fake(t *testing.T) (*odoo20Fake, *httptest.Server) {
	t.Helper()
	fake := &odoo20Fake{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var call struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &call)
		fake.mu.Lock()
		fake.methods = append(fake.methods, call.Params.Method)
		fake.bodies = append(fake.bodies, string(raw))
		fake.mu.Unlock()
		if call.Params.Method == "version" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}})
			return
		}
		// Anything else would be a gate failure; answer like a working Odoo so the test, not
		// this fake, is what notices.
		if call.Params.Method == "login" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
	}))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *odoo20Fake) assertNeverSawTheCredential(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, method := range f.methods {
		if method != "version" {
			t.Fatalf("calls = %v: only common.version may reach an unsupported series", f.methods)
		}
	}
	for _, body := range f.bodies {
		if strings.Contains(body, "private-secret-pass") {
			t.Fatal("the credential reached an Odoo whose series was refused")
		}
	}
}

func TestExecuteRefusesAnUnsupportedOdooSeries(t *testing.T) {
	fake, server := newOdoo20Fake(t)
	writeTestProfile(t, server.URL)

	code, out, _ := runOdoo(t, "execute", "--profile", "lab-a", "--operation", "identity.contact.list", "--limit", "1", "--output", "json")
	if code != clicore.ExitConfig {
		t.Fatalf("exit = %d, want %d (CONFIG): %s", code, clicore.ExitConfig, out)
	}
	errObj := decodeError(t, out)
	if errObj["code"] != "AERP_PROVIDER_VERSION_UNSUPPORTED" {
		t.Fatalf("code = %v, want AERP_PROVIDER_VERSION_UNSUPPORTED", errObj["code"])
	}
	errContext, _ := errObj["context"].(map[string]interface{})
	if errContext["provider_series"] != "20.0" || errContext["provider_version"] != "20.0-20260926" {
		t.Fatalf("context = %v", errContext)
	}
	if supported, _ := errContext["supported_series"].([]interface{}); len(supported) != 1 || supported[0] != "19.0" {
		t.Fatalf("supported_series = %v", errContext["supported_series"])
	}
	if retryable, _ := errObj["retryable"].(bool); retryable {
		t.Fatal("a series refusal must not be retryable")
	}
	if errContext["candidate"] != true {
		t.Fatalf("context = %v: 20.0 is a candidate series and the refusal must say so", errContext)
	}
	fake.assertNeverSawTheCredential(t)
}

// TestExecuteRunsACandidateSeriesWhenUnverifiedResultsAreAccepted: --allow-unverified is the
// operator's acceptance of results without conformance evidence, and it is what admits a candidate
// series. The answer must say which series produced it and that it is UNVERIFIED there:
// finance.bank_statement.list is CURRENT_VERIFIED on 19.0 but has no 20.0 record, so 19.0's evidence
// leaking into a 20.0 answer would show.
func TestExecuteRunsACandidateSeriesWhenUnverifiedResultsAreAccepted(t *testing.T) {
	if baseline, _ := odoo.ReadinessOf("finance.bank_statement.list"); baseline != odoo.ReadinessCurrentVerified {
		t.Fatalf("test premise: finance.bank_statement.list must be CURRENT_VERIFIED on 19.0; it is %s", baseline)
	}
	if candidate, _ := odoo.ReadinessFor(odoo.Odoo20ProviderSeries, "finance.bank_statement.list"); candidate != odoo.ReadinessUnverified {
		t.Fatalf("test premise: finance.bank_statement.list must have no 20.0 record; it is %s there, pick another operation", candidate)
	}
	_, server := newOdoo20Fake(t)
	writeTestProfile(t, server.URL)

	code, out, diag := runOdoo(t, "execute", "--profile", "lab-a", "--operation", "finance.bank_statement.list", "--limit", "1", "--allow-unverified", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit = %d: %s\n%s", code, out, diag)
	}
	data := decode(t, out)
	if data["providerSeries"] != "20.0" || data["readiness"] != "UNVERIFIED" || data["evidenceClass"] != "OTHER_SERIES" {
		t.Fatalf("providerSeries=%v readiness=%v evidenceClass=%v", data["providerSeries"], data["readiness"], data["evidenceClass"])
	}
}

func TestDoctorProbeReportsAnUnsupportedOdooSeriesAsNotReady(t *testing.T) {
	fake, server := newOdoo20Fake(t)
	writeTestProfile(t, server.URL)

	code, out, diag := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("doctor failed: %s\n%s", out, diag)
	}
	data := decode(t, out)
	if data["ready"] != false {
		t.Fatalf("ready = %v, want false", data["ready"])
	}
	selection, _ := data["selection"].(map[string]interface{})
	probe, _ := selection["probe"].(map[string]interface{})
	if probe["status"] != "FAILED" || probe["class"] != "PROVIDER_VERSION_UNSUPPORTED" || probe["providerSeries"] != "20.0" {
		t.Fatalf("probe = %v, want FAILED / PROVIDER_VERSION_UNSUPPORTED / 20.0 (never OK_CREDENTIAL_ACCEPTED)", probe)
	}
	capability, _ := probe["capabilityProbe"].(map[string]interface{})
	if capability["class"] != "PROVIDER_VERSION_UNSUPPORTED" || capability["providerVersion"] != "20.0-20260926" {
		t.Fatalf("capabilityProbe = %v", capability)
	}
	fake.assertNeverSawTheCredential(t)
}
