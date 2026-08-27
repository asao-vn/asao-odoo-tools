package main

import "fmt"

// buildChain assembles the full live conformance run: representative READ
// calls across every domain, the two golden cross-domain chains
// (commercial: CRM->Quotation->Sale->Delivery->Invoice->Payment, and its
// procurement mirror), and destructive-guard negative/positive pairs for
// the two provider gaps the odoo19 research proved (department delete
// orphans employees; project delete cascades tasks/milestones with zero
// warning).
//
// runID is folded into every distinguishing name literal so that repeated
// harness invocations don't collide on the DETERMINISTIC idempotency key
// (derived from organization+operation+arguments when no explicit key is
// supplied) and silently replay a stale prior run's now-deleted record —
// which is correct idempotency behavior, just confusing across independent
// test runs. See testIdempotencyReplay below for a deliberate,
// WITHIN-one-run proof of that same replay behavior.
func buildChain(runID string) []step {
	var chain []step
	name := func(s string) string { return s + " " + runID }

	// ---- representative READs across every domain --------------------------
	reads := []string{
		"identity.contact.list", "people.employee.list", "people.department.list",
		"work.project.list", "work.task.list", "crm.lead.list",
		"sales.quotation.list", "procurement.rfq.list", "inventory.transfer.list",
		"inventory.stock_level.read", "finance.invoice.list", "finance.journal.list",
		"finance.account.chart.list", "finance.tax.list", "finance.payment_term.list",
		"catalog.product.list", "schedule.event.list",
	}
	for _, id := range reads {
		chain = append(chain, step{Label: "read:" + id, OperationID: id, Args: map[string]interface{}{"limit": 5}})
	}

	// ---- catalog: product needed by both golden chains ----------------------
	chain = append(chain, step{
		Label: "create sellable product", OperationID: "catalog.product.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Widget"), "type": "consu", "is_storable": true, "list_price": 120.0, "sale_ok": true, "purchase_ok": true}},
		CaptureIDFrom: "product_id",
	})
	chain = append(chain, step{
		Label: "update product price", OperationID: "catalog.product.update",
		Args: map[string]interface{}{"target_id": "$product_id", "values": map[string]interface{}{"list_price": 150.0}},
	})
	chain = append(chain, step{
		// sale.order.line/purchase.order.line need a product.product
		// (variant) id, not the product.template id catalog.product.create
		// returns — resolve it via a READ, exactly as a real caller would.
		Label: "resolve product variant id", OperationID: "catalog.product.describe",
		Args: map[string]interface{}{"target_id": "$product_id"}, CaptureIDFrom: "variant_id", CaptureField: "variants",
	})

	// ==== GOLDEN CHAIN 1: commercial (CRM -> Quotation -> Sale -> Delivery -> Invoice -> Payment) ====
	chain = append(chain, step{
		Label: "create customer contact", OperationID: "identity.contact.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Buyer LLC"), "is_company": true}},
		CaptureIDFrom: "customer_id",
	})
	chain = append(chain, step{
		Label: "describe the customer contact", OperationID: "identity.contact.describe",
		Args: map[string]interface{}{"target_id": "$customer_id"},
	})
	chain = append(chain, step{
		Label: "create CRM lead", OperationID: "crm.lead.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Deal"), "type": "lead", "partner_id": "$customer_id", "expected_revenue": 5000.0}},
		CaptureIDFrom: "lead_id",
	})
	chain = append(chain, step{
		Label: "describe the CRM lead", OperationID: "crm.lead.describe",
		Args: map[string]interface{}{"target_id": "$lead_id"},
	})
	chain = append(chain, step{
		Label: "convert lead to opportunity (field write, not the RPC-unsafe convert_opportunity method)", OperationID: "crm.opportunity.convert",
		Args: map[string]interface{}{"target_id": "$lead_id", "values": map[string]interface{}{"type": "opportunity"}},
	})
	chain = append(chain, step{
		Label: "win the opportunity", OperationID: "crm.opportunity.win",
		Args: map[string]interface{}{"target_id": "$lead_id"},
	})
	chain = append(chain, step{
		Label: "create a second, throwaway lead to exercise the lose path", OperationID: "crm.lead.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Doomed Deal"), "type": "opportunity", "partner_id": "$customer_id"}},
		CaptureIDFrom: "doomed_lead_id",
	})
	chain = append(chain, step{
		Label: "lose the throwaway opportunity (void provider return, treated as success)", OperationID: "crm.opportunity.lose",
		Args: map[string]interface{}{"target_id": "$doomed_lead_id"},
	})
	chain = append(chain, step{
		Label: "create quotation linked to the won opportunity", OperationID: "sales.quotation.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"partner_id": "$customer_id", "opportunity_id": "$lead_id"}},
		CaptureIDFrom: "sale_order_id",
	})
	chain = append(chain, step{
		Label: "add quotation line", OperationID: "sales.quotation.line.add",
		Args: map[string]interface{}{"values": map[string]interface{}{"order_id": "$sale_order_id", "product_id": "$variant_id", "product_uom_qty": 2}},
	})
	chain = append(chain, step{
		Label: "confirm sale order (spawns a delivery picking as a side effect)", OperationID: "sales.order.confirm",
		Args: map[string]interface{}{"target_id": "$sale_order_id"},
	})
	chain = append(chain, step{
		Label: "resolve the auto-created delivery picking id", OperationID: "sales.order.describe",
		Args: map[string]interface{}{"target_id": "$sale_order_id"}, CaptureIDFrom: "picking_id", CaptureField: "picking_ids",
	})
	chain = append(chain, step{
		Label: "attempt to reserve stock for the delivery (none on hand yet)", OperationID: "inventory.transfer.reserve",
		Args: map[string]interface{}{"target_id": "$picking_id"},
	})
	chain = append(chain, step{
		Label: "attempt to validate delivery with zero stock reserved (expected business precondition failure, not a crash)", OperationID: "inventory.transfer.validate",
		Args: map[string]interface{}{"target_id": "$picking_id"}, ExpectError: true,
	})
	chain = append(chain, step{
		Label: "create customer invoice via wizard", OperationID: "sales.invoice.create",
		Args: map[string]interface{}{"target_id": "$sale_order_id"}, CaptureIDFrom: "invoice_id", CaptureField: "created_id",
	})
	chain = append(chain, step{
		Label: "describe the customer invoice", OperationID: "finance.invoice.describe",
		Args: map[string]interface{}{"target_id": "$invoice_id"},
	})
	chain = append(chain, step{
		Label: "post the customer invoice", OperationID: "finance.invoice.post",
		Args: map[string]interface{}{"target_id": "$invoice_id"},
	})
	chain = append(chain, step{
		Label: "register payment on the customer invoice (reaches paid)", OperationID: "finance.payment.register",
		Args: map[string]interface{}{"target_id": "$invoice_id"},
	})

	// ==== GOLDEN CHAIN 2: procurement mirror ====
	chain = append(chain, step{
		Label: "create vendor contact", OperationID: "identity.contact.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Vendor Co"), "is_company": true}},
		CaptureIDFrom: "vendor_id",
	})
	chain = append(chain, step{
		Label: "create RFQ", OperationID: "procurement.rfq.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"partner_id": "$vendor_id"}},
		CaptureIDFrom: "purchase_order_id",
	})
	chain = append(chain, step{
		Label: "add RFQ line", OperationID: "procurement.rfq.line.add",
		Args: map[string]interface{}{"values": map[string]interface{}{"order_id": "$purchase_order_id", "product_id": "$variant_id", "product_qty": 10, "price_unit": 40}},
	})
	chain = append(chain, step{
		Label: "confirm purchase order (spawns a receipt picking as a side effect)", OperationID: "procurement.order.confirm",
		Args: map[string]interface{}{"target_id": "$purchase_order_id"},
	})
	// The receipt must actually be received before billing. Odoo bills the
	// RECEIVED quantity, so skipping this produced a zero-value bill that
	// then trivially reported payment_state=paid — a money workflow proving
	// nothing. Resolved through existing catalog operations (no new
	// operation): the PO's name identifies the picking via its origin.
	chain = append(chain, step{
		Label: "resolve the purchase order's document name", OperationID: "procurement.rfq.list",
		Args:          map[string]interface{}{"domain": []interface{}{[]interface{}{"id", "=", "$purchase_order_id"}}, "limit": 1},
		CaptureNameTo: "purchase_order_name",
	})
	chain = append(chain, step{
		Label: "find the receipt picking spawned by confirming the purchase order", OperationID: "inventory.transfer.list",
		Args:          map[string]interface{}{"domain": []interface{}{[]interface{}{"origin", "=", "$$purchase_order_name"}}, "limit": 1},
		CaptureIDFrom: "receipt_picking_id",
	})
	chain = append(chain, step{
		Label: "validate the receipt (goods received; this is what makes the bill non-zero)", OperationID: "inventory.transfer.validate",
		Args: map[string]interface{}{"target_id": "$receipt_picking_id"},
	})
	chain = append(chain, step{
		Label: "confirm stock actually moved as a side effect of the receipt", OperationID: "inventory.stock_level.read",
		Args: map[string]interface{}{"limit": 5},
	})
	chain = append(chain, step{
		Label: "create vendor bill directly (no wizard, asymmetric vs. sales.invoice.create)", OperationID: "procurement.bill.create",
		Args: map[string]interface{}{"target_id": "$purchase_order_id"}, CaptureIDFrom: "bill_id", CaptureField: "created_id",
	})
	chain = append(chain, step{
		Label: "set the vendor bill's invoice_date (required before posting, unlike customer invoices)", OperationID: "finance.invoice.update",
		Args: map[string]interface{}{"target_id": "$bill_id", "values": map[string]interface{}{"invoice_date": "2026-08-10"}},
	})
	chain = append(chain, step{
		Label: "assert the vendor bill carries a non-zero payable before posting", OperationID: "finance.invoice.describe",
		Args: map[string]interface{}{"target_id": "$bill_id"}, AssertAmountPositive: true,
	})
	chain = append(chain, step{
		Label: "post the vendor bill", OperationID: "finance.invoice.post",
		Args: map[string]interface{}{"target_id": "$bill_id"},
	})
	chain = append(chain, step{
		Label: "register payment on the vendor bill (reaches paid)", OperationID: "finance.payment.register",
		Args: map[string]interface{}{"target_id": "$bill_id"},
	})

	// ==== destructive guard: department delete ====
	chain = append(chain, step{
		Label: "create department", OperationID: "people.department.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Dept")}},
		CaptureIDFrom: "dept_id",
	})
	chain = append(chain, step{
		Label: "create employee in that department", OperationID: "people.employee.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Employee"), "department_id": "$dept_id"}},
		CaptureIDFrom: "employee_id",
	})
	chain = append(chain, step{
		Label: "describe the employee", OperationID: "people.employee.describe",
		Args: map[string]interface{}{"target_id": "$employee_id"},
	})
	chain = append(chain, step{
		// NOTE: intentionally run the destructive-guard test on the
		// department BEFORE offboarding the employee. action_archive
		// clears the employee's department_id as a side effect (the same
		// archive-cascade behavior phase-1 research found for
		// parent_id/coach_id) — offboarding first would leave zero
		// dependents and let Odoo's own state make the guard a no-op,
		// which would prove nothing about the guard itself.
		Label: "delete department WITHOUT confirming impact (expect DESTRUCTIVE_IMPACT_REQUIRES_CONFIRMATION)", OperationID: "people.department.delete",
		Args: map[string]interface{}{"target_id": "$dept_id"}, ExpectError: true,
	})
	chain = append(chain, step{
		Label: "delete department WITH confirmed impact (expect success)", OperationID: "people.department.delete",
		Args: map[string]interface{}{"target_id": "$dept_id", "confirm_impact": true},
	})
	chain = append(chain, step{
		Label: "offboard the employee (action_archive, act_window response treated as success)", OperationID: "people.employee.offboard",
		Args: map[string]interface{}{"target_id": "$employee_id"},
	})

	// ==== destructive guard: project delete ====
	chain = append(chain, step{
		Label: "create project", OperationID: "work.project.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Project")}},
		CaptureIDFrom: "project_id",
	})
	chain = append(chain, step{
		Label: "create task under project", OperationID: "work.task.create",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Task"), "project_id": "$project_id"}},
		CaptureIDFrom: "task_id",
	})
	chain = append(chain, step{
		Label: "update task state (field write, no action method exists in Odoo 19)", OperationID: "work.task.update",
		Args: map[string]interface{}{"target_id": "$task_id", "values": map[string]interface{}{"state": "1_done"}},
	})
	chain = append(chain, step{
		Label: "describe the task", OperationID: "work.task.describe",
		Args: map[string]interface{}{"target_id": "$task_id"},
	})

	// ==== work.milestone (FIELD_WRITE create then update, same operation) ====
	// Must run BEFORE the project is deleted below: project_id is required
	// on project.milestone.
	chain = append(chain, step{
		Label: "create milestone under the project", OperationID: "work.milestone.reach",
		Args:          map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Milestone"), "project_id": "$project_id"}},
		CaptureIDFrom: "milestone_id",
	})
	chain = append(chain, step{
		Label: "mark milestone reached", OperationID: "work.milestone.reach",
		Args: map[string]interface{}{"target_id": "$milestone_id", "values": map[string]interface{}{"is_reached": true}},
	})

	chain = append(chain, step{
		Label: "delete project WITHOUT confirming impact (expect refusal)", OperationID: "work.project.delete",
		Args: map[string]interface{}{"target_id": "$project_id"}, ExpectError: true,
	})
	chain = append(chain, step{
		Label: "delete project WITH confirmed impact (expect success, cascades the task and milestone)", OperationID: "work.project.delete",
		Args: map[string]interface{}{"target_id": "$project_id", "confirm_impact": true},
	})

	// ==== schedule ====
	chain = append(chain, step{
		Label: "create calendar event", OperationID: "schedule.event.create",
		Args: map[string]interface{}{"values": map[string]interface{}{"name": name("AERP Conformance Sync"), "start": "2026-09-01 09:00:00", "stop": "2026-09-01 10:00:00"}},
	})

	// ==== negative test: field outside the governed allowlist must be refused ====
	chain = append(chain, step{
		Label: "attempt to write a non-allowlisted field (expect governance rejection, never reaches Odoo)", OperationID: "people.employee.update",
		Args: map[string]interface{}{"target_id": "$employee_id", "values": map[string]interface{}{"active": false}}, ExpectError: true,
	})

	// ==== idempotency proof: identical (operation, arguments) submitted
	// twice in the SAME run must create exactly one provider record, not
	// two, and both calls must report the SAME execution_intent_id. ====
	idemArgs := map[string]interface{}{"values": map[string]interface{}{"name": fmt.Sprintf("AERP Idempotency Probe %s", runID), "is_company": true}}
	chain = append(chain, step{Label: "idempotency probe: first submission", OperationID: "identity.contact.create", Args: idemArgs, CaptureIDFrom: "idem_id_1"})
	chain = append(chain, step{Label: "idempotency probe: identical resubmission (expect replay, not a second create)", OperationID: "identity.contact.create", Args: idemArgs, CaptureIDFrom: "idem_id_2"})

	return chain
}
