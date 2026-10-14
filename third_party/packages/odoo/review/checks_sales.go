package review

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

func init() {
	register(check{
		id: "O19", name: "Bán dưới giá vốn", kind: kindSign,
		question: "Có ai bán hàng dưới giá vốn không, và có lặp lại không?",
		needs:    []string{opInvoice, opLine, "catalog.product.list", "catalog.product_variant.list"},
		formula:  "sale line net price (subtotal / quantity) below the product's cost, by seller; XEM_NGAY from repeat lines",
		params:   map[string]float64{"repeat": 3},
		run:      belowCost,
	})
	register(check{
		id: "O20", name: "Thuế suất khác thường lệ của sản phẩm", kind: kindErr,
		question: "Có dòng hoá đơn bán nào áp thuế suất khác với thuế suất sản phẩm đó vẫn mang không?",
		needs:    []string{opInvoice, opLine, "finance.tax.list"},
		formula:  "a sale line whose taxes differ from the taxes on >= dominance of that product's sale lines (at least min_lines lines)",
		params:   map[string]float64{"dominance": 0.9, "min_lines": 10},
		run:      taxMismatch,
	})
	register(check{
		id: "O21", name: "Giảm trừ sau khi đã thu tiền", kind: kindSign,
		question: "Có hoá đơn đã thu đủ tiền rồi mới bị giảm trừ và chi trả lại không?",
		needs:    []string{opInvoice},
		formula:  "a credit note reversing a customer invoice, dated >= min_days after it, and paid out",
		params:   map[string]float64{"min_days": 7},
		run:      creditNoteAfterPayment,
	})
	register(check{
		id: "O22", name: "Thu tiền trùng", kind: kindErr,
		question: "Có khoản thu nào bị ghi hai lần không?",
		needs:    []string{opPayment},
		formula:  "two inbound payments of one customer with the same amount, <= window_days apart, at least one not reconciled",
		params:   map[string]float64{"window_days": 3},
		run:      duplicatePayments,
	})
	register(check{
		id: "O15", name: "Công nợ quá hạn", kind: kindStat,
		question: "Khách nào đang nợ quá hạn, bao nhiêu, bao lâu?",
		needs:    []string{opInvoice},
		formula:  "posted customer invoices with an amount due past their due date at the as-of date, by customer, in buckets of days",
		params:   map[string]float64{"bucket_1": 30, "bucket_2": 60, "bucket_3": 90},
		run:      receivablesAging,
	})
}

type saleLine struct{ line, invoice map[string]interface{} }

func saleLines(ctx context.Context, b *books) ([]saleLine, error) {
	invoices, err := b.invoices(ctx, false, "out_invoice")
	if err != nil {
		return nil, err
	}
	lines, err := b.invoiceLines(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]map[string]interface{}{}
	for _, invoice := range invoices {
		byID[relID(invoice["id"])] = invoice
	}
	var out []saleLine
	for _, line := range lines {
		if invoice, ok := byID[relID(line["move_id"])]; ok && relID(line["product"]) != 0 {
			out = append(out, saleLine{line, invoice})
		}
	}
	return out, nil
}

func sellerOf(invoice map[string]interface{}) string {
	if name := relName(invoice["invoice_user_id"]); name != "" {
		return name
	}
	return "(không rõ)"
}

func belowCost(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := saleLines(ctx, b)
	if err != nil {
		return nil, err
	}
	type loss struct {
		saleLine
		price, cost float64
	}
	bySeller := map[string][]loss{}
	var sellers []string
	for _, row := range rows {
		cost, known, err := b.cost(ctx, relID(row.line["product"]))
		if err != nil {
			return nil, err
		}
		quantity := num(row.line["quantity"])
		if !known || cost == 0 || quantity <= 0 {
			continue
		}
		if price := num(row.line["price_subtotal"]) / quantity; price < cost {
			seller := sellerOf(row.invoice)
			if _, seen := bySeller[seller]; !seen {
				sellers = append(sellers, seller)
			}
			bySeller[seller] = append(bySeller[seller], loss{row, price, cost})
		}
	}
	sort.Strings(sellers)
	var out []Finding
	for _, seller := range sellers {
		list := bySeller[seller]
		total := 0.0
		customers := map[string]int{}
		for _, l := range list {
			total += (l.cost - l.price) * num(l.line["quantity"])
			customers[relName(l.invoice["partner"])]++
		}
		top, count := mostCommon(customers)
		why := fmt.Sprintf("%s: %d dòng bán dưới giá vốn, lỗ so với giá vốn %s", seller, len(list), money(total))
		if count > 1 {
			why += fmt.Sprintf("; %d trong số đó cho cùng khách %s.", count, quoted(top))
		} else {
			why += "."
		}
		var examples []string
		ev := make([]Evidence, 0, len(list))
		for i, l := range list {
			if i < 6 {
				examples = append(examples, fmt.Sprintf("%s %s: bán %s, vốn %s", text(l.invoice["name"]), relName(l.line["product"]), money(l.price), money(l.cost)))
			}
			ev = append(ev, evidence(opInvoice, l.invoice, ""))
		}
		severity := TheoDoi
		if float64(len(list)) >= p["repeat"] {
			severity = XemNgay
		}
		out = append(out, Finding{
			Title: fmt.Sprintf("%s bán dưới giá vốn %d lần", seller, len(list)), Severity: severity,
			Subject: Subject{Type: "seller", ID: seller, Name: seller},
			Why:     why + " " + strings.Join(examples, "; "), AmountVND: total, Evidence: ev,
			Facts: map[string]interface{}{"seller": seller, "lines": len(list), "loss": total, "topCustomer": top, "topCustomerLines": count},
		})
	}
	return out, nil
}

