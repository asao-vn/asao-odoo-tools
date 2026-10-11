package odoo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestSurfaceCatalogIsNeverGoverned(t *testing.T) {
	if len(SurfaceCatalog()) == 0 {
		t.Fatal("the surface catalog is empty")
	}
	for _, op := range Catalog() {
		if IsSurfaceOperation(op.SemanticID) {
			t.Fatalf("%s is in both catalogs", op.SemanticID)
		}
	}
	for _, op := range SurfaceCatalog() {
		if _, governed := CatalogByID[op.SemanticID]; governed {
			t.Fatalf("surface operation %s is reachable through CatalogByID", op.SemanticID)
		}
		if op.Applicability.AERPCLI || !op.Applicability.DirectCLI || op.Applicability.AERPCLIReason == "" {
			t.Fatalf("%s applicability = %+v; a surface operation is standalone-only and says why", op.SemanticID, op.Applicability)
		}
	}
}

// TestSurfaceOperationsClaimNothing: a surface operation is UNVERIFIED on a series unless that
// series' own manifest names it, and its evidence class never changes with promotion.
func TestSurfaceOperationsClaimNothing(t *testing.T) {
	evidenceOf := map[string][]string{}
	named := func(series string) map[string]bool {
		records, _, err := seriesConformanceRecords(series)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, record := range records {
			out[record.OperationID] = true
			evidenceOf[record.OperationID] = append(evidenceOf[record.OperationID], record.EvidenceRef)
		}
		return out
	}
	on19, on20 := named(Odoo19ProviderSeries), named(Odoo20ProviderSeries)
	for _, op := range SurfaceCatalog() {
		want19, want20 := ReadinessUnverified, ReadinessUnverified
		if on19[op.SemanticID] {
			want19 = ReadinessCurrentVerified
		}
		if on20[op.SemanticID] {
			want20 = ReadinessCurrentVerified
		}
		if readiness, ok := ReadinessOf(op.SemanticID); !ok || readiness != want19 {
			t.Fatalf("%s readiness = %q, %v; want %s", op.SemanticID, readiness, ok, want19)
		}
		if evidence, ok := Odoo19EvidenceClassOf(op.SemanticID); !ok || evidence != Odoo19EvidenceSurfaceGenerated {
			t.Fatalf("%s evidence = %q, %v; want SURFACE_GENERATED", op.SemanticID, evidence, ok)
		}
		if readiness, ok := ReadinessFor(Odoo20ProviderSeries, op.SemanticID); !ok || readiness != want20 {
			t.Fatalf("%s 20.0 readiness = %q, %v; want %s", op.SemanticID, readiness, ok, want20)
		}
		// Reads (scripts/dev/odoo-read-conformance.py) and creates/updates
		// (scripts/dev/odoo-write-conformance.py) each have their oracle-backed run. An action or a
		// wizard, or a guarded delete, is promoted only on the action runner's evidence
		// (scripts/dev/odoo-action-conformance.py: the CLI and an independent web-client oracle effected
		// the same transition on twin records, or deleted fresh twins and nothing else).
		if op.Strategy == StrategyMethodCall || op.Strategy == StrategyWizard || op.Strategy == StrategyDestructiveGuarded {
			for _, ref := range evidenceOf[op.SemanticID] {
				if !strings.Contains(ref, "/action-conformance-") {
					t.Fatalf("%s is promoted on %s, which is not action conformance evidence", op.SemanticID, ref)
				}
			}
		}
		// A print is promoted only on the report runner's evidence (scripts/dev/odoo-report-conformance.py:
		// the CLI's PDF and the browser's download of the same record have the same pages and text).
		if op.Strategy == StrategyReport {
			for _, ref := range evidenceOf[op.SemanticID] {
				if !strings.Contains(ref, "/report-conformance-") {
					t.Fatalf("%s is promoted on %s, which is not report conformance evidence", op.SemanticID, ref)
				}
			}
		}
		if op.Confidence != "SURFACE_DISCOVERED" {
			t.Fatalf("%s confidence = %q", op.SemanticID, op.Confidence)
		}
	}
	// The curated release gate and readiness report are about the curated catalog only.
	if report := CurrentConformanceReport(); report.CatalogOperations != len(Catalog()) {
		t.Fatalf("readiness report counts %d operations, the curated catalog has %d", report.CatalogOperations, len(Catalog()))
	}
}

var wellFormedCapability = regexp.MustCompile(`^[a-z]+(\.[a-z]+)+$`)

