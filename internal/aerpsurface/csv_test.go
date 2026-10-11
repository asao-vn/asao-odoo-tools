package aerpsurface

import (
	"encoding/json"
	"testing"
)

func TestCSVResultPrintsRecordsAndGroups(t *testing.T) {
	outcome := map[string]interface{}{
		"operation": "sales.order.list",
		"data": []interface{}{
			map[string]interface{}{"id": 7, "name": "S00007", "amount_total": 1500000.5, "partner_id": map[string]interface{}{"id": 3, "display_name": "Công ty A, \"chi nhánh\""}, "tag_ids": []interface{}{1, 2}, "note": nil},
			map[string]interface{}{"id": 8, "name": "S00008", "amount_total": 250000, "state": "sale"},
		},
	}
	want := "id,name,amount_total,note,partner_id,state,tag_ids\n" +
		"7,S00007,1500000.5,,\"Công ty A, \"\"chi nhánh\"\"\",,\"[1,2]\"\n" +
		"8,S00008,250000,,,sale,\n"
	if got := (CSVResult{Result: outcome}).RawText(); got != want {
		t.Fatalf("records:\n%s\nwant:\n%s", got, want)
	}

	groups := []interface{}{
		map[string]interface{}{"date_order:month": map[string]interface{}{"value": "2026-10-01", "display_name": "tháng 10 2026"}, "amount_total:sum": 1750000.5, "count": 2},
	}
	if got := (CSVResult{Result: groups}).RawText(); got != "amount_total:sum,count,date_order:month\n1750000.5,2,tháng 10 2026\n" {
		t.Fatalf("groups:\n%s", got)
	}

	// Not records: printed as JSON, and the JSON contract never changes.
	if got := (CSVResult{Result: map[string]interface{}{"status": "OK"}}).RawText(); got != "{\n  \"status\": \"OK\"\n}\n" {
		t.Fatalf("non-records: %q", got)
	}
	encoded, err := json.Marshal(CSVResult{Result: outcome})
	if err != nil {
		t.Fatal(err)
	}
	if direct, _ := json.Marshal(outcome); string(encoded) != string(direct) {
		t.Fatalf("JSON changed: %s", encoded)
	}
}
