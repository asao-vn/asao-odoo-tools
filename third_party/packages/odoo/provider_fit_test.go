package odoo

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fitOdoo answers the provider-fit check's reads from fixed facts, as the Odoo would to one login.
type fitOdoo struct {
	modules     map[string]string                 // installed module -> license
	extenders   map[string]string                 // model -> ir.model.modules
	automations []map[string]interface{}          // active base.automation rows
	fields      map[string]map[string]interface{} // model -> fields_get answer
	denied      map[string]bool                   // models this login may not read
	reads       []string
}

func (f *fitOdoo) ExecuteKw(_ context.Context, model, method string, args []interface{}, _ map[string]interface{}) (interface{}, error) {
	f.reads = append(f.reads, model+"."+method)
	if f.denied[model] {
		return nil, errors.New("You are not allowed to access '" + model + "' records.")
	}
	switch {
	case model == "ir.module.module":
		rows := []interface{}{}
		for name, license := range f.modules {
			rows = append(rows, map[string]interface{}{"name": name, "license": license})
		}
		return rows, nil
	case model == "ir.model":
		domain := args[0].([]interface{})
		wanted := domain[0].([]interface{})[2].([]string)
		rows := []interface{}{}
		for _, m := range wanted {
			if listed, ok := f.extenders[m]; ok {
				rows = append(rows, map[string]interface{}{"model": m, "modules": listed})
			}
		}
		return rows, nil
	case model == "base.automation":
		rows := []interface{}{}
		for _, row := range f.automations {
			rows = append(rows, row)
		}
		return rows, nil
	case method == "fields_get":
		if answer, ok := f.fields[model]; ok {
			return answer, nil
		}
		return map[string]interface{}{}, nil
	}
	return nil, errors.New("unexpected read " + model + "." + method)
}

func stock(extra map[string]string) map[string]string {
	modules := map[string]string{"base": "LGPL-3", "contacts": "LGPL-3", "sale": "LGPL-3", "account": "LGPL-3"}
	for name, license := range extra {
		modules[name] = license
	}
	return modules
}

func operation(t *testing.T, id string) Odoo19Operation {
	t.Helper()
	op, ok := LookupStandaloneOperation(id)
	if !ok {
		t.Fatalf("test premise: %s is not in the standalone catalog", id)
	}
	return op
}

func observe(t *testing.T, odoo *fitOdoo, version, endpoint string, ops ...Odoo19Operation) ProviderFit {
	t.Helper()
	var models []string
	for _, op := range ops {
		if ChangesRecords(op) {
			models = append(models, FitModels(op)...)
		}
	}
	fit, err := FetchProviderFit(context.Background(), odoo, endpoint, ProviderVersionInfo{ServerVersion: version, ServerSerie: "19.0"}, models)
	if err != nil {
		t.Fatal(err)
	}
	return fit
}

func TestClassifyModuleAgainstTheStockImage(t *testing.T) {
	for _, tc := range []struct {
		series, name, license string
		want                  ModuleOrigin
	}{
		{"19.0", "sale", "LGPL-3", ModuleStandard},
		{"19.0", "helpdesk", "LGPL-3", ModuleEnterprise}, // an app the Community image only advertises
		{"19.0", "web_enterprise", "OEEL-1", ModuleEnterprise},
		{"19.0", "partner_firstname", "AGPL-3", ModuleThirdParty}, // an OCA module
		{"19.0", "sale", "OPL-1", ModuleStandard},                 // a name of the image is the image's
		{"15.0", "sale", "LGPL-3", ModuleThirdParty},              // no list for the series: ask
	} {
		if got := ClassifyModule(tc.series, tc.name, tc.license); got != tc.want {
			t.Errorf("%s %s (%s) = %s, want %s", tc.series, tc.name, tc.license, got, tc.want)
		}
	}
	if len(StandardModules("19.0")) < 600 || len(StandardModules("20.0")) < 600 || StandardModules("15.0") != nil {
		t.Fatal("the recorded stock module lists are missing")
	}
}

