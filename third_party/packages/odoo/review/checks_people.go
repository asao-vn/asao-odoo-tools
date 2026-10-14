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
		id: "O23", name: "Chi phí kê hai lần", kind: kindErr,
		question: "Có ai kê cùng một khoản chi phí hai lần không?",
		needs:    []string{opExpense},
		formula:  "two claims of one employee with the same amount, <= window_days apart",
		params:   map[string]float64{"window_days": 3},
		run:      duplicateExpenses,
	})
	register(check{
		id: "O24", name: "Chi phí vào ngày nghỉ phép", kind: kindSign,
		question: "Có ai kê chi phí công việc vào ngày đang nghỉ phép không?",
		needs:    []string{opExpense, opLeave},
		formula:  "a claim dated inside one of the claimant's validated leaves",
		run:      expenseOnLeave,
	})
	register(check{
		id: "O25", name: "Chi phí toàn số tròn", kind: kindSign,
		question: "Có ai mà các khoản chi phí số tròn nhiều bất thường so với mọi người không?",
		needs:    []string{opExpense},
		formula:  "an employee's share of claims that are multiples of unit >= max(min_share, times_others x everyone else's share), from min_claims claims",
		params:   map[string]float64{"unit": 100_000, "min_claims": 5, "min_share": 0.25, "times_others": 5},
		run:      roundExpenses,
	})
	register(check{
		id: "O12", name: "Huỷ hàng giá trị cao bất thường", kind: kindSign,
		question: "Hàng huỷ có tăng bất thường, nhất là hàng đắt tiền, không?",
		needs:    []string{opScrap, "catalog.product.list", "catalog.product_variant.list"},
		formula:  "done scraps of products costing >= high_value per unit, by product, with the monthly scrap value and months >= factor x the median month",
		params:   map[string]float64{"high_value": 500_000, "factor": 3},
		run:      unusualScrap,
	})
}

func claims(ctx context.Context, b *books) ([]map[string]interface{}, error) {
	rows, err := b.read(ctx, opExpense, nil)
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, row := range rows {
		state := text(row["state"])
		if b.inPeriod(row["date"]) && state != "draft" && state != "refused" {
			out = append(out, row)
		}
	}
	return out, nil
}

func duplicateExpenses(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := claims(ctx, b)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]map[string]interface{}{}
	var keys []string
	for _, row := range rows {
		key := fmt.Sprintf("%d/%d", relID(row["employee_id"]), int64(math.Round(num(row["total_amount"]))))
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], row)
	}
	sort.Strings(keys)
	var out []Finding
	for _, key := range keys {
		same := byKey[key]
		sort.SliceStable(same, func(i, j int) bool {
			if text(same[i]["date"]) != text(same[j]["date"]) {
				return text(same[i]["date"]) < text(same[j]["date"])
			}
			return relID(same[i]["id"]) < relID(same[j]["id"])
		})
		for i := 0; i+1 < len(same); i++ {
			first, second := same[i], same[i+1]
			d1, _ := parseDay(first["date"])
			d2, _ := parseDay(second["date"])
			gap := d1.daysUntil(d2)
			if float64(gap) > p["window_days"] {
				continue
			}
			severity := NenXuLy
			if gap == 0 {
				severity = XemNgay
			}
			employee := relName(first["employee_id"])
			out = append(out, Finding{
				Title: "Một khoản chi phí có thể bị kê hai lần", Severity: severity,
				Subject: Subject{Type: "employee", ID: fmt.Sprint(relID(first["employee_id"])), Name: employee},
				Why: fmt.Sprintf("%s: %s (%s) và %s (%s), cùng %s.", employee, quoted(text(first["name"])), text(first["date"]),
					quoted(text(second["name"])), text(second["date"]), money(num(first["total_amount"]))),
				AmountVND: num(second["total_amount"]),
				Evidence:  []Evidence{evidence(opExpense, first, ""), evidence(opExpense, second, "")},
				Facts:     map[string]interface{}{"employee": employee, "daysApart": gap},
			})
		}
	}
	return out, nil
}

func expenseOnLeave(ctx context.Context, b *books, _ map[string]float64) ([]Finding, error) {
	rows, err := claims(ctx, b)
	if err != nil {
		return nil, err
	}
	leaves, err := b.read(ctx, opLeave, []interface{}{[]interface{}{"state", "=", "validate"}})
	if err != nil {
		return nil, err
	}
	byEmployee := map[int64][]map[string]interface{}{}
	for _, leave := range leaves {
		byEmployee[relID(leave["employee_id"])] = append(byEmployee[relID(leave["employee_id"])], leave)
	}
	var out []Finding
	for _, claim := range rows {
		when, _ := parseDay(claim["date"])
		for _, leave := range byEmployee[relID(claim["employee_id"])] {
			from, okFrom := parseDay(leave["date_from"])
			to, okTo := parseDay(leave["date_to"])
			if !okFrom || !okTo || when.before(from) || when.after(to) {
				continue
			}
			employee := relName(claim["employee_id"])
			out = append(out, Finding{
				Title: "Chi phí công việc vào ngày đang nghỉ phép", Severity: NenXuLy,
				Subject: Subject{Type: "employee", ID: fmt.Sprint(relID(claim["employee_id"])), Name: employee},
				Why: fmt.Sprintf("%s kê %s %s ngày %s, trong kỳ nghỉ %s %s – %s đã được duyệt.", employee, quoted(text(claim["name"])),
					money(num(claim["total_amount"])), when.format(), relName(leave["holiday_status_id"]), from.format(), to.format()),
				AmountVND: num(claim["total_amount"]),
				Evidence:  []Evidence{evidence(opExpense, claim, ""), evidence(opLeave, leave, "nghỉ "+relName(leave["employee_id"]))},
				Facts:     map[string]interface{}{"employee": employee, "date": when.format()},
			})
		}
	}
	return out, nil
}

