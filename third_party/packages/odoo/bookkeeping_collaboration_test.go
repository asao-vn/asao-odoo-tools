package odoo

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// keywordTransport is a fakeTransport that can also call methods with keyword arguments.
type keywordTransport struct {
	fakeTransport
	keywordCalls []string
}

func (k *keywordTransport) CallMethodKwargs(_ context.Context, model string, ids []int64, method string, kwargs map[string]interface{}, _ map[string]interface{}) (interface{}, error) {
	k.recordMutation("CallMethodKwargs:"+method, model)
	k.keywordCalls = append(k.keywordCalls, fmt.Sprintf("%s%v.%s %v", model, ids, method, kwargs))
	return []interface{}{float64(31)}, nil
}

func surfaceRun(tr Transport, operation, input string) (*ExecutionResult, error) {
	a := NewAdapter(tr)
	a.Surface = true
	return a.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: operation, Input: input})
}

// Odoo refuses a line that leaves its entry unbalanced, even in draft: an entry is made with its
// lines in one call. Each line names its account by code and is checked on its own.
func TestAJournalEntryIsMadeWithItsLines(t *testing.T) {
	tr := &fakeTransport{searchRead: func(model string, domain interface{}, _ []string) ([]map[string]interface{}, error) {
		if model == "account.account" {
			return []map[string]interface{}{{"id": 11.0, "display_name": fmt.Sprint(domain)}}, nil
		}
		return nil, nil
	}, searchCount: func(string) (int64, error) { return 1, nil }}
	_, err := surfaceRun(tr, "finance.journal_entry.create", `{"values":{"ref":"Phí ngân hàng","line_ids":[{"account_id":"6427","name":"Phí","debit":22000},{"account_id":"1121","name":"Phí","credit":22000}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tr.createdValues); got != "[map[line_ids:[[0 0 map[account_id:11 debit:22000 name:Phí]] [0 0 map[account_id:11 credit:22000 name:Phí]]] move_type:entry ref:Phí ngân hàng]]" {
		t.Fatalf("created %s", got)
	}
	for input, want := range map[string]string{
		`{"values":{"line_ids":[{"account_id":"6427","debit":1,"tax_ids":[1]}]}}`: "line_ids[0]",
		`{"values":{"line_ids":"6427:1"}}`:                                        "line_ids takes a list",
		`{"values":{"line_ids":[]}}`:                                              "line_ids takes a list",
	} {
		refused := &fakeTransport{}
		_, err := surfaceRun(refused, "finance.journal_entry.create", input)
		var lines *LinesError
		if !errors.As(err, &lines) || lines.ErrorClass() != "VALIDATION_FAILED" || !containsText(err.Error(), want) {
			t.Fatalf("%s: want a VALIDATION_FAILED naming %q, got %v", input, want, err)
		}
		assertNoMutation(t, refused, input)
	}
}

// A count writes in inventory mode, so it lands on the quant it counts; applying is held to counted
// quants, because Odoo applies an uncounted quant's inventory quantity of 0 just the same.
func TestAnInventoryCountIsSetInInventoryModeAndAppliedOnlyWhenCounted(t *testing.T) {
	tr := &fakeTransport{searchCount: func(string) (int64, error) { return 1, nil }}
	if _, err := surfaceRun(tr, "inventory.count.set", `{"values":{"product_id":5,"location_id":8,"inventory_quantity":17}}`); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tr.createdContexts); got != "[map[inventory_mode:true]]" {
		t.Fatalf("counted with context %s", got)
	}
	uncounted := &fakeTransport{}
	if _, err := surfaceRun(uncounted, "inventory.count.apply", `{"target_id":1}`); err == nil {
		t.Fatal("a quant outside the counted ones was applied")
	}
	assertNoMutation(t, uncounted, "apply an uncounted quant")
}

// A note is an internal note whatever the caller sends: the operation's keywords win, and a body
// is required. An activity's user is resolved by name.
func TestANoteIsAlwaysInternalAndAnActivityNamesItsUser(t *testing.T) {
	tr := &keywordTransport{}
	tr.searchRead = func(model string, _ interface{}, _ []string) ([]map[string]interface{}, error) {
		return []map[string]interface{}{{"id": 7.0, "display_name": model}}, nil
	}
	tr.searchCount = func(string) (int64, error) { return 1, nil }
	if _, err := surfaceRun(tr, "sales.order.log_note", `{"target_id":4,"values":{"body":"Khách hẹn gọi lại"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := surfaceRun(tr, "crm.lead.schedule_activity", `{"target_id":9,"values":{"summary":"Gọi lại","date_deadline":"2026-10-20","user_id":"Mitchell Admin"}}`); err != nil {
		t.Fatal(err)
	}
	want := "[sale.order[4].message_post map[body:Khách hẹn gọi lại message_type:comment subtype_xmlid:mail.mt_note] " +
		"crm.lead[9].activity_schedule map[act_type_xmlid:mail.mail_activity_data_todo date_deadline:2026-10-20 summary:Gọi lại user_id:7]]"
	if got := fmt.Sprint(tr.keywordCalls); got != want {
		t.Fatalf("calls %s\nwant %s", got, want)
	}
	for _, input := range []string{`{"target_id":4,"values":{"body":"x","subtype_xmlid":"mail.mt_comment"}}`, `{"target_id":4,"values":{}}`} {
		refused := &keywordTransport{}
		if _, err := surfaceRun(refused, "sales.order.log_note", input); err == nil {
			t.Fatalf("%s was accepted", input)
		}
		assertNoMutation(t, &refused.fakeTransport, input)
	}
}

// A file is attached only to a record the entity reaches: an invoice's, never a journal entry's
// through the invoice operation.
func TestAFileIsAttachedOnlyToARecordTheEntityReaches(t *testing.T) {
	tr := &fakeTransport{searchCount: func(string) (int64, error) { return 0, nil }}
	_, err := surfaceRun(tr, "finance.invoice.attachment.create", `{"values":{"res_id":12,"name":"hop-dong.pdf","datas":"JVBERi0="}}`)
	var missing *RelationNotFoundError
	if !errors.As(err, &missing) || missing.TargetModel != "account.move" {
		t.Fatalf("want the record refused as outside the invoices, got %v", err)
	}
	assertNoMutation(t, tr, "attach outside the entity")
}

func containsText(text, part string) bool {
	return len(part) == 0 || (len(text) >= len(part) && (text == part || indexOf(text, part) >= 0))
}

func indexOf(text, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
