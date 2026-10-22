package review

import (
	"fmt"
	"strings"
	"testing"
)

func customerInvoice(id, customer int, date, created string, total float64) map[string]interface{} {
	return map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("INV/%d", id), "document_type": "out_invoice", "state": "posted",
		"invoice_date": date, "date": date, "create_date": created + " 09:00:00", "create_uid": []interface{}{2.0, "Lan"},
		"total_amount": total, "amount_untaxed": total / 1.1, "payment_status": "not_paid",
		"currency_id": []interface{}{1.0, "VND"}, "invoice_user_id": []interface{}{2.0, "Lan"},
		"partner": map[string]interface{}{"id": float64(customer), "display_name": fmt.Sprintf("KH %d", customer)}}
}

func creditNote(id, customer, reverses int, date string, total float64) map[string]interface{} {
	row := customerInvoice(id, customer, date, date, total)
	row["name"], row["document_type"], row["reversed_entry_id"] = fmt.Sprintf("RINV/%d", id), "out_refund", []interface{}{float64(reverses), "INV"}
	return row
}

func vendorBill(id, vendor int, date string, untaxed, vat float64) map[string]interface{} {
	return map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("BILL/%d", id), "document_type": "in_invoice", "state": "posted",
		"invoice_date": date, "date": date, "total_amount": untaxed + vat, "amount_untaxed": untaxed, "payment_status": "paid",
		"currency_id": []interface{}{1.0, "VND"}, "partner": map[string]interface{}{"id": float64(vendor), "display_name": fmt.Sprintf("NCC %d", vendor)}}
}

func outbound(id, partner int, date string, amount float64, journal int, bills ...int) map[string]interface{} {
	ids := make([]interface{}, 0, len(bills))
	for _, b := range bills {
		ids = append(ids, float64(b))
	}
	return map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("PAY/%d", id), "payment_type": "outbound", "state": "paid",
		"partner": []interface{}{float64(partner), fmt.Sprintf("NCC %d", partner)}, "amount": amount, "date": date,
		"journal_id": []interface{}{float64(journal), "sổ"}, "reconciled_bill_ids": ids}
}

func line(move, product int, date string, qty, price float64) map[string]interface{} {
	return map[string]interface{}{"id": float64(move*100 + product), "move_id": []interface{}{float64(move), "INV"}, "product_id": []interface{}{float64(product), "SP"},
		"quantity": qty, "price_unit": price, "price_subtotal": qty * price, "date": date}
}

func findings(report *Report, check string) []Finding {
	var out []Finding
	for _, f := range report.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestACustomerInvoicedTwiceIsFoundAndAReorderIsNot(t *testing.T) {
	invoices := []map[string]interface{}{
		// The same sale invoiced twice on one day: same lines.
		customerInvoice(1, 7, "2026-06-03", "2026-06-03", 8_800_000), customerInvoice(2, 7, "2026-06-03", "2026-06-03", 8_800_000),
		// Same total two days apart, other goods: worth a look only.
		customerInvoice(3, 8, "2026-07-01", "2026-07-01", 5_500_000), customerInvoice(4, 8, "2026-07-03", "2026-07-03", 5_500_000),
		// The same monthly total, a month apart: no finding.
		customerInvoice(5, 9, "2026-05-05", "2026-05-05", 3_300_000), customerInvoice(6, 9, "2026-06-05", "2026-06-05", 3_300_000),
		// A duplicate already reversed by a credit note: dealt with.
		customerInvoice(7, 10, "2026-08-10", "2026-08-10", 2_200_000), customerInvoice(8, 10, "2026-08-10", "2026-08-10", 2_200_000),
		creditNote(9, 10, 8, "2026-08-12", 2_200_000),
	}
	lines := []map[string]interface{}{
		line(1, 50, "2026-06-03", 10, 800_000), line(2, 50, "2026-06-03", 10, 800_000),
		line(3, 51, "2026-07-01", 5, 1_000_000), line(4, 52, "2026-07-03", 2, 2_500_000),
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices, opLine: lines}}, "O02")
	got := findings(report, "O02")
	if len(got) != 2 || got[0].Severity != XemNgay || got[0].Facts["sameLines"] != true || got[1].Severity != NenXuLy {
		t.Fatalf("findings %+v", got)
	}
	if got[0].Evidence[0].ID != 1 || got[0].Evidence[1].ID != 2 || got[1].Evidence[0].ID != 3 {
		t.Fatalf("evidence %+v / %+v", got[0].Evidence, got[1].Evidence)
	}
}

