package review

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
)

var processPrefix = map[string]string{
	"purchase_to_pay": "P2P", "order_to_cash": "O2C", "inventory": "INV", "point_of_sale": "POS",
	"record_to_report": "R2R", "expenses": "EXP", "people": "HR", "access": "AC", "manufacturing": "MRP",
	"master_data": "MD", "tax": "TAX",
}

var fieldPath = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// TestPatternCatalogIsWellFormed reads patterns.json strictly: no unknown key, every value from its closed set,
// every source and operation named somewhere real.
func TestPatternCatalogIsWellFormed(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewReader(patternsJSON))
	decoder.DisallowUnknownFields()
	var catalog PatternCatalog
	if err := decoder.Decode(&catalog); err != nil {
		t.Fatalf("patterns.json: %v", err)
	}
	if _, err := Patterns(); err != nil {
		t.Fatalf("Patterns(): %v", err)
	}
	causes := map[string]bool{}
	for _, cause := range catalog.Causes {
		causes[cause] = true
	}
	severities := map[string]bool{XemNgay: true, NenXuLy: true, TheoDoi: true}
	branches := []string{"asset_misappropriation.", "corruption.", "financial_statement_fraud."}
	seen := map[string]bool{}
	for _, p := range catalog.Patterns {
		prefix, ok := processPrefix[p.Process]
		switch {
		case !ok:
			t.Errorf("%s: unknown process %q", p.ID, p.Process)
		case !regexp.MustCompile(`^` + prefix + `-\d{3}$`).MatchString(p.ID):
			t.Errorf("%s: an id of process %s is %s-NNN", p.ID, p.Process, prefix)
		}
		if seen[p.ID] {
			t.Errorf("%s: two patterns share the id", p.ID)
		}
		seen[p.ID] = true
		if p.Title["vi"] == "" || p.Title["en"] == "" || p.Signal == "" || p.Formula == "" {
			t.Errorf("%s: a pattern has a Vietnamese and an English title, a signal and a formula", p.ID)
		}
		if _, ok := catalog.Kinds[p.Kind]; !ok {
			t.Errorf("%s: unknown kind %q", p.ID, p.Kind)
		}
		if !severities[p.Severity] {
			t.Errorf("%s: unknown severity %q", p.ID, p.Severity)
		}
		if _, ok := catalog.Statuses[p.Status]; !ok {
			t.Errorf("%s: unknown status %q", p.ID, p.Status)
		}
		if p.Priority < 1 || p.Priority > 3 {
			t.Errorf("%s: priority %d is not 1, 2 or 3", p.ID, p.Priority)
		}
		if p.Scheme != nil && !hasAnyPrefix(*p.Scheme, branches) {
			t.Errorf("%s: scheme %q is not under a Fraud Tree branch", p.ID, *p.Scheme)
		}
		if len(p.Fields) == 0 || len(p.Causes) == 0 || len(p.Sources) == 0 || len(p.Cases) == 0 {
			t.Errorf("%s: a pattern names its fields, causes, sources and test cases", p.ID)
		}
		for _, field := range p.Fields {
			if !fieldPath.MatchString(field) {
				t.Errorf("%s: field %q is not model.field", p.ID, field)
			}
		}
		for _, cause := range p.Causes {
			if !causes[cause] {
				t.Errorf("%s: unknown cause %q", p.ID, cause)
			}
		}
		for _, source := range p.Sources {
			if _, ok := catalog.Sources[source]; !ok {
				t.Errorf("%s: unknown source %q", p.ID, source)
			}
		}
		for _, operation := range p.Operations {
			if _, curated := odoo.CatalogByID[operation]; !curated && !odoo.IsSurfaceOperation(operation) {
				t.Errorf("%s: operation %s is not in the catalog", p.ID, operation)
			}
		}
	}
}

// TestPatternCatalogMatchesTheChecks ties the catalog to the code: a built pattern is a registered check that
// reads exactly the operations it says, every check has its pattern, and a designed one is not built yet.
func TestPatternCatalogMatchesTheChecks(t *testing.T) {
	catalog, err := Patterns()
	if err != nil {
		t.Fatal(err)
	}
	builtFor := map[string][]string{}
	for _, p := range catalog.Patterns {
		switch p.Status {
		case "built":
			c, ok := checks[p.Check]
			if !ok {
				t.Errorf("%s: built as %q, which is not a registered check", p.ID, p.Check)
				continue
			}
			builtFor[p.Check] = append(builtFor[p.Check], p.ID)
			if got, want := sorted(p.Operations), sorted(c.needs); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s: operations %v, but check %s reads %v", p.ID, got, p.Check, want)
			}
		case "designed":
			if _, ok := catalog.DesignedChecks[p.Check]; !ok {
				t.Errorf("%s: designed as %q, which designedChecks does not list", p.ID, p.Check)
			}
			if len(p.Operations) > 0 {
				t.Errorf("%s: only a built pattern names the operations its check reads", p.ID)
			}
		case "new":
			if p.Check != "" || len(p.Operations) > 0 {
				t.Errorf("%s: a new pattern has no check and no operations yet", p.ID)
			}
		}
	}
	for id := range checks {
		if n := len(builtFor[id]); n != 1 {
			t.Errorf("check %s has %d built patterns, want exactly 1 (%v)", id, n, builtFor[id])
		}
	}
	for id := range catalog.DesignedChecks {
		if _, built := checks[id]; built && len(builtFor[id]) == 0 {
			t.Errorf("designed check %s is registered but no pattern says it is built", id)
		}
	}
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
