package odoo_test

import (
	"context"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
)

func TestOdoo19RetailReservationCatalogConformance(t *testing.T) {
	// 1. Verify sales.quotation.create has explicit company, warehouse, and provenance fields
	quotationCreate, ok := odoo.CatalogByID["sales.quotation.create"]
	if !ok {
		t.Fatal("sales.quotation.create not found in CatalogByID")
	}
	expectedQuotationFields := map[string]bool{
		"partner_id":       true,
		"opportunity_id":   true,
		"client_order_ref": true,
		"company_id":       true,
		"warehouse_id":     true,
	}
	for _, f := range quotationCreate.AllowedFields {
		delete(expectedQuotationFields, f)
	}
	if len(expectedQuotationFields) > 0 {
		t.Fatalf("sales.quotation.create missing allowed fields: %v", expectedQuotationFields)
	}
	if quotationCreate.Relations["company_id"].TargetModel != "res.company" {
		t.Fatalf("sales.quotation.create company_id target model = %q, want res.company", quotationCreate.Relations["company_id"].TargetModel)
	}
	if quotationCreate.Relations["warehouse_id"].TargetModel != "stock.warehouse" {
		t.Fatalf("sales.quotation.create warehouse_id target model = %q, want stock.warehouse", quotationCreate.Relations["warehouse_id"].TargetModel)
	}

	// 2. Verify identity.contact operations support company-scoped contact with order-scoped ref
	contactCreate, ok := odoo.CatalogByID["identity.contact.create"]
	if !ok {
		t.Fatal("identity.contact.create not found in CatalogByID")
	}
	hasCompany := false
	hasRef := false
	for _, f := range contactCreate.AllowedFields {
		if f == "company_id" {
			hasCompany = true
		}
		if f == "ref" {
			hasRef = true
		}
	}
	if !hasCompany || !hasRef {
		t.Fatalf("identity.contact.create missing company_id (%v) or ref (%v)", hasCompany, hasRef)
	}
	if contactCreate.Relations["company_id"].TargetModel != "res.company" {
		t.Fatalf("identity.contact.create company_id target model = %q, want res.company", contactCreate.Relations["company_id"].TargetModel)
	}

	contactList, ok := odoo.CatalogByID["identity.contact.list"]
	if !ok {
		t.Fatal("identity.contact.list not found in CatalogByID")
	}
	hasListCompany := false
	hasListRef := false
	for _, f := range contactList.OptionalReadFields {
		if f == "company_id" {
			hasListCompany = true
		}
		if f == "ref" {
			hasListRef = true
		}
	}
	if !hasListCompany || !hasListRef {
		t.Fatalf("identity.contact.list missing optional read fields company_id (%v) or ref (%v)", hasListCompany, hasListRef)
	}

	// 3. Verify sales.quotation.list optional read fields
	quotationList, ok := odoo.CatalogByID["sales.quotation.list"]
	if !ok {
		t.Fatal("sales.quotation.list not found in CatalogByID")
	}
	hasQuotationRef := false
	for _, f := range quotationList.OptionalReadFields {
		if f == "client_order_ref" {
			hasQuotationRef = true
		}
	}
	if !hasQuotationRef {
		t.Fatal("sales.quotation.list missing client_order_ref in OptionalReadFields")
	}
}

// retailWriteTransport is a minimal odoo.Transport for the retail delivery/total contract: it
// records every Create and CallMethod the real Adapter sends, reports every relation target as
// existing, and answers search_read with fixed rows.
type retailWriteTransport struct {
	rows    []map[string]interface{}
	creates []map[string]interface{}
}

func (t *retailWriteTransport) SearchRead(_ context.Context, model string, _ interface{}, fields []string, _ int, _ string, _ map[string]interface{}) ([]map[string]interface{}, error) {
	if model == "ir.model.fields" {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(t.rows))
	for _, row := range t.rows {
		projected := map[string]interface{}{"id": row["id"]}
		for _, f := range fields {
			if v, ok := row[f]; ok {
				projected[f] = v
			}
		}
		out = append(out, projected)
	}
	return out, nil
}

func (t *retailWriteTransport) Read(context.Context, string, []int64, []string, map[string]interface{}) ([]map[string]interface{}, error) {
	return nil, nil
}

func (t *retailWriteTransport) Write(context.Context, string, []int64, map[string]interface{}, map[string]interface{}) error {
	return nil
}

func (t *retailWriteTransport) Create(_ context.Context, _ string, values map[string]interface{}, _ map[string]interface{}) (int64, error) {
	t.creates = append(t.creates, values)
	return 77, nil
}

