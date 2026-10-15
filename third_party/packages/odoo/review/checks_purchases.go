package review

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	opInvoice  = "finance.invoice.list"
	opLine     = "finance.invoice.line.list"
	opPayment  = "finance.payment.list"
	opContact  = "identity.contact.list"
	opBank     = "identity.bank_account.list"
	opEmployee = "people.employee.list"
	opExpense  = "people.expense.list"
	opLeave    = "workforce.leave.list"
	opScrap    = "inventory.scrap.list"
	opRFQ      = "procurement.rfq.list"
	opEntry    = "finance.journal_entry.list"
	opJournal  = "finance.journal.list"
)

func init() {
	register(check{
		id: "O01", name: "Hoá đơn mua trùng", kind: kindErr,
		question: "Có hoá đơn của nhà cung cấp nào bị nhập (và trả) hai lần không?",
		needs:    []string{opInvoice},
		formula:  "same vendor and the same reference compared on letters and digits; or same vendor and total within window_days when a bill has no reference",
		params:   map[string]float64{"window_days": 14},
		run:      duplicateBills,
	})
	register(check{
		id: "O16", name: "Chia nhỏ đơn mua", kind: kindSign,
		question: "Có đơn mua nào bị chia nhỏ để lọt dưới ngưỡng phải duyệt không?",
		needs:    []string{opRFQ},
		formula:  "purchase orders of one vendor within window_days, each total (VAT included) in [band x threshold, threshold), together >= threshold, at least min_orders of them",
		params:   map[string]float64{"threshold": 20_000_000, "band": 0.8, "window_days": 7, "min_orders": 2},
		run:      splitPurchases,
	})
	register(check{
		id: "O07", name: "Chia nhỏ khoản chi", kind: kindSign,
		question: "Có khoản chi nào bị tách thành nhiều lần nhỏ để lọt dưới hạn mức duyệt chi không?",
		needs:    []string{opPayment},
		formula:  "outbound payments to one partner within window_days, each below limit, together >= limit, at least min_payments of them, that pay parts of one bill or no bill at all",
		params:   map[string]float64{"limit": 20_000_000, "window_days": 7, "min_payments": 2},
		run:      splitPayments,
	})
}

// splitPayments finds a payment cut into pieces under the approval limit. Paying several bills of one
// vendor in a week is ordinary; what is not is several payments that each pay part of the same bill, or
// pay no bill at all, which is how a large payment is kept under the limit.
func splitPayments(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	payments, err := b.payments(ctx, false)
	if err != nil {
		return nil, err
	}
	var outbound []map[string]interface{}
	for _, payment := range payments {
		if text(payment["payment_type"]) == "outbound" {
			outbound = append(outbound, payment)
		}
	}
	if err := requireField(opPayment, "reconciled_bill_ids", outbound); err != nil {
		return nil, err
	}
	byPartner := map[int64][]map[string]interface{}{}
	var partners []int64
	for _, payment := range outbound {
		if num(payment["amount"]) >= p["limit"] {
			continue
		}
		partner := relID(payment["partner"])
		if _, seen := byPartner[partner]; !seen {
			partners = append(partners, partner)
		}
		byPartner[partner] = append(byPartner[partner], payment)
	}
	sort.Slice(partners, func(i, j int) bool { return partners[i] < partners[j] })
	var out []Finding
	for _, partner := range partners {
		list := byPartner[partner]
		sort.SliceStable(list, func(i, j int) bool {
			if text(list[i]["date"]) != text(list[j]["date"]) {
				return text(list[i]["date"]) < text(list[j]["date"])
			}
			return relID(list[i]["id"]) < relID(list[j]["id"])
		})
		var cluster []map[string]interface{}
		var reported []string
		for _, payment := range list {
			when, _ := parseDay(payment["date"])
			kept := cluster[:0:0]
			for _, previous := range cluster {
				before, _ := parseDay(previous["date"])
				if float64(before.daysUntil(when)) <= p["window_days"] {
					kept = append(kept, previous)
				}
			}
			cluster = append(kept, payment)
			total := 0.0
			billUses := map[int64]int{}
			var unbilled, names []string
			for _, c := range cluster {
				total += num(c["amount"])
				names = append(names, text(c["name"]))
				bills := relIDs(c["reconciled_bill_ids"])
				if len(bills) == 0 {
					unbilled = append(unbilled, text(c["name"]))
				}
				for _, bill := range bills {
					billUses[bill]++
				}
			}
			var shared []int64
			for bill, uses := range billUses {
				if uses > 1 {
					shared = append(shared, bill)
				}
			}
			sort.Slice(shared, func(i, j int) bool { return shared[i] < shared[j] })
			if float64(len(cluster)) < p["min_payments"] || total < p["limit"] || (len(shared) == 0 && len(unbilled) == 0) {
				continue
			}
			first, _ := parseDay(cluster[0]["date"])
			last, _ := parseDay(cluster[len(cluster)-1]["date"])
			span := first.daysUntil(last) + 1
			parts := make([]string, 0, len(cluster))
			ev := make([]Evidence, 0, len(cluster))
			for _, c := range cluster {
				parts = append(parts, fmt.Sprintf("%s %s ngày %s", text(c["name"]), money(num(c["amount"])), text(c["date"])))
				ev = append(ev, evidence(opPayment, c, ""))
			}
			how := "trả từng phần của cùng một hoá đơn"
			if len(shared) == 0 {
				how = "không gắn với hoá đơn nào: " + strings.Join(unbilled, ", ")
			} else if len(unbilled) > 0 {
				how += "; " + strings.Join(unbilled, ", ") + " không gắn với hoá đơn nào"
			}
			severity := NenXuLy
			if span <= 2 {
				severity = XemNgay
			}
			name := relName(cluster[0]["partner"])
			finding := Finding{
				Title: "Một khoản chi có thể bị tách nhỏ dưới hạn mức duyệt", Severity: severity,
				Subject: Subject{Type: "partner", ID: fmt.Sprint(partner), Name: name},
				Why: fmt.Sprintf("%s: %d khoản chi trong %d ngày (%s), mỗi khoản dưới hạn mức %s, cộng lại %s; %s.",
					name, len(cluster), span, strings.Join(parts, ", "), money(p["limit"]), money(total), how),
				AmountVND: total, Evidence: ev,
				Facts: map[string]interface{}{"payments": names, "total": total, "limit": p["limit"], "days": span,
					"sharedBills": shared, "unbilled": unbilled},
			}
			// A growing cluster replaces the finding it grew from.
			if len(reported) > 0 && containsAll(names, reported) {
				out = out[:len(out)-1]
			}
			out = append(out, finding)
			reported = names
		}
	}
	return out, nil
}