func TestAPaymentSplitUnderTheLimitAndSeparateBillsAreNot(t *testing.T) {
	payments := []map[string]interface{}{
		// One 24-million bill paid in three parts over two days.
		outbound(1, 7, "2026-07-06", 8_000_000, 5, 100), outbound(2, 7, "2026-07-06", 8_000_000, 5, 100), outbound(3, 7, "2026-07-07", 8_000_000, 5, 100),
		// Three bills of one vendor paid in a week, one payment each: ordinary.
		outbound(4, 8, "2026-07-06", 9_000_000, 5, 201), outbound(5, 8, "2026-07-08", 9_000_000, 5, 202), outbound(6, 8, "2026-07-10", 9_000_000, 5, 203),
		// Two payments with no bill at all, five days apart.
		outbound(7, 9, "2026-08-03", 12_000_000, 5), outbound(8, 9, "2026-08-07", 11_000_000, 5),
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opPayment: payments}}, "O07")
	got := findings(report, "O07")
	if len(got) != 2 {
		t.Fatalf("findings %+v", got)
	}
	if got[0].Subject.ID != "7" || got[0].Severity != XemNgay || len(got[0].Evidence) != 3 || got[0].AmountVND != 24_000_000 {
		t.Fatalf("split payment %+v", got[0])
	}
	if got[1].Subject.ID != "9" || got[1].Severity != NenXuLy || len(got[1].Facts["unbilled"].([]string)) != 2 {
		t.Fatalf("unbilled payments %+v", got[1])
	}
}

func TestACheckWhoseFieldIsNotReturnedIsNotRun(t *testing.T) {
	old := outbound(1, 7, "2026-07-06", 8_000_000, 5, 100)
	delete(old, "reconciled_bill_ids")
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opPayment: {old}}}, "O07")
	if report.Checks[0].Ran || !strings.Contains(report.Checks[0].NotRun, "reconciled_bill_ids") {
		t.Fatalf("checks %+v", report.Checks)
	}
}

func TestBackdatedInvoicesAndEntriesAreFoundAndMonthEndClosingIsNot(t *testing.T) {
	invoices := []map[string]interface{}{
		customerInvoice(1, 7, "2026-06-30", "2026-07-10", 55_000_000), // ten days after June ended
		customerInvoice(2, 7, "2026-06-30", "2026-07-02", 11_000_000), // two days: closing
		customerInvoice(3, 8, "2026-07-15", "2026-07-15", 22_000_000), // made on its day
	}
	entry := func(id int, date, created string, amount float64, fromPayment bool) map[string]interface{} {
		row := map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("MISC/%d", id), "state": "posted", "date": date,
			"create_date": created + " 10:00:00", "create_uid": []interface{}{3.0, "Mai"}, "amount_total": amount,
			"journal_id": []interface{}{4.0, "Nghiệp vụ khác"}, "origin_payment_id": false, "statement_line_id": false}
		if fromPayment {
			row["origin_payment_id"] = []interface{}{9.0, "PAY/9"}
		}
		return row
	}
	entries := []map[string]interface{}{
		entry(10, "2026-06-30", "2026-07-20", 12_000_000, false), // twenty days late
		entry(11, "2026-06-30", "2026-07-08", 30_000_000, false), // eight days: closing
		entry(12, "2026-06-30", "2026-07-25", 40_000_000, true),  // made by a payment
		entry(13, "2026-08-31", "2026-09-30", 80_000_000, false), // late and large
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices, opEntry: entries}}, "O26")
	got := findings(report, "O26")
	var ids []string
	for _, f := range got {
		ids = append(ids, fmt.Sprintf("%d/%s", f.Evidence[0].ID, f.Severity))
	}
	if strings.Join(ids, ",") != "1/XEM_NGAY,13/XEM_NGAY,10/NEN_XU_LY" && strings.Join(ids, ",") != "13/XEM_NGAY,1/XEM_NGAY,10/NEN_XU_LY" {
		t.Fatalf("findings %v", ids)
	}
	if got[0].Facts["daysAfterMonthEnd"] == nil {
		t.Fatalf("facts %+v", got[0].Facts)
	}
}