// A stock Community database fits every operation, and the check reads no more than it must: the
// modules, and the hand-added fields of the models a write acts on.
func TestAStockCommunityOdooFitsEveryOperation(t *testing.T) {
	odoo := &fitOdoo{modules: stock(nil)}
	create := operation(t, "identity.contact.create")
	fit := observe(t, odoo, "19.0-20260803", "http://127.0.0.1:8069", create)
	if fit.Edition != EditionCommunity || fit.Hosting != HostingElsewhere || fit.StandardModules != 4 || len(fit.ThirdPartyModules) != 0 || fit.Automations {
		t.Fatalf("fit = %+v", fit)
	}
	for _, id := range []string{"identity.contact.create", "identity.contact.list"} {
		if verdict := fit.Assess(operation(t, id)); verdict.Verdict != FitOK || len(verdict.Reasons)+len(verdict.Notes) != 0 {
			t.Fatalf("%s: %+v", id, verdict)
		}
	}
	if want := []string{"ir.module.module.search_read", "res.partner.fields_get"}; !reflect.DeepEqual(odoo.reads, want) {
		t.Fatalf("reads = %v, want %v", odoo.reads, want)
	}
}

func TestAMissingModuleRefusesTheOperationsThatNeedIt(t *testing.T) {
	fit := observe(t, &fitOdoo{modules: map[string]string{"base": "LGPL-3"}}, "19.0", "http://odoo.internal")
	verdict := fit.Assess(operation(t, "sales.quotation.list"))
	if verdict.Verdict != FitRefused || !reflect.DeepEqual(verdict.MissingModules, []string{"sale"}) {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict := fit.Assess(operation(t, "identity.contact.list")); verdict.Verdict != FitOK {
		t.Fatalf("an operation whose modules are installed was held back: %+v", verdict)
	}
}

// Enterprise is recognised by its modules (or its build), and where its modules extend a model the
// writes there are not verified: the evidence was recorded on Community.
func TestEnterpriseIsRecognisedAndItsExtensionsAreNotVerified(t *testing.T) {
	odoo := &fitOdoo{
		modules:   stock(map[string]string{"web_enterprise": "OEEL-1", "account_accountant": "OEEL-1"}),
		extenders: map[string]string{"account.move": "account, account_accountant"},
	}
	post := operation(t, "finance.invoice.post")
	fit := observe(t, odoo, "19.0+e-20260803", "https://acme.odoo.com", post)
	if fit.Edition != EditionEnterprise || fit.Hosting != HostingOdooCloud || !reflect.DeepEqual(fit.EnterpriseModules, []string{"account_accountant", "web_enterprise"}) {
		t.Fatalf("fit = %+v", fit)
	}
	verdict := fit.Assess(post)
	if verdict.Verdict != FitUnverifiedHere || len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "account_accountant") || strings.Contains(verdict.Reasons[0], "account,") {
		t.Fatalf("verdict = %+v", verdict)
	}
	read := fit.Assess(operation(t, "finance.invoice.list"))
	if read.Verdict != FitOK || len(read.Notes) != 1 || !strings.Contains(read.Notes[0], "Enterprise") {
		t.Fatalf("a read on Enterprise: %+v", read)
	}

	// The build alone says Enterprise, even before any Enterprise module is read.
	if fit := observe(t, &fitOdoo{modules: stock(nil)}, "19.0+e", "http://127.0.0.1"); fit.Edition != EditionEnterprise {
		t.Fatalf("a +e build is Enterprise: %+v", fit)
	}
}