func duplicateBills(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	bills, err := b.invoices(ctx, false, "in_invoice")
	if err != nil {
		return nil, err
	}
	byVendor := map[int64][]map[string]interface{}{}
	var vendors []int64
	for _, bill := range bills {
		vendor := relID(bill["partner"])
		if _, seen := byVendor[vendor]; !seen {
			vendors = append(vendors, vendor)
		}
		byVendor[vendor] = append(byVendor[vendor], bill)
	}
	sort.Slice(vendors, func(i, j int) bool { return vendors[i] < vendors[j] })
	var out []Finding
	for _, vendor := range vendors {
		list := byVendor[vendor]
		sort.SliceStable(list, func(i, j int) bool {
			if text(list[i]["invoice_date"]) != text(list[j]["invoice_date"]) {
				return text(list[i]["invoice_date"]) < text(list[j]["invoice_date"])
			}
			return relID(list[i]["id"]) < relID(list[j]["id"])
		})
		for i, first := range list {
			for _, second := range list[i+1:] {
				d1, _ := parseDay(first["invoice_date"])
				d2, _ := parseDay(second["invoice_date"])
				gap := d1.daysUntil(d2)
				ref1, ref2 := lettersDigits(first["ref"]), lettersDigits(second["ref"])
				sameRef := ref1 != "" && ref1 == ref2
				sameAmount := math.Abs(num(first["total_amount"])-num(second["total_amount"])) < 1
				bothRefs := text(first["ref"]) != "" && text(second["ref"]) != ""
				if !(sameRef || (sameAmount && float64(gap) <= p["window_days"] && !bothRefs)) {
					continue
				}
				paid := func(row map[string]interface{}) bool {
					status := text(row["payment_status"])
					return status == "paid" || status == "in_payment"
				}
				bothPaid := paid(first) && paid(second)
				retyped := sameRef && text(first["ref"]) != text(second["ref"])
				how := fmt.Sprintf("cùng số tiền, cách nhau %d ngày", gap)
				if sameRef {
					how = "cùng số hoá đơn"
				}
				if retyped {
					how += " (số hoá đơn được gõ lại khác cách viết)"
				}
				settled := "Chưa trả cả hai."
				if bothPaid {
					settled = "Cả hai đã được thanh toán."
				}
				title := "Hoá đơn mua có thể bị nhập hai lần"
				severity := NenXuLy
				if bothPaid {
					title += " và đã trả hai lần"
				}
				if sameRef && bothPaid {
					severity = XemNgay
				}
				refOf := func(row map[string]interface{}) string {
					if r := text(row["ref"]); r != "" {
						return r
					}
					return "—"
				}
				vendorName := relName(first["partner"])
				out = append(out, Finding{
					Title: title, Severity: severity,
					Subject: Subject{Type: "vendor", ID: fmt.Sprint(vendor), Name: vendorName},
					Why: fmt.Sprintf("%s: %s (%s, số HĐ %s) và %s (%s, số HĐ %s), %s, %s và %s. %s",
						vendorName, text(first["name"]), text(first["invoice_date"]), refOf(first),
						text(second["name"]), text(second["invoice_date"]), refOf(second), how,
						money(num(first["total_amount"])), money(num(second["total_amount"])), settled),
					AmountVND: num(second["total_amount"]),
					Evidence:  []Evidence{evidence(opInvoice, first, ""), evidence(opInvoice, second, "")},
					Facts: map[string]interface{}{"vendor": vendorName, "refs": []string{text(first["ref"]), text(second["ref"])},
						"daysApart": gap, "bothPaid": bothPaid, "sameReference": sameRef},
				})
			}
		}
	}
	return out, nil
}

