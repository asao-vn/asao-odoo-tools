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
		formula:  "same vendor and the same reference compared on letters and digits; or same vendor and total within window_days when a bill has no reference. An amount the vendor bills recurring_min times or more is its fixed fee: only two such bills in a row closer than cadence_share of its usual gap are a pair, and the vendor gets one THEO_DOI finding asking for invoice numbers",
		params:   map[string]float64{"window_days": 14, "recurring_min": 3, "cadence_share": 0.25},
		run:      duplicateBills,
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
		// Without a reference only the amount can match two bills, and a vendor that bills one amount again
		// and again (a unit price, a weekly fee) makes pairs of it all year: 232 alarms for one supplier of
		// a city council's real payments (2026-10-20). Such an amount is the vendor's fixed fee; a copy of it
		// shows as two bills far closer together than the vendor's usual gap.
		byAmount := map[int64][]map[string]interface{}{}
		for _, bill := range list {
			key := int64(math.Round(num(bill["total_amount"])))
			byAmount[key] = append(byAmount[key], bill)
		}
		fixed := map[int64]bool{}
		nextOf := map[int64]int64{}
		cadence := map[int64]float64{}
		var unreferenced []map[string]interface{}
		for key, bills := range byAmount {
			if float64(len(bills)) < p["recurring_min"] {
				continue
			}
			fixed[key] = true
			var gaps []float64
			ambiguous := false
			for k := 0; k+1 < len(bills); k++ {
				a, _ := parseDay(bills[k]["invoice_date"])
				z, _ := parseDay(bills[k+1]["invoice_date"])
				gaps = append(gaps, float64(a.daysUntil(z)))
				nextOf[relID(bills[k]["id"])] = relID(bills[k+1]["id"])
				noRef := text(bills[k]["ref"]) == "" || text(bills[k+1]["ref"]) == ""
				ambiguous = ambiguous || (noRef && float64(a.daysUntil(z)) <= p["window_days"])
			}
			sort.Float64s(gaps)
			cadence[key] = gaps[len(gaps)/2]
			if ambiguous {
				for _, bill := range bills {
					if text(bill["ref"]) == "" {
						unreferenced = append(unreferenced, bill)
					}
				}
			}
		}
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
				if key := int64(math.Round(num(first["total_amount"]))); !sameRef && fixed[key] &&
					(nextOf[relID(first["id"])] != relID(second["id"]) || float64(gap) >= p["cadence_share"]*cadence[key]) {
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
		if len(unreferenced) > 0 {
			sort.SliceStable(unreferenced, func(i, j int) bool { return relID(unreferenced[i]["id"]) < relID(unreferenced[j]["id"]) })
			vendorName := relName(unreferenced[0]["partner"])
			ev := make([]Evidence, 0, len(unreferenced))
			for _, bill := range unreferenced {
				ev = append(ev, evidence(opInvoice, bill, ""))
			}
			out = append(out, Finding{
				Title: "Hoá đơn mua không có số hoá đơn, cùng số tiền lặp lại: không kiểm được trùng", Severity: TheoDoi,
				Subject: Subject{Type: "vendor", ID: fmt.Sprint(vendor), Name: vendorName},
				Why: fmt.Sprintf("%s: %d hoá đơn không có số hoá đơn, với số tiền lặp lại từ %d lần trở lên, có lần cách nhau không quá %d ngày. Chỉ dựa vào số tiền thì không phân biệt được hoá đơn nhập hai lần với phí cố định; chỉ báo khi hai lần sát nhau bất thường so với nhịp thường lệ. Nhập số hoá đơn của nhà cung cấp để kiểm được trùng.",
					vendorName, len(unreferenced), int(p["recurring_min"]), int(p["window_days"])),
				Evidence: ev,
				Facts:    map[string]interface{}{"vendor": vendorName, "bills": len(unreferenced)},
			})
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
