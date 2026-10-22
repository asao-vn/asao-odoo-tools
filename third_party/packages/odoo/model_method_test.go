package odoo

import (
	"context"
	"testing"
)

// modelMethodTransport answers a keyword call the way website.new_page does, and keeps what it was asked.
type modelMethodTransport struct {
	fakeTransport
	ids    []int64
	method string
	kwargs map[string]interface{}
}

func (m *modelMethodTransport) CallMethodKwargs(_ context.Context, model string, ids []int64, method string, kwargs map[string]interface{}, _ map[string]interface{}) (interface{}, error) {
	m.recordMutation("CallMethodKwargs:"+method, model)
	m.ids, m.method, m.kwargs = ids, method, kwargs
	return map[string]interface{}{"url": "/lop-hoc", "view_id": float64(41), "page_id": float64(9), "menu_id": float64(12)}, nil
}

// website.new_page is declared @api.model: Odoo reads a positional list of ids as its first parameter (name), so a
// model method is called with no ids at all, with the caller's inputs under the operation's fixed keywords, and its
// answer names the page it made.
func TestAModelMethodIsCalledOnTheModelWithKeywordsOnly(t *testing.T) {
	tr := &modelMethodTransport{}
	result, err := surfaceRun(tr, "website.page.new",
		`{"values":{"name":"lop-hoc","page_title":"Lớp học","sections_arch":"<section><p>x</p></section>","add_menu":true}}`)
	if err != nil {
		t.Fatalf("website.page.new: %v", err)
	}
	if tr.ids != nil || tr.method != "new_page" {
		t.Fatalf("called %s with ids %v; a model method takes none", tr.method, tr.ids)
	}
	for key, want := range map[string]interface{}{"name": "lop-hoc", "page_title": "Lớp học", "add_menu": true,
		"template": "website.default_page", "ispage": true} {
		if tr.kwargs[key] != want {
			t.Errorf("keyword %s = %v, want %v", key, tr.kwargs[key], want)
		}
	}
	if result.ProviderReference != "9" {
		t.Fatalf("the reference is the page Odoo made (page_id 9), got %q", result.ProviderReference)
	}
	if TargetRequirementOf(CatalogByID["website.page.new"]) != TargetRequirementNone {
		t.Fatal("a model method acts on no existing record")
	}
}

func TestAModelMethodRefusesATargetAndOverridesNoFixedKeyword(t *testing.T) {
	tr := &modelMethodTransport{}
	if _, err := surfaceRun(tr, "website.page.new", `{"target_id":3,"values":{"name":"lop-hoc"}}`); err == nil {
		t.Fatal("a model method accepted a target record")
	}
	if _, err := surfaceRun(tr, "website.page.new", `{"values":{"name":"x","template":"website.homepage"}}`); err == nil {
		t.Fatal("a caller chose the template the operation fixes")
	}
	if _, err := surfaceRun(tr, "website.page.new", `{"values":{"page_title":"no name"}}`); err == nil {
		t.Fatal("a page was made without its address word")
	}
	if len(tr.mutations) != 0 {
		t.Fatalf("a refused call reached the provider: %v", tr.mutations)
	}
}