func TestSurfaceOperationsAreWellFormed(t *testing.T) {
	for _, op := range SurfaceCatalog() {
		if !wellFormedCapability.MatchString(string(op.RequiredCapability)) {
			t.Fatalf("%s capability %q would be refused by the governance catalogue on promotion", op.SemanticID, op.RequiredCapability)
		}
		switch op.Strategy {
		case StrategyRead:
			// A surface read asks only for what every model has; everything else is optional, so an
			// Odoo without one of the fields (another series, another module set) still answers. A
			// new record (the Settings page) has no id: all of its fields are optional.
			switch {
			case op.ReadNewRecord && (len(op.ReadFields) != 0 || len(op.OptionalReadFields) == 0):
				t.Fatalf("%s reads a new record through %v and %v", op.SemanticID, op.ReadFields, op.OptionalReadFields)
			case !op.ReadNewRecord && strings.Join(op.ReadFields, ",") != "id,display_name":
				t.Fatalf("%s reads %v unconditionally", op.SemanticID, op.ReadFields)
			}
		case StrategyFieldWrite:
			for _, field := range op.AllowedFields {
				if field == "model" || field == "method" {
					t.Fatalf("%s offers %q for writing; it would surface as a raw-RPC flag", op.SemanticID, field)
				}
			}
		case StrategyMethodCall:
			if op.Method == "" || strings.HasPrefix(op.Method, "_") {
				t.Fatalf("%s calls %q", op.SemanticID, op.Method)
			}
		case StrategyWizard:
			// Opened the way Odoo's own button opens it: through the active context, or as the dialog
			// the business method answers with, in that dialog's own context (never both); with no
			// link field written (a wizard's field back to the model can be an output). A standalone
			// wizard (the Settings page) is opened by no record: neither, and caller values only.
			if op.WizardStandalone {
				if op.WizardModel == "" || op.WizardMethod == "" || op.WizardActiveContext || op.WizardOpenerMethod != "" || op.WizardLinkField != "" || len(op.WizardAllowedFields) == 0 {
					t.Fatalf("%s standalone wizard = %s.%s context=%v opener=%q link=%q allowed=%v", op.SemanticID, op.WizardModel, op.WizardMethod, op.WizardActiveContext, op.WizardOpenerMethod, op.WizardLinkField, op.WizardAllowedFields)
				}
				break
			}
			if op.WizardModel == "" || op.WizardMethod == "" || op.WizardActiveContext == (op.WizardOpenerMethod != "") || op.WizardLinkField != "" {
				t.Fatalf("%s wizard = %s.%s context=%v opener=%q link=%q", op.SemanticID, op.WizardModel, op.WizardMethod, op.WizardActiveContext, op.WizardOpenerMethod, op.WizardLinkField)
			}
			if op.WizardOpenerMethod != "" && (len(op.WizardContext) > 0 || len(op.WizardContextTarget) > 0 || strings.HasPrefix(op.WizardOpenerMethod, "_")) {
				t.Fatalf("%s opens its dialog through %q with a context of its own", op.SemanticID, op.WizardOpenerMethod)
			}
		case StrategyReport:
			// A print renders a named report and changes nothing.
			if op.ReportName == "" || op.Kind != KindRead {
				t.Fatalf("%s prints %q as %s", op.SemanticID, op.ReportName, op.Kind)
			}
		case StrategyDestructiveGuarded:
			// A surface delete (GUARDED_DELETES) is high risk and guarded: it names the records that
			// point at its target, or, having none, a provider domain that holds what it reaches.
			if op.Risk != RiskHigh || (op.ImpactModel == "" && len(op.ProviderDomain) == 0) || (op.ImpactModel == "") != (op.ImpactField == "") {
				t.Fatalf("%s deletes with risk %s, impact %s.%s, domain %v", op.SemanticID, op.Risk, op.ImpactModel, op.ImpactField, op.ProviderDomain)
			}
		default:
			t.Fatalf("%s has strategy %s; the generator emits READ, FIELD_WRITE, METHOD_CALL, WIZARD, DESTRUCTIVE_GUARDED and REPORT only", op.SemanticID, op.Strategy)
		}
		if TargetRequirementOf(op) == TargetRequirementRequired && op.Strategy == StrategyRead {
			t.Fatalf("%s is a read that requires a target", op.SemanticID)
		}
	}
}

// TestOnlyASurfaceAdapterExecutesASurfaceOperation: the governed product builds its adapter without
// Surface, so it cannot execute a surface operation even if it is named, and it never contacts the
// provider trying.
func TestOnlyASurfaceAdapterExecutesASurfaceOperation(t *testing.T) {
	var surfaceRead Odoo19Operation
	for _, op := range SurfaceCatalog() {
		if op.Strategy == StrategyRead {
			surfaceRead = op
			break
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Params.Method {
		case "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}})
		case "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 2})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
		}
	}))
	defer server.Close()

	governed := NewAdapter(NewClientTransport(NewRPCClient(server.URL, "db", "admin", "secret")))
	if _, err := governed.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: surfaceRead.SemanticID}); err == nil {
		t.Fatalf("a governed adapter executed surface operation %s", surfaceRead.SemanticID)
	}
	if calls != 0 {
		t.Fatalf("a governed adapter contacted the provider %d times for a surface operation", calls)
	}

	standalone := NewAdapter(NewClientTransport(NewRPCClient(server.URL, "db", "admin", "secret")))
	standalone.Surface = true
	if _, err := standalone.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: surfaceRead.SemanticID}); err != nil {
		t.Fatalf("the standalone adapter refused surface operation %s: %v", surfaceRead.SemanticID, err)
	}
}

