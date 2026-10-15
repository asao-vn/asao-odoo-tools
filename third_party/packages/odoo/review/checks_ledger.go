package review

import (
	"context"
	"fmt"
)

func init() {
	register(check{
		id: "O26", name: "Chứng từ ghi lùi ngày", kind: kindSign,
		question: "Có hoá đơn bán hay bút toán tay nào được tạo khi tháng đã qua nhưng lại ghi ngày vào tháng đó không?",
		needs:    []string{opInvoice, opEntry},
		formula:  "customer invoices and credit notes created more than invoice_lag_days, and manual journal entries (no payment, no bank statement line) created more than entry_lag_days, after the end of the month of their accounting date",
		params:   map[string]float64{"invoice_lag_days": 3, "entry_lag_days": 10, "high_value": 50_000_000},
		run:      backdated,
	})
	register(check{
		id: "O27", name: "Doanh thu cuối tháng bị giảm trừ đầu tháng sau", kind: kindSign,
		question: "Có hoá đơn bán ghi vào những ngày cuối tháng rồi bị giảm trừ ngay đầu tháng sau không?",
		needs:    []string{opInvoice},
		formula:  "customer invoices dated in the last end_days of a month and reversed, for >= min_share of their total, by credit notes dated in the first start_days of the next month",
		params:   map[string]float64{"end_days": 5, "start_days": 15, "min_share": 0.5},
		run:      periodEndReversals,
	})
}

// backdated finds documents written into a month after it had ended. Closing a month takes a few days, so
// an entry made early in the next month and dated at the end of the closed one is ordinary; a customer
// invoice made two weeks later and dated back is a sale moved into a month it did not happen in. Vendor
// bills are left out: they arrive late by nature.
func backdated(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	invoices, err := b.invoices(ctx, false, "out_invoice", "out_refund")
	if err != nil {
		return nil, err
	}
	if err := requireField(opInvoice, "create_date", invoices); err != nil {
		return nil, err
	}
	entries, err := b.read(ctx, opEntry, []interface{}{[]interface{}{"state", "=", "posted"}})
	if err != nil {
		return nil, err
	}
	if err := requireField(opEntry, "create_date", entries); err != nil {
		return nil, err
	}
	var out []Finding
	late := func(row map[string]interface{}, dateField string) (date, date, int, bool) {
		when, ok := parseDay(row[dateField])
		created, okCreated := parseDay(row["create_date"])
		if !ok || !okCreated {
			return date{}, date{}, 0, false
		}
		return when, created, when.monthEnd().daysUntil(created), true
	}
	for _, invoice := range invoices {
		field := "date"
		if _, has := invoice["date"]; !has {
			field = "invoice_date"
		}
		when, created, days, ok := late(invoice, field)
		if !ok || float64(days) <= p["invoice_lag_days"] {
			continue
		}
		kind := "Hoá đơn bán"
		if text(invoice["document_type"]) == "out_refund" {
			kind = "Giảm trừ hoá đơn bán"
		}
		out = append(out, Finding{
			Title: kind + " ghi lùi ngày vào tháng đã qua", Severity: XemNgay,
			Subject: Subject{Type: "invoice", ID: fmt.Sprint(relID(invoice["id"])), Name: text(invoice["name"])},
			Why: fmt.Sprintf("%s %s (%s, %s) ghi ngày %s nhưng được tạo ngày %s, %d ngày sau khi tháng %s đã kết thúc; người tạo %s.",
				kind, text(invoice["name"]), relName(invoice["partner"]), money(num(invoice["total_amount"])), when.format(),
				created.format(), days, when.month(), relName(invoice["create_uid"])),
			AmountVND: num(invoice["total_amount"]),
			Evidence:  []Evidence{evidence(opInvoice, invoice, "")},
			Facts: map[string]interface{}{"date": when.format(), "created": created.format(), "daysAfterMonthEnd": days,
				"createdBy": relName(invoice["create_uid"])},
		})
	}
	for _, entry := range entries {
		if !b.inPeriod(entry["date"]) || relID(entry["origin_payment_id"]) != 0 || relID(entry["statement_line_id"]) != 0 {
			continue
		}
		when, created, days, ok := late(entry, "date")
		if !ok || float64(days) <= p["entry_lag_days"] {
			continue
		}
		amount := num(entry["amount_total"])
		severity := NenXuLy
		if amount >= p["high_value"] {
			severity = XemNgay
		}
		out = append(out, Finding{
			Title: "Bút toán tay ghi lùi ngày vào tháng đã qua", Severity: severity,
			Subject: Subject{Type: "entry", ID: fmt.Sprint(relID(entry["id"])), Name: text(entry["name"])},
			Why: fmt.Sprintf("Bút toán %s (%s, sổ %s) ghi ngày %s nhưng được tạo ngày %s, %d ngày sau khi tháng %s đã kết thúc; người tạo %s.",
				text(entry["name"]), money(amount), relName(entry["journal_id"]), when.format(), created.format(), days, when.month(),
				relName(entry["create_uid"])),
			AmountVND: amount,
			Evidence:  []Evidence{evidence(opEntry, entry, "")},
			Facts: map[string]interface{}{"date": when.format(), "created": created.format(), "daysAfterMonthEnd": days,
				"createdBy": relName(entry["create_uid"]), "journal": relName(entry["journal_id"])},
		})
	}
	return out, nil
}