func roundExpenses(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := claims(ctx, b)
	if err != nil {
		return nil, err
	}
	unit := p["unit"]
	isRound := func(row map[string]interface{}) bool {
		return math.Mod(math.Round(num(row["total_amount"])), unit) == 0
	}
	byEmployee := map[string][]map[string]interface{}{}
	var employees []string
	for _, row := range rows {
		name := relName(row["employee_id"])
		if _, seen := byEmployee[name]; !seen {
			employees = append(employees, name)
		}
		byEmployee[name] = append(byEmployee[name], row)
	}
	sort.Strings(employees)
	var out []Finding
	for _, employee := range employees {
		own := byEmployee[employee]
		var rounded []map[string]interface{}
		for _, row := range own {
			if isRound(row) {
				rounded = append(rounded, row)
			}
		}
		othersRound, others := 0, 0
		for _, name := range employees {
			if name == employee {
				continue
			}
			for _, row := range byEmployee[name] {
				others++
				if isRound(row) {
					othersRound++
				}
			}
		}
		othersRate := float64(othersRound) / math.Max(float64(others), 1)
		rate := float64(len(rounded)) / float64(len(own))
		// Compared with everyone else: in a company where every claim is a flat allowance, round is normal.
		if float64(len(rounded)) < p["min_claims"] || rate < math.Max(p["min_share"], p["times_others"]*othersRate) {
			continue
		}
		total := 0.0
		ev := make([]Evidence, 0, len(rounded))
		for _, row := range rounded {
			total += num(row["total_amount"])
			ev = append(ev, evidence(opExpense, row, ""))
		}
		severity := TheoDoi
		if rate >= 0.5 {
			severity = NenXuLy
		}
		out = append(out, Finding{
			Title: "Chi phí số tròn nhiều bất thường", Severity: severity,
			Subject: Subject{Type: "employee", Name: employee},
			Why: fmt.Sprintf("%s: %d/%d khoản chi (%s) là bội số của %s, những người khác chỉ %s; tổng các khoản tròn %s. Hoá đơn thật hiếm khi tròn; khoản tròn thường là ước lượng không có chứng từ.",
				employee, len(rounded), len(own), pct(rate, 0), money(unit), pct(othersRate, 0), money(total)),
			AmountVND: total, Evidence: ev,
			Facts: map[string]interface{}{"employee": employee, "rounded": len(rounded), "claims": len(own), "rate": rate, "othersRate": othersRate},
		})
	}
	return out, nil
}

func unusualScrap(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := b.read(ctx, opScrap, []interface{}{[]interface{}{"state", "=", "done"}})
	if err != nil {
		return nil, err
	}
	type valued struct {
		row   map[string]interface{}
		value float64
	}
	monthly := map[string]float64{}
	byProduct := map[string][]valued{}
	var products []string
	var count int
	var total float64
	var ev []Evidence
	for _, row := range rows {
		when, ok := parseDay(row["date_done"])
		if !ok || !b.inPeriod(row["date_done"]) {
			continue
		}
		cost, _, err := b.cost(ctx, relID(row["product_id"]))
		if err != nil {
			return nil, err
		}
		value := cost * num(row["scrap_qty"])
		monthly[when.month()] += value
		if cost < p["high_value"] {
			continue
		}
		product := relName(row["product_id"])
		if _, seen := byProduct[product]; !seen {
			products = append(products, product)
		}
		byProduct[product] = append(byProduct[product], valued{row, value})
		count++
		total += value
		ev = append(ev, evidence(opScrap, row, ""))
	}
	if count == 0 {
		return nil, nil
	}
	months := make([]string, 0, len(monthly))
	values := make([]float64, 0, len(monthly))
	for month, value := range monthly {
		months = append(months, month)
		values = append(values, value)
	}
	sort.Strings(months)
	sort.Float64s(values)
	typical := values[len(values)/2]
	var spikes, perMonth, perProduct []string
	for _, month := range months {
		perMonth = append(perMonth, month+" "+money(monthly[month]))
		if typical > 0 && monthly[month] >= p["factor"]*typical {
			spikes = append(spikes, month)
		}
	}
	sort.Strings(products)
	for _, product := range products {
		units, value := 0.0, 0.0
		for _, v := range byProduct[product] {
			units += num(v.row["scrap_qty"])
			value += v.value
		}
		perProduct = append(perProduct, fmt.Sprintf("%s: %d lần, %s cái, %s", product, len(byProduct[product]), trimFloat(units), money(value)))
	}
	why := strings.Join(perProduct, "; ") + ". Tổng giá trị hàng huỷ theo tháng: " + strings.Join(perMonth, ", ")
	if len(spikes) > 0 {
		why += fmt.Sprintf("; tháng %s gấp %s lần mức thường.", strings.Join(spikes, ", "), trimFloat(p["factor"]))
	} else {
		why += "."
	}
	why += " Phiếu huỷ chưa cho đọc người lập: cần hỏi kho ai đã lập các phiếu này."
	severity := NenXuLy
	if count >= 4 {
		severity = XemNgay
	}
	return []Finding{{
		Title: fmt.Sprintf("Huỷ %d lần hàng giá trị cao, %s", count, money(total)), Severity: severity,
		Subject: Subject{Type: "stock"}, Why: why, AmountVND: total, Evidence: ev,
		Facts: map[string]interface{}{"valuable": count, "total": total, "monthly": monthly, "spikeMonths": spikes},
	}}, nil
}