// A login that may not read ir.model cannot tell which models a third-party module extends: the
// writes are held back, the reads still run, and both say why.
func TestAThirdPartyModuleThisLoginCannotTraceHoldsBackWrites(t *testing.T) {
	odoo := &fitOdoo{modules: stock(map[string]string{"partner_firstname": "AGPL-3"}), denied: map[string]bool{"ir.model": true}}
	update := operation(t, "identity.contact.update")
	fit := observe(t, odoo, "19.0", "http://127.0.0.1", update)
	if !reflect.DeepEqual(fit.ThirdPartyModules, []string{"partner_firstname"}) {
		t.Fatalf("fit = %+v", fit)
	}
	verdict := fit.Assess(update)
	if verdict.Verdict != FitUnverifiedHere || len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "cannot tell which modules extend it") {
		t.Fatalf("verdict = %+v", verdict)
	}
	read := fit.Assess(operation(t, "identity.contact.list"))
	if read.Verdict != FitOK || len(read.Notes) != 1 || !strings.Contains(read.Notes[0], "partner_firstname") {
		t.Fatalf("read = %+v", read)
	}

	// An administrator's login traces it, and a model it does not extend is not held back.
	odoo = &fitOdoo{modules: stock(map[string]string{"partner_firstname": "AGPL-3"}), extenders: map[string]string{"res.partner": "base, contacts, partner_firstname", "sale.order": "sale"}}
	fit = observe(t, odoo, "19.0", "http://127.0.0.1", update, operation(t, "sales.quotation.create"))
	if verdict := fit.Assess(update); verdict.Verdict != FitUnverifiedHere || !strings.Contains(verdict.Reasons[0], "res.partner is extended by modules outside the stock image: partner_firstname") {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict := fit.Assess(operation(t, "sales.quotation.create")); verdict.Verdict != FitOK {
		t.Fatalf("a write on a model the module does not extend: %+v", verdict)
	}
}

// An automation that a change sets off holds back the writes on its model; a scheduled one does not.
func TestAutomationsHoldBackTheWritesTheyWatch(t *testing.T) {
	odoo := &fitOdoo{modules: stock(map[string]string{"base_automation": "LGPL-3"}), automations: []map[string]interface{}{
		{"name": "Notify sales", "model_name": "res.partner", "trigger": "on_create"},
		{"name": "Nightly cleanup", "model_name": "res.partner", "trigger": "on_time"},
	}}
	create := operation(t, "identity.contact.create")
	fit := observe(t, odoo, "19.0", "http://127.0.0.1", create)
	verdict := fit.Assess(create)
	if verdict.Verdict != FitUnverifiedHere || len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "Notify sales (on_create)") || strings.Contains(verdict.Reasons[0], "Nightly") {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict := fit.Assess(operation(t, "identity.contact.list")); verdict.Verdict != FitOK {
		t.Fatalf("a read was held back by an automation: %+v", verdict)
	}

	// Installed automations this login may not read hold back writes too.
	odoo = &fitOdoo{modules: stock(map[string]string{"base_automation": "LGPL-3"}), denied: map[string]bool{"base.automation": true}}
	if verdict := observe(t, odoo, "19.0", "http://127.0.0.1", create).Assess(create); verdict.Verdict != FitUnverifiedHere || !strings.Contains(verdict.Reasons[0], "which automations run on it") {
		t.Fatalf("unreadable automations: %+v", verdict)
	}
}

// A required field added by hand or by Studio holds back creates, which cannot supply it, and not
// updates.
func TestARequiredHandAddedFieldHoldsBackCreatesOnly(t *testing.T) {
	odoo := &fitOdoo{modules: stock(nil), fields: map[string]map[string]interface{}{"res.partner": {
		"name":          map[string]interface{}{"manual": false, "required": true},
		"x_cost_center": map[string]interface{}{"manual": true, "required": true},
		"x_note":        map[string]interface{}{"manual": true, "required": false},
	}}}
	create, update := operation(t, "identity.contact.create"), operation(t, "identity.contact.update")
	fit := observe(t, odoo, "19.0", "http://127.0.0.1", create, update)
	if mf := fit.Models["res.partner"]; !reflect.DeepEqual(mf.ManualFields, []string{"x_cost_center", "x_note"}) || !reflect.DeepEqual(mf.RequiredManualFields, []string{"x_cost_center"}) {
		t.Fatalf("model fit = %+v", mf)
	}
	if verdict := fit.Assess(create); verdict.Verdict != FitUnverifiedHere || !strings.Contains(verdict.Reasons[0], "x_cost_center") {
		t.Fatalf("create: %+v", verdict)
	}
	if verdict := fit.Assess(update); verdict.Verdict != FitOK {
		t.Fatalf("update: %+v", verdict)
	}
}