func TestAContextWizardIsOpenedLikeOdoosOwnButton(t *testing.T) {
	op, ok := SurfaceCatalogByID["crm.lead.mark_lost"]
	if !ok {
		t.Fatal("crm.lead.mark_lost is not generated")
	}
	var created struct {
		values  map[string]interface{}
		context map[string]interface{}
	}
	var called []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string        `json:"method"`
				Args   []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Params.Method {
		case "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}})
			return
		case "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 2})
			return
		}
		model, _ := req.Params.Args[3].(string)
		method, _ := req.Params.Args[4].(string)
		called = append(called, model+"."+method)
		if method == "create" {
			positional, _ := req.Params.Args[5].([]interface{})
			created.values, _ = positional[0].(map[string]interface{})
			kwargs, _ := req.Params.Args[6].(map[string]interface{})
			created.context, _ = kwargs["context"].(map[string]interface{})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 41})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": true})
	}))
	defer server.Close()
	adapter := NewAdapter(NewClientTransport(NewRPCClient(server.URL, "db", "admin", "secret")))
	adapter.Surface = true

	if _, err := adapter.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: op.SemanticID, Input: `{"target_id":7,"values":{"stage_id":3}}`}); err == nil {
		t.Fatal("a wizard value outside WizardAllowedFields was accepted")
	}
	if len(called) != 0 {
		t.Fatalf("a refused wizard value still reached the provider: %v", called)
	}

	if _, err := adapter.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: op.SemanticID, Input: `{"target_id":7,"values":{"lost_feedback":"Gia cao"}}`}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if created.context["active_model"] != "crm.lead" || created.context["active_id"] != 7.0 || fmt.Sprint(created.context["active_ids"]) != "[7]" {
		t.Fatalf("wizard context = %v, want the lead as active record", created.context)
	}
	// crm.lead.lost's action context names the leads as default_lead_ids: active_ids.
	if fmt.Sprint(created.context["default_lead_ids"]) != "[7]" {
		t.Fatalf("wizard context = %v, want default_lead_ids bound to the target", created.context)
	}
	if created.values["lost_feedback"] != "Gia cao" || len(created.values) != 1 {
		t.Fatalf("wizard values = %v, want exactly the caller's feedback and no link field", created.values)
	}
	if strings.Join(called, ",") != "crm.lead.lost.fields_get,crm.lead.lost.onchange,crm.lead.lost.create,crm.lead.lost.action_lost_reason_apply" {
		t.Fatalf("calls = %v", called)
	}
}

// TestSurfaceEvidenceIsBoundToTheDefinitionItVerified: a regenerated surface operation keeps its
// promotion only while its definition is the one the conformance run checked.
func TestSurfaceEvidenceIsBoundToTheDefinitionItVerified(t *testing.T) {
	for _, series := range []string{Odoo19ProviderSeries, Odoo20ProviderSeries} {
		records, _, err := seriesConformanceRecords(series)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			op, surface := SurfaceCatalogByID[record.OperationID]
			if !surface {
				continue
			}
			if record.DefinitionHash == "" {
				t.Fatalf("%s surface record %s carries no definitionHash", series, record.OperationID)
			}
			if record.DefinitionHash != OperationDefinitionHash(op) {
				t.Fatalf("%s surface record %s was verified against another definition; re-run its conformance or drop the record", series, record.OperationID)
			}
		}
	}

	records, err := currentConformanceRecords()
	if err != nil {
		t.Fatal(err)
	}
	var probe ConformanceRecord
	for _, record := range records {
		if IsSurfaceOperation(record.OperationID) {
			probe = record
			break
		}
	}
	if readiness, _ := ReadinessOf(probe.OperationID); readiness != ReadinessCurrentVerified {
		t.Fatalf("%s readiness = %s before the definition changed", probe.OperationID, readiness)
	}
	mutated := make([]ConformanceRecord, len(records))
	copy(mutated, records)
	for i := range mutated {
		if mutated[i].OperationID == probe.OperationID {
			mutated[i].DefinitionHash = "sha256:" + strings.Repeat("0", 64)
		}
	}
	manifestOnce.Do(func() {})
	saved := manifestRecords
	manifestRecords = mutated
	defer func() { manifestRecords = saved }()
	if readiness, _ := ReadinessOf(probe.OperationID); readiness != ReadinessUnverified {
		t.Fatalf("%s readiness = %s with evidence for another definition; want UNVERIFIED", probe.OperationID, readiness)
	}
}
