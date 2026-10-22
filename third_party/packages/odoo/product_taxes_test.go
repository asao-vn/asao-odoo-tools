package odoo

import (
	"fmt"
	"testing"
)

// A teaching organization's Odoo puts its default sales tax on every new product, and tuition is usually outside VAT
// (the services gate's first tuition invoice carried 10%). So a product's sales taxes are written on create and on
// update, replaced as a whole, an empty list leaving none. (A caller checks the result on the order: describe's
// definition is bound to its conformance evidence, and its read set stays as verified.)
func TestProductSalesTaxesAreReplacedAsAWhole(t *testing.T) {
	tr := &fakeTransport{searchCount: func(string) (int64, error) { return 1, nil }}
	if _, err := surfaceRun(tr, "catalog.product.update", `{"target_id":7,"values":{"taxes_id":[]}}`); err != nil {
		t.Fatalf("clearing a product's taxes: %v", err)
	}
	if got := fmt.Sprint(tr.writtenValues); got != "[map[taxes_id:[[6 0 []]]]]" {
		t.Fatalf("an empty list must replace the taxes with none, wrote %s", got)
	}
	for _, call := range tr.calls {
		if call.Op == "SearchCount" && call.Model == "account.tax" {
			t.Fatalf("no tax was named, yet one was looked up: %v", tr.calls)
		}
	}

	tr = &fakeTransport{searchCount: func(string) (int64, error) { return 1, nil }}
	if _, err := surfaceRun(tr, "catalog.product.update", `{"target_id":7,"values":{"taxes_id":[3]}}`); err != nil {
		t.Fatalf("putting a tax back: %v", err)
	}
	if got := fmt.Sprint(tr.writtenValues); got != "[map[taxes_id:[[6 0 [3]]]]]" {
		t.Fatalf("a named tax must replace the taxes, wrote %s", got)
	}

	tr = &fakeTransport{searchCount: func(string) (int64, error) { return 0, nil }}
	if _, err := surfaceRun(tr, "catalog.product.update", `{"target_id":7,"values":{"taxes_id":[99]}}`); err == nil {
		t.Fatal("a tax that does not exist was written")
	}
	if len(tr.writtenValues) != 0 {
		t.Fatalf("nothing may be written when a tax does not exist, wrote %v", tr.writtenValues)
	}
}

func TestProductSalesTaxesAreInTheCallerVocabulary(t *testing.T) {
	for _, op := range []string{"catalog.product.create", "catalog.product.update"} {
		taxes, ok := Odoo19Contracts[op].InputByProviderField("taxes_id")
		if !ok || taxes.Name != "taxes" || taxes.Type != "reference_list" {
			t.Fatalf("%s must accept taxes_id as the semantic reference list taxes: %+v", op, taxes)
		}
	}
}
