package review

import (
	"fmt"
	"sort"
	"strings"
)

var limits = []string{
	"Một phát hiện là **dấu hiệu cần xem lại**, không phải kết luận ai sai. Mỗi phát hiện kèm chứng từ để người có trách nhiệm mở ra kiểm tra.",
	"Rà soát chỉ đọc sổ sách trong Odoo. Việc không được ghi vào Odoo (tiền mặt ngoài sổ, hàng không nhập kho) thì không thấy được.",
	"Mỗi phép kiểm tra chỉ tìm đúng kiểu sai sót nó mô tả. Không có phát hiện ở một phép kiểm tra không chứng minh là không có sai sót kiểu khác.",
}

func pctOrDash(value *float64) string {
	if value == nil {
		return "—"
	}
	return pct(*value, 1)
}

// Markdown is the report a director reads: what to look at first, what the period looked like,
// every finding with the documents behind it, and what the review did not cover.
func Markdown(report *Report, company string) string {
	kindOf := map[string]string{}
	nameOf := map[string]string{}
	for _, c := range report.Checks {
		kindOf[c.ID], nameOf[c.ID] = c.Kind, c.Name
	}
	var issues, state []Finding
	for _, f := range report.Findings {
		if kindOf[f.Check] == kindStat {
			state = append(state, f)
		} else {
			issues = append(issues, f)
		}
	}
	counts := map[string]int{}
	for _, f := range issues {
		counts[f.Severity]++
	}
	title := "# Báo cáo rà soát sổ sách"
	if company != "" {
		title += ": " + company
	}
	out := []string{title, "",
		fmt.Sprintf("Kỳ %s – %s, số liệu tính đến %s. Chỉ đọc; mọi con số lấy từ Odoo qua dòng lệnh ASAO.", report.From, report.To, report.AsOf),
		"", "## 1. Tóm tắt", "",
		fmt.Sprintf("- **%d** việc cần xem ngay, **%d** nên xử lý, **%d** theo dõi. Số tiền của các việc không cộng lại với nhau: nhiều phát hiện có thể nói về cùng một khoản tiền.",
			counts[XemNgay], counts[NenXuLy], counts[TheoDoi])}
	if f := report.Figures; f != nil {
		out = append(out, fmt.Sprintf("- Doanh thu kỳ %s, lãi gộp %s (%s); khách đang nợ %s, trong đó quá hạn %s.",
			money(f.Revenue), money(f.GrossMargin), pctOrDash(rate(f.GrossMargin, f.Revenue)), money(f.Receivable), money(f.OverdueReceivable)))
	}
	out = append(out, "", "**Cần xem trước:**", "")
	shown := 0
	for _, f := range issues {
		if f.Severity == XemNgay && shown < 7 {
			shown++
			out = append(out, fmt.Sprintf("%d. %s (%s) — `%s`", shown, f.Title, money(f.AmountVND), f.Check))
		}
	}
	if counts[XemNgay] == 0 {
		out = append(out, "Không có việc cần xem ngay.")
	}

	if f := report.Figures; f != nil {
		out = append(out, "", "## 2. Tình hình kỳ", "",
			"| Tháng | Doanh thu | Lãi gộp | Biên | Mua vào | Chi phí NV | Thu tiền | Chi tiền |", "|---|---:|---:|---:|---:|---:|---:|---:|")
		for _, m := range f.Months {
			out = append(out, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |", m.Month, money(m.Revenue), money(m.GrossMargin),
				pctOrDash(m.MarginRate), money(m.Purchases), money(m.Expenses), money(m.CashIn), money(m.CashOut)))
		}
		out = append(out, "")
		for _, s := range f.Signals {
			out = append(out, "- "+s)
		}
		var sellers []string
		for _, s := range f.Sellers {
			sellers = append(sellers, fmt.Sprintf("%s %s, biên %s", s.Name, money(s.Revenue), pctOrDash(s.MarginRate)))
		}
		out = append(out, "", "**Theo người bán:** "+strings.Join(sellers, "; "))
		var weakest []Share
		for _, p := range f.Products {
			if p.MarginRate != nil {
				weakest = append(weakest, p)
			}
		}
		sort.SliceStable(weakest, func(i, j int) bool { return *weakest[i].MarginRate < *weakest[j].MarginRate })
		if len(weakest) > 3 {
			weakest = weakest[:3]
		}
		if len(weakest) > 0 {
			var parts []string
			for _, p := range weakest {
				parts = append(parts, fmt.Sprintf("%s %s", p.Name, pctOrDash(p.MarginRate)))
			}
			out = append(out, "", "**Sản phẩm biên thấp nhất:** "+strings.Join(parts, "; "))
		}
		var top []string
		for _, c := range f.TopCustomers {
			top = append(top, fmt.Sprintf("%s %s", c.Name, pct(c.Share, 0)))
		}
		out = append(out, "", "**Khách lớn nhất:** "+strings.Join(top, "; "))
	} else if report.FiguresNotRun != "" {
		out = append(out, "", "## 2. Tình hình kỳ", "", "Không tính được: "+report.FiguresNotRun)
	}

	out = append(out, "", "## 3. Sai sót và dấu hiệu gian lận", "")
	if len(issues) == 0 {
		out = append(out, "Không có phát hiện nào ở các phép kiểm tra đã chạy.")
	}
	for _, f := range issues {
		params := formatParams(f.Params)
		if params == "" {
			params = "không"
		}
		var docs []string
		for i, e := range f.Evidence {
			if i == 12 {
				docs = append(docs, fmt.Sprintf("và %d chứng từ khác", len(f.Evidence)-12))
				break
			}
			docs = append(docs, fmt.Sprintf("%s (`%s` #%d)", e.Name, e.Operation, e.ID))
		}
		out = append(out, fmt.Sprintf("### [%s] %s", SeverityLabel[f.Severity], f.Title), "",
			fmt.Sprintf("`%s` %s (%s). %s", f.Check, nameOf[f.Check], kindOf[f.Check], f.Why), "",
			fmt.Sprintf("Công thức: %s; tham số %s.", f.Formula, params), "",
			"Chứng từ: "+strings.Join(docs, ", "), "")
	}

	if len(state) > 0 {
		sort.SliceStable(state, func(i, j int) bool { return state[i].AmountVND > state[j].AmountVND })
		out = append(out, "## 4. Công nợ quá hạn", "", "| Khách | Quá hạn | Lâu nhất | Mức |", "|---|---:|---:|---|")
		for _, f := range state {
			out = append(out, fmt.Sprintf("| %v | %s | %v ngày | %s |", f.Facts["customer"], money(f.AmountVND), f.Facts["oldestDays"], SeverityLabel[f.Severity]))
		}
		out = append(out, "")
	}

	var ran, notRun []string
	for _, c := range report.Checks {
		if c.Ran {
			ran = append(ran, fmt.Sprintf("`%s` %s", c.ID, c.Name))
		} else {
			notRun = append(notRun, fmt.Sprintf("- `%s` %s: %s", c.ID, c.Name, c.NotRun))
		}
	}
	out = append(out, "## 5. Phạm vi và giới hạn", "", "Đã chạy: "+strings.Join(ran, ", ")+".")
	if len(notRun) > 0 {
		out = append(out, "", "**Không chạy được** (không có nghĩa là không có vấn đề):", "")
		out = append(out, notRun...)
	}
	out = append(out, "")
	for _, line := range limits {
		out = append(out, "- "+line)
	}
	return strings.Join(out, "\n") + "\n"
}