func TestHostingIsReadFromTheEndpoint(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://acme.odoo.com":              HostingOdooCloud,
		"https://acme-main-123.dev.odoo.com": HostingOdooCloud,
		"http://127.0.0.1:8069":              HostingElsewhere,
		"https://erp.notodoo.com":            HostingElsewhere,
	} {
		if got := hostingOf(endpoint); got != want {
			t.Errorf("%s = %s, want %s", endpoint, got, want)
		}
	}
}

// A refused series says why: older than everything verified, newer than everything known, an
// intermediate Online release, or a candidate the caller did not admit.
func TestARefusedSeriesSaysWhy(t *testing.T) {
	for _, tc := range []struct{ version, serie, reason string }{
		{"18.0-20250101", "18.0", SeriesOlder},
		{"16.0", "16.0", SeriesOlder},
		{"21.0", "21.0", SeriesNewer},
		{"saas~19.2+e", "saas~19.2", SeriesIntermediate},
		{"20.0-20260926", "20.0", SeriesCandidate},
		{"nightly", "", SeriesUnrecognized},
	} {
		err := checkProviderSeriesAdmission(ProviderVersionInfo{ServerVersion: tc.version, ServerSerie: tc.serie}, false)
		var refusal *ProviderVersionUnsupportedError
		if !errors.As(err, &refusal) || refusal.Reason != tc.reason || refusal.StructuredDetail()["reason"] != tc.reason {
			t.Errorf("%s: err = %v, want reason %s", tc.version, err, tc.reason)
		}
	}
	err := checkProviderSeriesAdmission(ProviderVersionInfo{ServerSerie: "18.0"}, false)
	if !strings.Contains(err.Error(), "older than every series this build was verified on") {
		t.Errorf("message = %q", err)
	}
	if err := checkProviderSeriesAdmission(ProviderVersionInfo{ServerSerie: "19.0"}, false); err != nil {
		t.Errorf("19.0 refused: %v", err)
	}
}

// An endpoint that does not answer Odoo's version handshake is not Odoo: it is refused as such before
// any login, so the credential is never sent.
func TestTheVersionHandshakeRecognisesAnEndpointThatIsNotOdoo(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"an HTTP error": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"a web page":    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>welcome</html>")) },
		"JSON naming no version": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		},
		"a closed connection": func(w http.ResponseWriter, _ *http.Request) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Counted from the server's handler goroutine and read here: atomic, or -race reports it (the
			// closed-connection case returns to the client before the handler has finished).
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				handler(w, r)
			}))
			defer server.Close()
			_, err := NewRPCClient(server.URL, "db", "admin", "secret").ExecuteKw(context.Background(), "res.partner", "search_read", []interface{}{[]interface{}{}}, nil)
			var refusal *ProviderNotOdooError
			if !errors.As(err, &refusal) || refusal.ErrorClass() != string(ErrClassProviderNotOdoo) {
				t.Fatalf("err = %v (%T), want PROVIDER_NOT_ODOO", err, err)
			}
			if n := requests.Load(); n != 1 {
				t.Fatalf("requests = %d: nothing may follow the version question", n)
			}
		})
	}
}

