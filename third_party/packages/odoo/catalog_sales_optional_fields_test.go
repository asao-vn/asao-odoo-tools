package odoo

import "testing"

func TestSalesOrderDescribeTreatsCRMOpportunityAsOptional(t *testing.T) {
	entry, ok := CatalogByID["sales.order.describe"]
	if !ok {
		t.Fatal("sales.order.describe is missing from the catalog")
	}

	for _, field := range entry.ReadFields {
		if field == "opportunity_id" {
			t.Fatal("opportunity_id must not be a required read field when crm is optional")
		}
	}

	for _, field := range entry.OptionalReadFields {
		if field == "opportunity_id" {
			contract, contractOK := Odoo19Contracts["sales.order.describe"]
			if !contractOK {
				t.Fatal("sales.order.describe contract is missing")
			}
			currency, currencyOK := contract.OutputByProviderField("currency_id")
			if !currencyOK || currency.Name != "currency" || currency.Type != "reference" {
				t.Fatalf("sales.order.describe must expose currency_id as semantic currency reference: %+v", currency)
			}
			for _, readField := range entry.ReadFields {
				if readField == "currency_id" {
					return
				}
			}
			t.Fatal("currency_id must be read back for provider currency verification")
		}
	}
	t.Fatal("opportunity_id must be declared optional for sales.order.describe")
}
