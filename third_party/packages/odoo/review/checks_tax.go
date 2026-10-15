package review

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// The Vietnamese rule both checks hold to: Nghị định 181/2025/NĐ-CP (Công báo số 899+900, 16/7/2025), Điều 26.
// Input VAT on goods and services bought for 5 million đồng or more, VAT included, is deductible only with a
// non-cash payment document (cash paid into the seller's account does not count); clause 3 applies the same to
// several purchases from one seller on one day that are each under 5 million and together reach it. Nghị định
// 144/2026/NĐ-CP (Công báo số 278, 18/5/2026) amends only point g of clause 2 (deferred and instalment
// payments), not these.
const vatRule = "Nghị định 181/2025/NĐ-CP, Điều 26"

func init() {
	register(check{
		id: "O28", name: "Hoá đơn mua từ 5 triệu đồng trả bằng tiền mặt", kind: kindErr,
		question: "Có hoá đơn mua từ 5 triệu đồng trở lên nào trả bằng tiền mặt, nên thuế GTGT đầu vào không được khấu trừ không?",
		needs:    []string{opInvoice, opPayment, opJournal},
		formula:  "posted vendor bills in VND with VAT, total (VAT included) >= threshold, settled by payments of a cash journal; the VAT of the share paid in cash is not deductible (" + vatRule + ")",
		params:   map[string]float64{"threshold": 5_000_000},
		run:      cashPaidBills,
	})
	register(check{
		id: "O29", name: "Mua nhiều lần trong ngày, tổng từ 5 triệu đồng, trả tiền mặt", kind: kindErr,
		question: "Có ngày nào mua của cùng một người bán nhiều hoá đơn nhỏ, cộng lại từ 5 triệu đồng, mà trả bằng tiền mặt không?",
		needs:    []string{opInvoice, opPayment, opJournal},
		formula:  "posted vendor bills in VND with VAT of one vendor on one invoice date, each below threshold, together >= threshold, some settled by a cash journal (" + vatRule + " khoản 3)",
		params:   map[string]float64{"threshold": 5_000_000},
		run:      cashPaidSameDay,
	})
}

// taxedVNDBills are the posted vendor bills of the period in VND that carry VAT.
func taxedVNDBills(ctx context.Context, b *books) ([]map[string]interface{}, error) {
	bills, err := b.invoices(ctx, false, "in_invoice")
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, bill := range bills {
		if relName(bill["currency_id"]) != "VND" || num(bill["total_amount"])-num(bill["amount_untaxed"]) < 0.5 {
			continue
		}
		out = append(out, bill)
	}
	return out, nil
}