// The check only reads, so a transient failure is retried as a READ operation's is.
func TestTheCheckRetriesATransientFailure(t *testing.T) {
	failures := 1
	reader := readerFunc(func() (interface{}, error) {
		if failures > 0 {
			failures--
			return nil, &HTTPStatusError{StatusCode: http.StatusServiceUnavailable}
		}
		return []interface{}{}, nil
	})
	retrying := RetryReads(reader, RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	if _, err := retrying.ExecuteKw(context.Background(), "ir.module.module", "search_read", nil, nil); err != nil || failures != 0 {
		t.Fatalf("err = %v, failures left = %d", err, failures)
	}
}

type readerFunc func() (interface{}, error)

func (f readerFunc) ExecuteKw(context.Context, string, string, []interface{}, map[string]interface{}) (interface{}, error) {
	return f()
}

// A login that cannot read the installed modules gets no refusal it cannot verify: reads run with a
// note, writes are held back, and nothing repeats the provider's own words, which can echo a secret.
func TestAnUnreadableModuleListIsUnknownNotEchoed(t *testing.T) {
	create := operation(t, "identity.contact.create")
	for name, odoo := range map[string]*fitOdoo{
		"refused":                {modules: stock(nil), denied: map[string]bool{"ir.module.module": true}},
		"an answer without base": {modules: map[string]string{"sale": "LGPL-3"}},
	} {
		t.Run(name, func(t *testing.T) {
			fit := observe(t, odoo, "19.0", "http://127.0.0.1", create)
			if !fit.ModulesUnreadable || fit.Edition != EditionUnknown {
				t.Fatalf("fit = %+v", fit)
			}
			verdict := fit.Assess(create)
			if verdict.Verdict != FitUnverifiedHere || len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "cannot read ir.module.module") || strings.Contains(verdict.Reasons[0], "not allowed") {
				t.Fatalf("write: %+v", verdict)
			}
			if read := fit.Assess(operation(t, "sales.quotation.list")); read.Verdict != FitOK || len(read.Notes) != 1 {
				t.Fatalf("a read whose modules cannot be checked: %+v", read)
			}
		})
	}

	// An unreachable provider is not an unreadable module list: it stays a reachability failure.
	down := readerFunc(func() (interface{}, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	})
	if _, err := FetchProviderFit(context.Background(), down, "http://127.0.0.1", ProviderVersionInfo{ServerSerie: "19.0"}, nil); err == nil || !isProviderUnreachableError(err) {
		t.Fatalf("err = %v, want a reachability failure", err)
	}
}

// ASAO's notice goes wherever the Odoo is not the stock image: with a write held back there, with
// any operation on a database running non-stock modules, and with the refusal. A stock Odoo, and a
// read untouched by a per-model change, carry none.
func TestTheCustomisedOdooNoticeGoesWhereNothingIsGuaranteed(t *testing.T) {
	create, list := operation(t, "identity.contact.create"), operation(t, "identity.contact.list")
	stockFit := observe(t, &fitOdoo{modules: stock(nil)}, "19.0", "http://127.0.0.1", create)
	for _, op := range []Odoo19Operation{create, list} {
		if notice := stockFit.Assess(op).Notice; notice != "" {
			t.Fatalf("%s on a stock Odoo carries a notice: %q", op.SemanticID, notice)
		}
	}

	automated := observe(t, &fitOdoo{modules: stock(map[string]string{"base_automation": "LGPL-3"}), automations: []map[string]interface{}{
		{"name": "Notify sales", "model_name": "res.partner", "trigger": "on_create"},
	}}, "19.0", "http://127.0.0.1", create)
	if notice := automated.Assess(create).Notice; notice != CustomisedOdooNotice {
		t.Fatalf("a write held back by an automation: notice %q", notice)
	}
	if notice := automated.Assess(list).Notice; notice != "" {
		t.Fatalf("a read the automation does not touch: notice %q", notice)
	}

	thirdParty := observe(t, &fitOdoo{modules: stock(map[string]string{"partner_firstname": "AGPL-3"}), denied: map[string]bool{"ir.model": true}}, "19.0", "http://127.0.0.1", create)
	for _, op := range []Odoo19Operation{create, list} {
		if notice := thirdParty.Assess(op).Notice; notice != CustomisedOdooNotice {
			t.Fatalf("%s on an Odoo with a third-party module: notice %q", op.SemanticID, notice)
		}
	}

	refusal := (&EnvironmentUnverifiedError{Operation: "identity.contact.create", Reasons: []string{"x"}}).StructuredDetail()
	if refusal["notice"] != CustomisedOdooNotice {
		t.Fatalf("the refusal's detail = %v", refusal)
	}
	for _, word := range []string{"stock Odoo image", "cannot verify", "does not guarantee", "own risk"} {
		if !strings.Contains(CustomisedOdooNotice, word) {
			t.Fatalf("the notice lost %q: %s", word, CustomisedOdooNotice)
		}
	}
}
