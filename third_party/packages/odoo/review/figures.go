package review

import (
	"context"
	"fmt"
	"sort"
)

// Month is one month of the period.
type Month struct {
	Month       string   `json:"month"`
	Revenue     float64  `json:"revenue"`
	GrossMargin float64  `json:"grossMargin"`
	MarginRate  *float64 `json:"marginRate"`
	Purchases   float64  `json:"purchases"`
	Expenses    float64  `json:"expenses"`
	CashIn      float64  `json:"cashIn"`
	CashOut     float64  `json:"cashOut"`
}

// Share is a customer's, a seller's or a product's part of the period.
type Share struct {
	Name       string   `json:"name"`
	Revenue    float64  `json:"revenue"`
	Share      float64  `json:"share,omitempty"`
	MarginRate *float64 `json:"marginRate,omitempty"`
}

// Figures are what the period looked like, month by month. Every figure is a sum of records the
// checks also read.
type Figures struct {
	Months            []Month  `json:"months"`
	Revenue           float64  `json:"revenue"`
	GrossMargin       float64  `json:"grossMargin"`
	Receivable        float64  `json:"receivable"`
	OverdueReceivable float64  `json:"overdueReceivable"`
	Payable           float64  `json:"payable"`
	TopCustomers      []Share  `json:"topCustomers"`
	Sellers           []Share  `json:"sellers"`
	Products          []Share  `json:"products"`
	Signals           []string `json:"signals"`
}

func rate(margin, revenue float64) *float64 {
	if revenue == 0 {
		return nil
	}
	r := margin / revenue
	return &r
}

func periodFigures(ctx context.Context, b *books) (*Figures, error) {
	type sums struct{ revenue, cogs, purchases, expenses, cashIn, cashOut float64 }
	months := map[string]*sums{}
	at := func(value interface{}) *sums {
		d, _ := parseDay(value)
		key := d.month()
		if months[key] == nil {
			months[key] = &sums{}
		}
		return months[key]
	}
	invoices, err := b.invoices(ctx, false, "out_invoice", "out_refund")
	if err != nil {
		return nil, err
	}
	sign := func(row map[string]interface{}, refund string) float64 {
		if text(row["document_type"]) == refund {
			return -1
		}
		return 1
	}
	byID := map[int64]map[string]interface{}{}
	customers, sellerRevenue, sellerCost := map[string]float64{}, map[string]float64{}, map[string]float64{}
	for _, invoice := range invoices {
		byID[relID(invoice["id"])] = invoice
		amount := sign(invoice, "out_refund") * num(invoice["amount_untaxed"])
		at(invoice["invoice_date"]).revenue += amount
		customers[relName(invoice["partner"])] += amount
		sellerRevenue[sellerOf(invoice)] += amount
	}
	lines, err := b.invoiceLines(ctx)
	if err != nil {
		return nil, err
	}
	productRevenue, productCost := map[string]float64{}, map[string]float64{}
	for _, line := range lines {
		invoice, ok := byID[relID(line["move_id"])]
		if !ok {
			continue
		}
		cost, known, err := b.cost(ctx, relID(line["product"]))
		if err != nil {
			return nil, err
		}
		if !known {
			continue
		}
		s := sign(invoice, "out_refund")
		cogs := s * cost * num(line["quantity"])
		at(invoice["invoice_date"]).cogs += cogs
		sellerCost[sellerOf(invoice)] += cogs
		name := relName(line["product"])
		productRevenue[name] += s * num(line["price_subtotal"])
		productCost[name] += cogs
	}
	bills, err := b.invoices(ctx, false, "in_invoice", "in_refund")
	if err != nil {
		return nil, err
	}
	for _, bill := range bills {
		at(bill["invoice_date"]).purchases += sign(bill, "in_refund") * num(bill["amount_untaxed"])
	}
	expenses, err := claims(ctx, b)
	if err != nil {
		return nil, err
	}
	for _, claim := range expenses {
		at(claim["date"]).expenses += num(claim["total_amount"])
	}
	payments, err := b.payments(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, payment := range payments {
		if text(payment["payment_type"]) == "inbound" {
			at(payment["date"]).cashIn += num(payment["amount"])
		} else {
			at(payment["date"]).cashOut += num(payment["amount"])
		}
	}
	figures := &Figures{}
	history, err := b.invoices(ctx, true, "out_invoice")
	if err != nil {
		return nil, err
	}
	for _, invoice := range history {
		residual := num(invoice["amount_residual"])
		figures.Receivable += residual
		if due, ok := parseDay(invoice["invoice_date_due"]); ok && due.before(b.asOf) {
			figures.OverdueReceivable += residual
		}
	}
	allBills, err := b.invoices(ctx, true, "in_invoice")
	if err != nil {
		return nil, err
	}
	for _, bill := range allBills {
		figures.Payable += num(bill["amount_residual"])
	}
	keys := make([]string, 0, len(months))
	for key := range months {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		m := months[key]
		margin := m.revenue - m.cogs
		figures.Months = append(figures.Months, Month{Month: key, Revenue: m.revenue, GrossMargin: margin, MarginRate: rate(margin, m.revenue),
			Purchases: m.purchases, Expenses: m.expenses, CashIn: m.cashIn, CashOut: m.cashOut})
		figures.Revenue += m.revenue
		figures.GrossMargin += margin
	}
	figures.TopCustomers = ranked(customers, nil, figures.Revenue, 5)
	figures.Sellers = ranked(sellerRevenue, sellerCost, 0, 0)
	figures.Products = ranked(productRevenue, productCost, 0, 0)
	figures.Signals = signals(figures)
	return figures, nil
}

func ranked(revenue, cost map[string]float64, total float64, limit int) []Share {
	out := make([]Share, 0, len(revenue))
	for name, value := range revenue {
		share := Share{Name: name, Revenue: value}
		if total != 0 {
			share.Share = value / total
		}
		if cost != nil {
			share.MarginRate = rate(value-cost[name], value)
		}
		out = append(out, share)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Revenue != out[j].Revenue {
			return out[i].Revenue > out[j].Revenue
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// signals are plain statements of direction the figures support, nothing they do not.
func signals(f *Figures) []string {
	var out []string
	if len(f.Months) >= 2 {
		first, last := f.Months[0], f.Months[len(f.Months)-1]
		if first.Revenue != 0 {
			change := (last.Revenue - first.Revenue) / first.Revenue
			direction := "tăng"
			if change < 0 {
				direction, change = "giảm", -change
			}
			out = append(out, fmt.Sprintf("Doanh thu tháng %s %s %s so với tháng %s.", last.Month, direction, pct(change, 0), first.Month))
		}
		var rates []float64
		for _, m := range f.Months {
			if m.MarginRate != nil {
				rates = append(rates, *m.MarginRate)
			}
		}
		if len(rates) >= 2 && last.MarginRate != nil {
			sort.Float64s(rates)
			out = append(out, fmt.Sprintf("Biên lãi gộp dao động %s – %s; tháng cuối %s.", pct(rates[0], 1), pct(rates[len(rates)-1], 1), pct(*last.MarginRate, 1)))
		}
	}
	if len(f.TopCustomers) > 0 && f.TopCustomers[0].Share >= 0.3 {
		out = append(out, fmt.Sprintf("Phụ thuộc một khách: %s chiếm %s doanh thu.", f.TopCustomers[0].Name, pct(f.TopCustomers[0].Share, 0)))
	}
	if f.Receivable != 0 {
		out = append(out, fmt.Sprintf("%s số tiền khách đang nợ đã quá hạn.", pct(f.OverdueReceivable/f.Receivable, 0)))
	}
	return out
}