func TestPeriodEndRevenueReversedEarlyNextMonth(t *testing.T) {
	invoices := []map[string]interface{}{
		customerInvoice(1, 7, "2026-06-28", "2026-06-28", 100_000_000), creditNote(2, 7, 1, "2026-07-03", 100_000_000), // full, quarter end
		customerInvoice(3, 8, "2026-07-30", "2026-07-30", 50_000_000), creditNote(4, 8, 3, "2026-08-05", 30_000_000), // 60%
		customerInvoice(5, 9, "2026-06-10", "2026-06-10", 40_000_000), creditNote(6, 9, 5, "2026-07-02", 40_000_000), // mid-month sale
		customerInvoice(7, 9, "2026-07-31", "2026-07-31", 40_000_000), creditNote(8, 9, 7, "2026-09-10", 40_000_000), // weeks later
		customerInvoice(9, 9, "2026-08-29", "2026-08-29", 40_000_000), creditNote(10, 9, 9, "2026-09-02", 4_000_000), // 10%
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices}}, "O27")
	got := findings(report, "O27")
	if len(got) != 2 || got[0].Evidence[0].ID != 1 || got[0].Severity != XemNgay || got[0].Facts["quarterEnd"] != true {
		t.Fatalf("findings %+v", got)
	}
	if got[1].Evidence[0].ID != 3 || got[1].Severity != NenXuLy {
		t.Fatalf("partial %+v", got[1])
	}
}

func TestCashPaidPurchasesLoseTheirVATDeduction(t *testing.T) {
	journals := []map[string]interface{}{{"id": 5.0, "type": "bank"}, {"id": 6.0, "type": "cash"}}
	bills := []map[string]interface{}{
		vendorBill(1, 7, "2026-07-06", 10_000_000, 1_000_000), // 11 million in cash: O28
		vendorBill(2, 7, "2026-07-08", 10_000_000, 1_000_000), // 11 million by bank
		vendorBill(3, 8, "2026-07-09", 3_000_000, 300_000),    // 3,3 million in cash: under the threshold
		vendorBill(4, 9, "2026-07-10", 2_000_000, 200_000),    // three bills of one seller on one day, cash: O29
		vendorBill(5, 9, "2026-07-10", 2_000_000, 200_000),
		vendorBill(6, 9, "2026-07-10", 1_000_000, 100_000),
		vendorBill(7, 10, "2026-07-11", 2_000_000, 200_000), // two bills, 4,4 million: under the threshold together
		vendorBill(8, 10, "2026-07-11", 2_000_000, 200_000),
	}
	usd := vendorBill(9, 11, "2026-07-12", 1_000, 100)
	usd["currency_id"] = []interface{}{2.0, "USD"}
	bills = append(bills, usd)
	payments := []map[string]interface{}{
		outbound(1, 7, "2026-07-06", 11_000_000, 6, 1), outbound(2, 7, "2026-07-09", 11_000_000, 5, 2), outbound(3, 8, "2026-07-09", 3_300_000, 6, 3),
		outbound(4, 9, "2026-07-10", 5_500_000, 6, 4, 5, 6), outbound(5, 10, "2026-07-11", 4_400_000, 6, 7, 8), outbound(6, 11, "2026-07-12", 1_100, 6, 9),
	}
	books := fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: bills, opPayment: payments, opJournal: journals}}
	report := run(t, books, "O28", "O29")
	o28, o29 := findings(report, "O28"), findings(report, "O29")
	if len(o28) != 1 || o28[0].Evidence[0].ID != 1 || o28[0].AmountVND != 1_000_000 {
		t.Fatalf("O28 %+v", o28)
	}
	if len(o29) != 1 || o29[0].Subject.ID != "9" || o29[0].AmountVND != 500_000 || !strings.Contains(o29[0].Why, "khoản 3") {
		t.Fatalf("O29 %+v", o29)
	}
}