// cashSettled answers, for each bill, the cash paid towards it and the cash payments: a payment that settles
// several bills is shared among them in proportion to their totals.
func cashSettled(ctx context.Context, b *books, bills []map[string]interface{}) (map[int64]float64, map[int64][]map[string]interface{}, error) {
	payments, err := b.payments(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	var outbound []map[string]interface{}
	for _, payment := range payments {
		if text(payment["payment_type"]) == "outbound" {
			outbound = append(outbound, payment)
		}
	}
	if err := requireField(opPayment, "reconciled_bill_ids", outbound); err != nil {
		return nil, nil, err
	}
	types, err := b.journalTypes(ctx)
	if err != nil {
		return nil, nil, err
	}
	totals := map[int64]float64{}
	for _, bill := range bills {
		totals[relID(bill["id"])] = num(bill["total_amount"])
	}
	paid := map[int64]float64{}
	by := map[int64][]map[string]interface{}{}
	for _, payment := range outbound {
		if types[relID(payment["journal_id"])] != "cash" {
			continue
		}
		settled := relIDs(payment["reconciled_bill_ids"])
		sum := 0.0
		for _, id := range settled {
			sum += totals[id]
		}
		for _, id := range settled {
			if totals[id] == 0 || sum == 0 {
				continue
			}
			paid[id] += num(payment["amount"]) * totals[id] / sum
			by[id] = append(by[id], payment)
		}
	}
	return paid, by, nil
}

func vatAtRisk(bill map[string]interface{}, cash float64) (float64, float64) {
	total := num(bill["total_amount"])
	share := cash / total
	if share > 1 {
		share = 1
	}
	return share, (total - num(bill["amount_untaxed"])) * share
}

func cashPaidBills(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	bills, err := taxedVNDBills(ctx, b)
	if err != nil {
		return nil, err
	}
	paid, by, err := cashSettled(ctx, b, bills)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, bill := range bills {
		id := relID(bill["id"])
		if num(bill["total_amount"]) < p["threshold"] || paid[id] < 0.5 {
			continue
		}
		share, vat := vatAtRisk(bill, paid[id])
		ev := []Evidence{evidence(opInvoice, bill, "")}
		var names []string
		for _, payment := range by[id] {
			ev = append(ev, evidence(opPayment, payment, ""))
			names = append(names, text(payment["name"]))
		}
		vendor := relName(bill["partner"])
		out = append(out, Finding{
			Title: "Hoá đơn mua từ 5 triệu đồng trả bằng tiền mặt", Severity: NenXuLy,
			Subject: Subject{Type: "bill", ID: fmt.Sprint(id), Name: text(bill["name"])},
			Why: fmt.Sprintf("%s của %s ngày %s, %s (gồm thuế GTGT), được trả bằng tiền mặt (%s, %s số tiền): phần thuế GTGT đầu vào %s không được khấu trừ theo %s.",
				text(bill["name"]), vendor, text(bill["invoice_date"]), money(num(bill["total_amount"])), strings.Join(names, ", "),
				pct(share, 0), money(vat), vatRule),
			AmountVND: vat, Evidence: ev,
			Facts: map[string]interface{}{"vendor": vendor, "total": num(bill["total_amount"]), "paidInCash": paid[id],
				"cashShare": share, "vatNotDeductible": vat, "rule": vatRule},
		})
	}
	return out, nil
}

func cashPaidSameDay(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	bills, err := taxedVNDBills(ctx, b)
	if err != nil {
		return nil, err
	}
	paid, by, err := cashSettled(ctx, b, bills)
	if err != nil {
		return nil, err
	}
	groups := map[string][]map[string]interface{}{}
	var keys []string
	for _, bill := range bills {
		if num(bill["total_amount"]) >= p["threshold"] {
			continue
		}
		key := fmt.Sprintf("%d/%s", relID(bill["partner"]), text(bill["invoice_date"]))
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], bill)
	}
	sort.Strings(keys)
	var out []Finding
	for _, key := range keys {
		group := groups[key]
		total, vat, cash := 0.0, 0.0, 0.0
		for _, bill := range group {
			total += num(bill["total_amount"])
		}
		if len(group) < 2 || total < p["threshold"] {
			continue
		}
		var ev []Evidence
		var parts, payments []string
		seen := map[int64]bool{}
		for _, bill := range group {
			id := relID(bill["id"])
			ev = append(ev, evidence(opInvoice, bill, ""))
			parts = append(parts, text(bill["name"])+" "+money(num(bill["total_amount"])))
			if paid[id] < 0.5 {
				continue
			}
			_, part := vatAtRisk(bill, paid[id])
			vat += part
			cash += paid[id]
			for _, payment := range by[id] {
				if !seen[relID(payment["id"])] {
					seen[relID(payment["id"])] = true
					ev = append(ev, evidence(opPayment, payment, ""))
					payments = append(payments, text(payment["name"]))
				}
			}
		}
		if cash < 0.5 {
			continue
		}
		vendor := relName(group[0]["partner"])
		out = append(out, Finding{
			Title: "Mua nhiều lần trong ngày, tổng từ 5 triệu đồng, trả tiền mặt", Severity: NenXuLy,
			Subject: Subject{Type: "vendor", ID: fmt.Sprint(relID(group[0]["partner"])), Name: vendor},
			Why: fmt.Sprintf("%s: %d hoá đơn ngày %s (%s), mỗi hoá đơn dưới %s nhưng cộng lại %s, trả bằng tiền mặt %s (%s): phần thuế GTGT đầu vào %s không được khấu trừ theo %s khoản 3.",
				vendor, len(group), text(group[0]["invoice_date"]), strings.Join(parts, ", "), money(p["threshold"]), money(total),
				money(cash), strings.Join(payments, ", "), money(vat), vatRule),
			AmountVND: vat, Evidence: ev,
			Facts: map[string]interface{}{"vendor": vendor, "bills": len(group), "total": total, "paidInCash": cash,
				"vatNotDeductible": vat, "rule": vatRule},
		})
	}
	return out, nil
}