func mostCommon(counts map[string]int) (string, int) {
	best, n := "", 0
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if counts[key] > n {
			best, n = key, counts[key]
		}
	}
	return best, n
}

func taxMismatch(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := saleLines(ctx, b)
	if err != nil {
		return nil, err
	}
	keyOf := func(line map[string]interface{}) (string, []int64) {
		ids := relIDs(line["tax_ids"])
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return fmt.Sprint(ids), ids
	}
	usual := map[int64]map[string]int{}
	idsOf := map[string][]int64{}
	for _, row := range rows {
		product := relID(row.line["product"])
		key, ids := keyOf(row.line)
		idsOf[key] = ids
		if usual[product] == nil {
			usual[product] = map[string]int{}
		}
		usual[product][key]++
	}
	var out []Finding
	for _, row := range rows {
		counts := usual[relID(row.line["product"])]
		key, ids := keyOf(row.line)
		common, n := mostCommon(counts)
		total := 0
		for _, c := range counts {
			total += c
		}
		if key == common || float64(total) < p["min_lines"] || float64(n)/float64(total) < p["dominance"] {
			continue
		}
		have, err := b.taxLabel(ctx, ids)
		if err != nil {
			return nil, err
		}
		usualLabel, err := b.taxLabel(ctx, idsOf[common])
		if err != nil {
			return nil, err
		}
		product := relName(row.line["product"])
		out = append(out, Finding{
			Title: "Dòng hoá đơn áp thuế khác thường lệ", Severity: NenXuLy,
			Subject: Subject{Type: "invoice", ID: fmt.Sprint(relID(row.invoice["id"])), Name: text(row.invoice["name"])},
			Why: fmt.Sprintf("%s (%s, %s): %s mang thuế %s, trong khi %d/%d dòng bán sản phẩm này mang thuế %s. Doanh thu %s.",
				text(row.invoice["name"]), text(row.invoice["invoice_date"]), relName(row.invoice["invoice_user_id"]),
				product, have, n, total, usualLabel, money(num(row.line["price_subtotal"]))),
			AmountVND: num(row.line["price_subtotal"]), Evidence: []Evidence{evidence(opInvoice, row.invoice, "")},
			Facts: map[string]interface{}{"product": product, "taxIds": ids, "usualTaxIds": idsOf[common], "share": float64(n) / float64(total)},
		})
	}
	return out, nil
}

func creditNoteAfterPayment(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
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
		d1, _ := parseDay(original["invoice_date"])
		d2, _ := parseDay(refund["invoice_date"])
		gap := d1.daysUntil(d2)
		status := text(refund["payment_status"])
		refunded := status == "paid" || status == "in_payment"
		if float64(gap) < p["min_days"] || !refunded {
			continue
		}
		severity := NenXuLy
		if num(refund["total_amount"]) >= num(original["total_amount"]) {
			severity = XemNgay
		}
		out = append(out, Finding{
			Title: "Hoá đơn đã thu tiền bị giảm trừ và chi trả lại", Severity: severity,
			Subject: Subject{Type: "invoice", ID: fmt.Sprint(relID(original["id"])), Name: text(original["name"])},
			Why: fmt.Sprintf("%s (%s, %s, %s, người bán %s) bị giảm trừ bằng %s ngày %s (%d ngày sau), %s, và số tiền này đã được chi trả lại.",
				text(original["name"]), text(original["invoice_date"]), relName(original["partner"]), money(num(original["total_amount"])),
				relName(original["invoice_user_id"]), text(refund["name"]), text(refund["invoice_date"]), gap, money(num(refund["total_amount"]))),
			AmountVND: num(refund["total_amount"]),
			Evidence:  []Evidence{evidence(opInvoice, original, ""), evidence(opInvoice, refund, "")},
			Facts:     map[string]interface{}{"daysAfter": gap, "seller": relName(original["invoice_user_id"]), "refundedOut": refunded},
		})
	}
	return out, nil
}