// A credit note that cancels an invoice nobody paid moves no money: Odoo marks the credit note paid and the
// invoice reversed. Only a paid invoice refunded out is O21's case.
func TestACreditNoteCancellingAnUnpaidInvoiceIsNoRefund(t *testing.T) {
	paid := customerInvoice(1, 7, "2026-06-16", "2026-06-16", 50_000_000)
	paid["payment_status"] = "paid"
	refundOut := creditNote(2, 7, 1, "2026-07-03", 50_000_000)
	refundOut["payment_status"] = "paid"
	unpaid := customerInvoice(3, 8, "2026-06-29", "2026-06-29", 40_000_000)
	unpaid["payment_status"] = "reversed"
	cancel := creditNote(4, 8, 3, "2026-07-06", 40_000_000)
	cancel["payment_status"] = "paid"
	// A return of part of the goods, three weeks on: the credit note is set against the invoice, whose
	// status then reads partial, and no money goes back (the 12-month simulation's look-alike).
	partlyReturned := customerInvoice(5, 9, "2026-07-01", "2026-07-01", 10_000_000)
	partlyReturned["payment_status"] = "partial"
	returned := creditNote(6, 9, 5, "2026-07-20", 3_000_000)
	returned["payment_status"] = "paid"
	refundPayment := outbound(7, 7, "2026-07-04", 50_000_000, 1)
	refundPayment["reconciled_invoice_ids"] = []interface{}{2.0}
	books := fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: {paid, refundOut, unpaid, cancel, partlyReturned, returned}, opPayment: {refundPayment}}}
	got := findings(run(t, books, "O21"), "O21")
	if len(got) != 1 || got[0].Evidence[0].ID != 1 {
		t.Fatalf("findings %+v", got)
	}
}

// A return credited on one of two copies leaves the customer billed twice for the rest; only a whole
// reversal deals with a copy (the weekly simulation of 2026-10-19, seed 21).
func TestAPartReturnDoesNotHideACopiedInvoice(t *testing.T) {
	invoices := []map[string]interface{}{
		customerInvoice(1, 7, "2026-07-21", "2026-07-21", 9_900_000), customerInvoice(2, 7, "2026-07-21", "2026-07-21", 9_900_000),
		creditNote(3, 7, 1, "2026-08-04", 1_980_000),
		// Reversed in two credit notes that add up to the whole: dealt with.
		customerInvoice(4, 8, "2026-08-10", "2026-08-10", 2_200_000), customerInvoice(5, 8, "2026-08-10", "2026-08-10", 2_200_000),
		creditNote(6, 8, 5, "2026-08-11", 1_100_000), creditNote(7, 8, 5, "2026-08-12", 1_100_000),
	}
	lines := []map[string]interface{}{line(1, 50, "2026-07-21", 10, 900_000), line(2, 50, "2026-07-21", 10, 900_000)}
	got := findings(run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices, opLine: lines}}, "O02"), "O02")
	if len(got) != 1 || got[0].Evidence[0].ID != 1 || got[0].Evidence[1].ID != 2 || got[0].Severity != XemNgay {
		t.Fatalf("findings %+v", got)
	}
	if !strings.Contains(got[0].Why, "chưa hoá đơn nào bị giảm trừ toàn bộ") || !strings.Contains(got[0].Why, "1.980.000") {
		t.Fatalf("why: %s", got[0].Why)
	}
}

// Two invoices of one customer, same goods, two days apart, each from its own sale order, are two sales;
// an invoice copied by hand has no origin.
func TestTwoSaleOrdersAreTwoSalesAndACopiedInvoiceIsNot(t *testing.T) {
	first, copied := customerInvoice(1, 7, "2026-06-03", "2026-06-03", 8_800_000), customerInvoice(2, 7, "2026-06-03", "2026-06-03", 8_800_000)
	first["invoice_origin"] = "S00012"
	again, reorder := customerInvoice(3, 8, "2026-08-20", "2026-08-20", 400_400), customerInvoice(4, 8, "2026-08-22", "2026-08-22", 400_400)
	again["invoice_origin"], reorder["invoice_origin"] = "S00401", "S00405"
	lines := []map[string]interface{}{line(1, 50, "2026-06-03", 10, 800_000), line(2, 50, "2026-06-03", 10, 800_000),
		line(3, 51, "2026-08-20", 14, 26_000), line(4, 51, "2026-08-22", 14, 26_000)}
	got := findings(run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: {first, copied, again, reorder}, opLine: lines}}, "O02"), "O02")
	if len(got) != 1 || got[0].Evidence[0].ID != 1 || got[0].Evidence[1].ID != 2 {
		t.Fatalf("findings %+v", got)
	}
}