func (t *retailWriteTransport) CallMethod(context.Context, string, []int64, string, []interface{}, map[string]interface{}) (interface{}, error) {
	return true, nil
}

func (t *retailWriteTransport) SearchCount(context.Context, string, interface{}, map[string]interface{}) (int64, error) {
	return 1, nil
}

func (t *retailWriteTransport) Unlink(context.Context, string, []int64, map[string]interface{}) error {
	return nil
}

// The retail handoff hands the checkout's delivery address to Odoo as a child delivery contact and
// sets it as the quotation's ship-to address, then reconciles the order's tax before confirming.
// Every one of those fields goes through the real Adapter, which refuses a field the catalog does
// not allow and drops a field the catalog does not read; before these catalog entries were
// extended, all three calls below failed (FieldValidationError) or lost the field.
func TestOdoo19RetailDeliveryAddressAndTotalsThroughTheRealAdapter(t *testing.T) {
	ctx := context.Background()

	contactTransport := &retailWriteTransport{}
	if _, err := odoo.NewAdapter(contactTransport).Execute(ctx, &odoo.ExecutionIntent{
		ID: "delivery-contact", Operation: "identity.contact.create",
		Input: `{"values":{"name":"Nguyen Van A","parent_id":99,"type":"delivery","street":"12 Ly Thuong Kiet, Hoan Kiem, Ha Noi","company_id":1,"ref":"order:x:delivery","phone":"+84900000000"}}`,
	}); err != nil {
		t.Fatalf("identity.contact.create must accept a delivery child contact: %v", err)
	}
	if len(contactTransport.creates) != 1 {
		t.Fatalf("expected one res.partner create, got %d", len(contactTransport.creates))
	}
	created := contactTransport.creates[0]
	if created["type"] != "delivery" || created["street"] != "12 Ly Thuong Kiet, Hoan Kiem, Ha Noi" {
		t.Fatalf("delivery contact values lost on the way to Odoo: %+v", created)
	}

	quotationTransport := &retailWriteTransport{}
	if _, err := odoo.NewAdapter(quotationTransport).Execute(ctx, &odoo.ExecutionIntent{
		ID: "quotation", Operation: "sales.quotation.create",
		Input: `{"values":{"partner_id":99,"partner_shipping_id":100,"client_order_ref":"order-1","company_id":1,"warehouse_id":10}}`,
	}); err != nil {
		t.Fatalf("sales.quotation.create must accept partner_shipping_id: %v", err)
	}
	if got := quotationTransport.creates[0]["partner_shipping_id"]; got != float64(100) {
		t.Fatalf("partner_shipping_id = %v, want 100", got)
	}
	if rel := odoo.CatalogByID["sales.quotation.create"].Relations["partner_shipping_id"]; rel.TargetModel != "res.partner" {
		t.Fatalf("partner_shipping_id relation target = %q, want res.partner", rel.TargetModel)
	}

	describeTransport := &retailWriteTransport{rows: []map[string]interface{}{{
		"id": float64(500), "name": "S00500", "state": "draft", "partner_id": []interface{}{float64(99), "A"},
		"amount_total": 50.0, "amount_tax": 0.0, "currency_id": []interface{}{float64(23), "VND"},
		"invoice_status": "no", "invoice_ids": []interface{}{}, "company_id": []interface{}{float64(1), "Co"},
		"client_order_ref": "order-1", "order_line": []interface{}{float64(2001)},
		"partner_shipping_id": []interface{}{float64(100), "A, Delivery"},
	}}}
	result, err := odoo.NewAdapter(describeTransport).Execute(ctx, &odoo.ExecutionIntent{
		ID: "describe", Operation: "sales.order.describe", Input: `{"target_id":500}`,
	})
	if err != nil {
		t.Fatalf("sales.order.describe: %v", err)
	}
	rows, ok := result.Data.([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("sales.order.describe data = %#v", result.Data)
	}
	rec := rows[0].(map[string]interface{})
	if _, ok := rec["amount_tax"]; !ok {
		t.Fatalf("sales.order.describe must read amount_tax back for tax reconciliation: %+v", rec)
	}
	if _, ok := rec["partner_shipping_id"]; !ok {
		t.Fatalf("sales.order.describe must read partner_shipping_id back: %+v", rec)
	}
	if _, ok := rec["total_amount"]; !ok {
		t.Fatalf("sales.order.describe must expose amount_total as total_amount: %+v", rec)
	}

	listEntry := odoo.CatalogByID["identity.contact.list"]
	hasType := false
	for _, f := range listEntry.OptionalReadFields {
		if f == "type" {
			hasType = true
		}
	}
	if !hasType {
		t.Fatal("identity.contact.list must read the contact type back so a delivery contact can be reused")
	}
}