func splitPurchases(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := b.read(ctx, opRFQ, []interface{}{[]interface{}{"state", "in", []interface{}{"purchase", "done"}}})
	if err != nil {
		return nil, err
	}
	low := p["threshold"] * p["band"]
	byVendor := map[int64][]map[string]interface{}{}
	var vendors []int64
	for _, order := range rows {
		total := num(order["total_amount"])
		if !b.inPeriod(order["date_order"]) || total < low || total >= p["threshold"] {
			continue
		}
		vendor := relID(order["partner"])
		if _, seen := byVendor[vendor]; !seen {
			vendors = append(vendors, vendor)
		}
		byVendor[vendor] = append(byVendor[vendor], order)
	}
	sort.Slice(vendors, func(i, j int) bool { return vendors[i] < vendors[j] })
	var out []Finding
	for _, vendor := range vendors {
		orders := byVendor[vendor]
		sort.SliceStable(orders, func(i, j int) bool { return text(orders[i]["date_order"]) < text(orders[j]["date_order"]) })
		var cluster []map[string]interface{}
		var last *Finding
		for _, order := range orders {
			when, _ := parseDay(order["date_order"])
			kept := cluster[:0:0]
			for _, previous := range cluster {
				before, _ := parseDay(previous["date_order"])
				if float64(before.daysUntil(when)) <= p["window_days"] {
					kept = append(kept, previous)
				}
			}
			cluster = append(kept, order)
			total := 0.0
			for _, o := range cluster {
				total += num(o["total_amount"])
			}
			if float64(len(cluster)) < p["min_orders"] || total < p["threshold"] {
				continue
			}
			first, _ := parseDay(cluster[0]["date_order"])
			lastDay, _ := parseDay(cluster[len(cluster)-1]["date_order"])
			parts := make([]string, 0, len(cluster))
			names := make([]string, 0, len(cluster))
			ev := make([]Evidence, 0, len(cluster))
			for _, o := range cluster {
				parts = append(parts, text(o["name"])+" "+money(num(o["total_amount"])))
				names = append(names, text(o["name"]))
				ev = append(ev, evidence(opRFQ, o, ""))
			}
			severity := NenXuLy
			if len(cluster) >= 3 {
				severity = XemNgay
			}
			vendorName := relName(order["partner"])
			finding := Finding{
				Title: "Đơn mua có thể bị chia nhỏ dưới ngưỡng duyệt", Severity: severity,
				Subject: Subject{Type: "vendor", ID: fmt.Sprint(vendor), Name: vendorName},
				Why: fmt.Sprintf("%s: %d đơn trong %d ngày (%s), mỗi đơn ngay dưới ngưỡng duyệt %s, cộng lại %s.",
					vendorName, len(cluster), first.daysUntil(lastDay)+1, strings.Join(parts, ", "), money(p["threshold"]), money(total)),
				AmountVND: total, Evidence: ev,
				Facts: map[string]interface{}{"orders": names, "total": total, "threshold": p["threshold"]},
			}
			// A growing cluster replaces the finding it grew from.
			if last != nil && len(out) > 0 && containsAll(names, out[len(out)-1].Facts["orders"].([]string)) {
				out = out[:len(out)-1]
			}
			out = append(out, finding)
			last = &out[len(out)-1]
		}
	}
	return out, nil
}

func containsAll(set, subset []string) bool {
	present := map[string]bool{}
	for _, s := range set {
		present[s] = true
	}
	for _, s := range subset {
		if !present[s] {
			return false
		}
	}
	return true
}
