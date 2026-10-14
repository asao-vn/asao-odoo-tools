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