func duplicatePayments(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	payments, err := b.payments(ctx, false)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]map[string]interface{}{}
	var keys []string
	for _, payment := range payments {
		if text(payment["payment_type"]) != "inbound" {
			continue
		}
		key := fmt.Sprintf("%d/%d", relID(payment["partner"]), int64(math.Round(num(payment["amount"]))))
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], payment)
	}
	sort.Strings(keys)
	var out []Finding
	for _, key := range keys {
		same := byKey[key]
		sort.SliceStable(same, func(i, j int) bool { return text(same[i]["date"]) < text(same[j]["date"]) })
		for i := 0; i+1 < len(same); i++ {
			first, second := same[i], same[i+1]
			d1, _ := parseDay(first["date"])
			d2, _ := parseDay(second["date"])
			gap := d1.daysUntil(d2)
			var loose []string
			for _, payment := range []map[string]interface{}{first, second} {
				if reconciled, isBool := payment["is_reconciled"].(bool); isBool && !reconciled {
					loose = append(loose, text(payment["name"]))
				}
			}
			if float64(gap) > p["window_days"] || len(loose) == 0 {
				continue
			}
			severity := NenXuLy
			if len(loose) == 1 {
				severity = XemNgay
			}
			customer := relName(first["partner"])
			out = append(out, Finding{
				Title: "Một khoản thu có thể bị ghi hai lần", Severity: severity,
				Subject: Subject{Type: "customer", ID: fmt.Sprint(relID(first["partner"])), Name: customer},
				Why: fmt.Sprintf("%s: %s (%s) và %s (%s), cùng %s; %s không khớp với hoá đơn nào.",
					customer, text(first["name"]), text(first["date"]), text(second["name"]), text(second["date"]),
					money(num(first["amount"])), strings.Join(loose, ", ")),
				AmountVND: num(first["amount"]),
				Evidence:  []Evidence{evidence(opPayment, first, ""), evidence(opPayment, second, "")},
				Facts:     map[string]interface{}{"daysApart": gap, "unreconciled": loose},
			})
		}
	}
	return out, nil
}

func receivablesAging(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	invoices, err := b.invoices(ctx, true, "out_invoice")
	if err != nil {
		return nil, err
	}
	edges := []int{int(p["bucket_1"]), int(p["bucket_2"]), int(p["bucket_3"])}
	labels := []string{fmt.Sprintf("1-%d", edges[0]), fmt.Sprintf("%d-%d", edges[0]+1, edges[1]), fmt.Sprintf("%d-%d", edges[1]+1, edges[2]), fmt.Sprintf(">%d", edges[2])}
	overdue := map[string]map[string]float64{}
	docs := map[string][]map[string]interface{}{}
	worst := map[string]int{}
	var customers []string
	for _, invoice := range invoices {
		due, ok := parseDay(invoice["invoice_date_due"])
		residual := num(invoice["amount_residual"])
		if residual <= 0 || !ok || !due.before(b.asOf) {
			continue
		}
		late := due.daysUntil(b.asOf)
		index := len(edges)
		for i, edge := range edges {
			if late <= edge {
				index = i
				break
			}
		}
		customer := relName(invoice["partner"])
		if _, seen := overdue[customer]; !seen {
			customers = append(customers, customer)
			overdue[customer] = map[string]float64{}
		}
		overdue[customer][labels[index]] += residual
		docs[customer] = append(docs[customer], invoice)
		if late > worst[customer] {
			worst[customer] = late
		}
	}
	sort.Strings(customers)
	var out []Finding
	for _, customer := range customers {
		total := 0.0
		var parts []string
		for _, label := range labels {
			if amount, present := overdue[customer][label]; present {
				total += amount
				parts = append(parts, fmt.Sprintf("%s quá hạn %s ngày", money(amount), label))
			}
		}
		// Overdue money is the state of the business, not a fault: it ranks below errors and fraud signs.
		severity := TheoDoi
		if worst[customer] > edges[0] {
			severity = NenXuLy
		}
		ev := make([]Evidence, 0, len(docs[customer]))
		for _, invoice := range docs[customer] {
			ev = append(ev, evidence(opInvoice, invoice, ""))
		}
		out = append(out, Finding{
			Title: fmt.Sprintf("%s nợ quá hạn %s", customer, money(total)), Severity: severity,
			Subject:   Subject{Type: "customer", Name: customer},
			Why:       fmt.Sprintf("%s: %s; lâu nhất %d ngày.", customer, strings.Join(parts, ", "), worst[customer]),
			AmountVND: total, Evidence: ev,
			Facts: map[string]interface{}{"customer": customer, "buckets": overdue[customer], "total": total, "oldestDays": worst[customer]},
		})
	}
	return out, nil
}