// One seller selling below cost again and again is a sign; the same product marked down by every seller
// in one week is the shop's price (the 12-month simulation's clearance week).
func TestBelowCostIsOneSellersHabitNotAShopsMarkdown(t *testing.T) {
	var invoices, lines []map[string]interface{}
	sale := func(id int, seller, date string, customer, product int, price float64) {
		invoice := customerInvoice(id, customer, date, date, price*2)
		invoice["invoice_user_id"] = []interface{}{float64(len(seller)), seller}
		invoices = append(invoices, invoice)
		row := line(id, product, date, 2, price)
		row["product"] = []interface{}{float64(product), "SP"}
		lines = append(lines, row)
	}
	// Tuấn, alone, four times to one customer, product 60 (cost 2,000,000).
	for i, date := range []string{"2026-06-24", "2026-07-15", "2026-08-19", "2026-09-09"} {
		sale(1+i, "Tuấn", date, 14, 60, 1_700_000)
	}
	// The clearance week: product 61 (cost 1,150,000) sold at 1,035,000 by all three sellers.
	id := 20
	for _, date := range []string{"2026-08-24", "2026-08-25", "2026-08-26"} {
		for customer, seller := range []string{"Lan", "Hùng", "Tuấn"} {
			sale(id, seller, date, customer, 61, 1_035_000)
			id++
		}
	}
	products := []map[string]interface{}{{"id": 600.0, "standard_price": 2_000_000.0}, {"id": 610.0, "standard_price": 1_150_000.0}}
	variants := []map[string]interface{}{{"id": 60.0, "product_tmpl_id": []interface{}{600.0, "Khoan"}}, {"id": 61.0, "product_tmpl_id": []interface{}{610.0, "Sơn lót"}}}
	books := fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices, opLine: lines,
		"catalog.product.list": products, "catalog.product_variant.list": variants}}
	got := findings(run(t, books, "O19"), "O19")
	if len(got) != 1 || got[0].Facts["seller"] != "Tuấn" || got[0].Facts["lines"] != 4 || got[0].Severity != XemNgay {
		t.Fatalf("findings %+v", got)
	}
}

// Where refunds are routine, a single refund is not flagged; an invoice refunded twice or in full, and a
// seller whose refunds stand out, still are.
func TestRoutineRefundsFlagOnlyWhatIsUnusualEvenThere(t *testing.T) {
	var invoices, payments []map[string]interface{}
	refundID := 1000
	for i := 1; i <= 60; i++ {
		inv := customerInvoice(i, 100+i, "2026-07-01", "2026-07-01", 1_000_000)
		inv["payment_status"] = "paid"
		seller := "Lan"
		if i <= 12 {
			seller = "Tuấn"
		}
		inv["invoice_user_id"] = []interface{}{1.0, seller}
		invoices = append(invoices, inv)
	}
	refund := func(original int, date string, amount float64) {
		refundID++
		cn := creditNote(refundID, 100+original, original, date, amount)
		cn["payment_status"] = "paid"
		invoices = append(invoices, cn)
		pay := outbound(5000+refundID, 100+original, date, amount, 1)
		pay["reconciled_invoice_ids"] = []interface{}{float64(refundID)}
		payments = append(payments, pay)
	}
	// Tuấn's twelve invoices: six refunded. Lan's forty-eight: two refunded, one of them twice.
	for i := 1; i <= 6; i++ {
		refund(i, "2026-07-20", 200_000)
	}
	refund(30, "2026-07-20", 300_000)
	refund(50, "2026-07-20", 1_000_000) // the whole invoice
	refund(40, "2026-07-20", 100_000)
	refund(40, "2026-07-25", 100_000)
	got := findings(run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: invoices, opPayment: payments}}, "O21"), "O21")
	var twice, whole, seller int
	for _, f := range got {
		switch f.Subject.Type {
		case "invoice":
			if f.Facts["refunds"] == 2 {
				twice++
			} else if f.Facts["refunded"] == 1_000_000.0 {
				whole++
			} else {
				t.Errorf("invoice finding %+v", f)
			}
		case "seller":
			seller++
			if f.Facts["seller"] != "Tuấn" {
				t.Errorf("seller finding %+v", f)
			}
		}
	}
	if len(got) != 3 || twice != 1 || whole != 1 || seller != 1 {
		t.Fatalf("findings %+v", got)
	}
}
