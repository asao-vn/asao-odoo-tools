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
		id: "O13", name: "Đối tác trùng", kind: kindErr,
		question: "Có khách hàng hay nhà cung cấp nào được tạo hai lần không?",
		needs:    []string{opContact},
		formula:  "customers or vendors sharing a tax code, a phone (9+ digits) or an e-mail, excluding contacts of one company",
		run:      duplicatePartners,
	})
	register(check{
		id: "O17", name: "Tài khoản nhà cung cấp trùng tài khoản nhân viên", kind: kindSign,
		question: "Có nhà cung cấp nào nhận tiền vào tài khoản ngân hàng của một nhân viên không?",
		needs:    []string{opBank, opEmployee, opContact, opPayment},
		formula:  "a partner's bank account number equal to an account held by an employee's own contact; amount = everything paid out to that partner",
		run:      vendorBankIsEmployee,
	})
	register(check{
		id: "O18", name: "Nhà cung cấp mới, thiếu thông tin, được trả nhanh", kind: kindSign,
		question: "Có nhà cung cấp mới nào thiếu mã số thuế hay địa chỉ mà đã nhận nhiều tiền, và được trả nhanh bất thường không?",
		needs:    []string{opContact, opInvoice, opPayment},
		formula:  "a vendor whose first bill is in the period, missing a tax code or an address, paid >= min_paid; XEM_NGAY when every bill was paid within fast_days",
		params:   map[string]float64{"min_paid": 10_000_000, "fast_days": 2},
		run:      newVendorPaidFast,
	})
}

func duplicatePartners(ctx context.Context, b *books, _ map[string]float64) ([]Finding, error) {
	all, err := b.contacts(ctx)
	if err != nil {
		return nil, err
	}
	var contacts []map[string]interface{}
	for _, c := range all {
		if num(c["customer_priority"]) > 0 || num(c["vendor_priority"]) > 0 {
			contacts = append(contacts, c)
		}
	}
	var out []Finding
	for _, field := range []struct{ key, label, severity string }{
		{"vat", "mã số thuế", NenXuLy}, {"phone", "số điện thoại", TheoDoi}, {"email", "email", TheoDoi},
	} {
		groups := map[string][]map[string]interface{}{}
		var keys []string
		for _, c := range contacts {
			value := lettersDigits(c[field.key])
			if field.key == "phone" {
				value = digits(c[field.key])
				if len(value) < 9 {
					continue
				}
			}
			if value == "" {
				continue
			}
			if _, seen := groups[value]; !seen {
				keys = append(keys, value)
			}
			groups[value] = append(groups[value], c)
		}
		sort.Strings(keys)
		for _, value := range keys {
			same := groups[value]
			roots := map[int64]bool{}
			for _, c := range same {
				root := relID(c["parent"])
				if root == 0 {
					root = relID(c["id"])
				}
				roots[root] = true
			}
			// A company and its own contact people share a phone or an e-mail; that is no duplicate.
			if len(same) < 2 || len(roots) < 2 {
				continue
			}
			names := make([]string, 0, len(same))
			ev := make([]Evidence, 0, len(same))
			for _, c := range same {
				names = append(names, quoted(text(c["name"])))
				ev = append(ev, evidence(opContact, c, ""))
			}
			out = append(out, Finding{
				Title: "Hai hồ sơ đối tác cùng " + field.label, Severity: field.severity,
				Subject: Subject{Type: "partner", ID: fmt.Sprint(relID(same[0]["id"])), Name: text(same[0]["name"])},
				Why: fmt.Sprintf("%s có cùng %s %s. Giao dịch, công nợ và hạn mức bị tách ra hai nơi; nếu là nhà cung cấp, một hồ sơ có thể dùng để nhận tiền.",
					strings.Join(names, " và "), field.label, text(same[0][field.key])),
				Evidence: ev, Facts: map[string]interface{}{"field": field.key, "value": text(same[0][field.key])},
			})
		}
	}
	return out, nil
}

