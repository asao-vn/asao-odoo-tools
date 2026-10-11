package odoo

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// groupingTransport is a fakeTransport that can also page and group (PagedSearcher, GroupReader).
type groupingTransport struct {
	fakeTransport
	groupCalls  []string
	pageOffsets []int
}

func (g *groupingTransport) ReadGroup(_ context.Context, model string, domain interface{}, groupBy, aggregates []string, offset, limit int, order string, _ map[string]interface{}) ([]map[string]interface{}, error) {
	g.groupCalls = append(g.groupCalls, fmt.Sprintf("%s %v %v %v %d %d %q", model, domain, groupBy, aggregates, offset, limit, order))
	return []map[string]interface{}{
		{"stage_id": []interface{}{2.0, "Qualified"}, "expected_revenue:sum": 1500.0, "__count": 3.0, "__extra_domain": []interface{}{}},
		{"stage_id": false, "expected_revenue:sum": 0.0, "__count": 1.0, "__extra_domain": []interface{}{}},
	}, nil
}

func (g *groupingTransport) SearchReadPage(_ context.Context, model string, _ interface{}, _ []string, offset, limit int, _ string, _ map[string]interface{}) ([]map[string]interface{}, error) {
	g.pageOffsets = append(g.pageOffsets, offset)
	return []map[string]interface{}{{"id": float64(offset + 1), "name": "lead"}}, nil
}

func execute(t *testing.T, tr Transport, input string) (*ExecutionResult, error) {
	t.Helper()
	return NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.lead.list", Input: input})
}

// A grouped read is aggregated by the provider, over the fields the read shows; each group carries
// its count, and a many2one group value reads like a relation does elsewhere.
func TestAReadIsGroupedByTheProvider(t *testing.T) {
	tr := &groupingTransport{}
	res, err := execute(t, tr, `{"domain":[["type","=","opportunity"]],"group_by":["stage_id"],"aggregates":["expected_revenue:sum"],"order":"expected_revenue:sum desc"}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[crm.lead [[type = opportunity]] [stage_id] [expected_revenue:sum __count] 0 0 "expected_revenue:sum desc"]`; fmt.Sprint(tr.groupCalls) != want {
		t.Fatalf("grouped as %v, want %s", tr.groupCalls, want)
	}
	if got := fmt.Sprint(res.Data); got != "[map[count:3 expected_revenue:sum:1500 stage_id:map[display_name:Qualified id:2]] map[count:1 expected_revenue:sum:0 stage_id:<nil>]]" {
		t.Fatalf("groups %s", got)
	}
}

// Grouping never shows more than reading does, and a malformed specification is refused before
// the provider is contacted.
func TestAGroupedReadIsRefusedBeforeOdooWhenMalformed(t *testing.T) {
	for _, input := range []string{
		`{"group_by":["description"]}`,
		`{"group_by":["stage_id:fortnight"]}`,
		`{"aggregates":["expected_revenue:median"]}`,
		`{"aggregates":["expected_revenue"]}`,
		`{"aggregates":["description:max"]}`,
		`{"target_id":4,"group_by":["stage_id"]}`,
		`{"offset":-1}`,
	} {
		tr := &groupingTransport{}
		_, err := execute(t, tr, input)
		var semantic interface{ ErrorClass() string }
		if !errors.As(err, &semantic) || semantic.ErrorClass() != "VALIDATION_FAILED" {
			t.Fatalf("%s: want VALIDATION_FAILED, got %v", input, err)
		}
		if len(tr.groupCalls) != 0 || len(tr.calls) != 0 {
			t.Fatalf("%s reached the provider: %v %v", input, tr.groupCalls, tr.calls)
		}
	}
}

// A read pages through its records with an offset.
func TestAReadPagesWithAnOffset(t *testing.T) {
	tr := &groupingTransport{}
	res, err := execute(t, tr, `{"offset":40,"limit":20}`)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(tr.pageOffsets) != "[40]" || fmt.Sprint(res.Data) != "[map[id:41 name:lead]]" {
		t.Fatalf("paged %v -> %v", tr.pageOffsets, res.Data)
	}
	if _, err := execute(t, &fakeTransport{}, `{"group_by":["stage_id"]}`); err == nil {
		t.Fatal("a transport that cannot group answered a grouped read")
	}
}