// periodEndReversals finds revenue booked in the last days of a month and taken back early in the next:
// the month looks better than it was. A return or a correction can do the same, so it is a sign to look at.
func periodEndReversals(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	originals, err := b.invoices(ctx, true, "out_invoice")
	if err != nil {
		return nil, err
	}
	refunds, err := b.invoices(ctx, false, "out_refund")
	if err != nil {
		return nil, err
	}
	byID := map[int64]map[string]interface{}{}
	for _, invoice := range originals {
		byID[relID(invoice["id"])] = invoice
	}
	var out []Finding
	for _, refund := range refunds {
		original, ok := byID[relID(refund["reversed_entry_id"])]
		if !ok {
			continue
		}
		sold, okSold := parseDay(original["invoice_date"])
		taken, okTaken := parseDay(refund["invoice_date"])
		if !okSold || !okTaken {
			continue
		}
		end := sold.monthEnd()
		share := 0.0
		if total := num(original["total_amount"]); total > 0 {
			share = num(refund["total_amount"]) / total
		}
		if float64(sold.daysUntil(end)) >= p["end_days"] || !taken.after(end) || float64(end.daysUntil(taken)) > p["start_days"] || share < p["min_share"] {
			continue
		}
		severity := NenXuLy
		if share >= 0.99 {
			severity = XemNgay
		}
		_, month, _ := end.t.Date()
		quarterEnd := month%3 == 0
		closing := "tháng " + end.month()
		if quarterEnd {
			closing = "tháng " + end.month() + ", cuối quý"
		}
		out = append(out, Finding{
			Title: "Doanh thu cuối tháng bị giảm trừ ngay đầu tháng sau", Severity: severity,
			Subject: Subject{Type: "invoice", ID: fmt.Sprint(relID(original["id"])), Name: text(original["name"])},
			Why: fmt.Sprintf("%s (%s, %s, người bán %s) ghi ngày %s, những ngày cuối %s, rồi bị giảm trừ %s bằng %s ngày %s, %d ngày sau khi tháng kết thúc.",
				text(original["name"]), relName(original["partner"]), money(num(original["total_amount"])), relName(original["invoice_user_id"]),
				sold.format(), closing, pct(share, 0), text(refund["name"]), taken.format(), end.daysUntil(taken)),
			AmountVND: num(refund["total_amount"]),
			Evidence:  []Evidence{evidence(opInvoice, original, ""), evidence(opInvoice, refund, "")},
			Facts: map[string]interface{}{"monthEnd": end.format(), "quarterEnd": quarterEnd, "share": share,
				"daysAfterMonthEnd": end.daysUntil(taken), "seller": relName(original["invoice_user_id"])},
		})
	}
	return out, nil
}