func vendorBankIsEmployee(ctx context.Context, b *books, _ map[string]float64) ([]Finding, error) {
	employees, err := b.read(ctx, opEmployee, nil)
	if err != nil {
		return nil, err
	}
	accounts, err := b.read(ctx, opBank, nil)
	if err != nil {
		return nil, err
	}
	contacts, err := b.contacts(ctx)
	if err != nil {
		return nil, err
	}
	payments, err := b.payments(ctx, true)
	if err != nil {
		return nil, err
	}
	employeeOf := map[int64]map[string]interface{}{}
	for _, e := range employees {
		if contact := relID(e["work_contact_id"]); contact != 0 {
			employeeOf[contact] = e
		}
	}
	type held struct {
		employee map[string]interface{}
		holder   int64
	}
	byNumber := map[string]held{}
	for _, account := range accounts {
		holder := relID(account["partner_id"])
		if e, isEmployee := employeeOf[holder]; isEmployee {
			byNumber[lettersDigits(account["acc_number"])] = held{e, holder}
		}
	}
	contactByID := map[int64]map[string]interface{}{}
	for _, c := range contacts {
		contactByID[relID(c["id"])] = c
	}
	paid := map[int64]float64{}
	for _, payment := range payments {
		if text(payment["payment_type"]) == "outbound" {
			paid[relID(payment["partner"])] += num(payment["amount"])
		}
	}
	var out []Finding
	for _, account := range accounts {
		number := lettersDigits(account["acc_number"])
		match, isEmployees := byNumber[number]
		holder := relID(account["partner_id"])
		if number == "" || !isEmployees || holder == match.holder {
			continue
		}
		name := relName(account["partner_id"])
		if c, known := contactByID[holder]; known {
			name = text(c["name"])
		}
		employee := text(match.employee["name"])
		out = append(out, Finding{
			Title: "Nhà cung cấp dùng tài khoản ngân hàng của nhân viên", Severity: XemNgay,
			Subject: Subject{Type: "partner", ID: fmt.Sprint(holder), Name: name},
			Why: fmt.Sprintf("Tài khoản %s của %s cũng là tài khoản của nhân viên %s. Đã chi cho %s tổng %s.",
				text(account["acc_number"]), quoted(name), employee, quoted(name), money(paid[holder])),
			AmountVND: paid[holder],
			Evidence: []Evidence{{Operation: opContact, ID: holder, Name: name}, evidence(opEmployee, match.employee, ""),
				{Operation: opBank, ID: relID(account["id"]), Name: text(account["acc_number"])}},
			Facts: map[string]interface{}{"accountNumber": text(account["acc_number"]), "employee": employee, "paidTotal": paid[holder]},
		})
	}
	return out, nil
}

func newVendorPaidFast(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	contacts, err := b.contacts(ctx)
	if err != nil {
		return nil, err
	}
	bills, err := b.invoices(ctx, true, "in_invoice")
	if err != nil {
		return nil, err
	}
	payments, err := b.payments(ctx, true)
	if err != nil {
		return nil, err
	}
	contactByID := map[int64]map[string]interface{}{}
	for _, c := range contacts {
		contactByID[relID(c["id"])] = c
	}
	first := map[int64]string{}
	billsOf := map[int64][]map[string]interface{}{}
	var vendors []int64
	for _, bill := range bills {
		vendor := relID(bill["partner"])
		when := text(bill["invoice_date"])
		if current, seen := first[vendor]; !seen {
			vendors = append(vendors, vendor)
			first[vendor] = when
		} else if when < current {
			first[vendor] = when
		}
		billsOf[vendor] = append(billsOf[vendor], bill)
	}
	outbound := map[int64][]map[string]interface{}{}
	for _, payment := range payments {
		if text(payment["payment_type"]) == "outbound" {
			outbound[relID(payment["partner"])] = append(outbound[relID(payment["partner"])], payment)
		}
	}
	sort.Slice(vendors, func(i, j int) bool { return vendors[i] < vendors[j] })
	var out []Finding
	for _, vendor := range vendors {
		contact, known := contactByID[vendor]
		firstDay, _ := parseDay(first[vendor])
		if !known || firstDay.before(b.from) {
			continue
		}
		var missing []string
		if text(contact["vat"]) == "" {
			missing = append(missing, "mã số thuế")
		}
		if text(contact["street"]) == "" {
			missing = append(missing, "địa chỉ")
		}
		total := 0.0
		for _, payment := range outbound[vendor] {
			total += num(payment["amount"])
		}
		if len(missing) == 0 || total < p["min_paid"] {
			continue
		}
		// Each bill's quickest matching payment; a bill not paid yet says nothing about speed.
		slowest := -1
		for _, bill := range billsOf[vendor] {
			billDay, _ := parseDay(bill["invoice_date"])
			best := -1
			for _, payment := range outbound[vendor] {
				paidDay, _ := parseDay(payment["date"])
				if paidDay.before(billDay) || math.Abs(num(payment["amount"])-num(bill["total_amount"])) >= 1 {
					continue
				}
				if gap := billDay.daysUntil(paidDay); best < 0 || gap < best {
					best = gap
				}
			}
			if best > slowest {
				slowest = best
			}
		}
		fast := slowest >= 0 && float64(slowest) <= p["fast_days"]
		severity, tail := NenXuLy, "."
		if fast {
			severity = XemNgay
			tail = fmt.Sprintf(", mỗi hoá đơn được trả trong %d ngày (các nhà cung cấp khác thường 1–4 tuần).", slowest)
		}
		name := text(contact["name"])
		ev := []Evidence{{Operation: opContact, ID: vendor, Name: name}}
		for _, bill := range billsOf[vendor] {
			ev = append(ev, evidence(opInvoice, bill, ""))
		}
		out = append(out, Finding{
			Title: "Nhà cung cấp mới thiếu thông tin đã nhận nhiều tiền", Severity: severity,
			Subject: Subject{Type: "vendor", ID: fmt.Sprint(vendor), Name: name},
			Why: fmt.Sprintf("%s: hoá đơn đầu tiên ngày %s, thiếu %s; %d hoá đơn, đã nhận %s%s",
				quoted(name), first[vendor], strings.Join(missing, ", "), len(billsOf[vendor]), money(total), tail),
			AmountVND: total, Evidence: ev,
			Facts: map[string]interface{}{"firstBill": first[vendor], "missing": missing, "bills": len(billsOf[vendor]), "paid": total, "maxDaysToPay": slowest},
		})
	}
	return out, nil
}
