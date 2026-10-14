package review

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// date is a civil day.
type date struct{ t time.Time }

func day(t time.Time) date {
	y, m, d := t.Date()
	return date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// parseDay reads "2026-07-06" or "2026-07-06 10:00:00"; ok is false for an empty or unreadable value.
func parseDay(value interface{}) (date, bool) {
	text, isText := value.(string)
	if !isText || len(text) < 10 {
		return date{}, false
	}
	t, err := time.Parse("2006-01-02", text[:10])
	if err != nil {
		return date{}, false
	}
	return date{t}, true
}

func (d date) format() string           { return d.t.Format("2006-01-02") }
func (d date) month() string            { return d.t.Format("2006-01") }
func (d date) before(other date) bool   { return d.t.Before(other.t) }
func (d date) after(other date) bool    { return d.t.After(other.t) }
func (d date) daysUntil(other date) int { return int(other.t.Sub(d.t).Hours() / 24) }

// books are the records of one Odoo over one period, read once and shared by every check.
type books struct {
	reader       Reader
	from, to     date
	asOf         date
	cache        map[string][]map[string]interface{}
	variantsByID map[int64]map[string]interface{}
	productsByID map[int64]map[string]interface{}
	taxNames     map[int64]string
}

func newBooks(reader Reader, from, to, asOf date) *books {
	return &books{reader: reader, from: from, to: to, asOf: asOf, cache: map[string][]map[string]interface{}{}}
}

func (b *books) read(ctx context.Context, operation string, domain []interface{}) ([]map[string]interface{}, error) {
	key := operation
	if domain != nil {
		encoded, _ := json.Marshal(domain)
		key += string(encoded)
	}
	if rows, cached := b.cache[key]; cached {
		return rows, nil
	}
	rows, err := b.reader.ReadAll(ctx, operation, domain)
	if err != nil {
		return nil, err
	}
	b.cache[key] = rows
	return rows, nil
}

func (b *books) inPeriod(value interface{}) bool {
	d, ok := parseDay(value)
	return ok && !d.before(b.from) && !d.after(b.to)
}

// invoices answers posted documents of the given types dated in the period, or up to its end with
// history.
func (b *books) invoices(ctx context.Context, history bool, types ...string) ([]map[string]interface{}, error) {
	rows, err := b.read(ctx, "finance.invoice.list", []interface{}{[]interface{}{"state", "=", "posted"}})
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var out []map[string]interface{}
	for _, row := range rows {
		d, ok := parseDay(row["invoice_date"])
		if !wanted[text(row["document_type"])] || !ok || d.after(b.to) || (!history && d.before(b.from)) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (b *books) invoiceLines(ctx context.Context) ([]map[string]interface{}, error) {
	rows, err := b.read(ctx, "finance.invoice.line.list", []interface{}{
		[]interface{}{"parent_state", "=", "posted"}, []interface{}{"display_type", "=", "product"}})
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, row := range rows {
		if b.inPeriod(row["date"]) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (b *books) payments(ctx context.Context, history bool) ([]map[string]interface{}, error) {
	rows, err := b.read(ctx, "finance.payment.list", []interface{}{
		[]interface{}{"state", "!=", "draft"}, []interface{}{"state", "!=", "canceled"}})
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, row := range rows {
		d, ok := parseDay(row["date"])
		if !ok || d.after(b.to) || (!history && d.before(b.from)) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (b *books) contacts(ctx context.Context) ([]map[string]interface{}, error) {
	return b.read(ctx, "identity.contact.list", nil)
}

// cost is a product variant's cost: its template's standard price.
func (b *books) cost(ctx context.Context, variant int64) (float64, bool, error) {
	if b.variantsByID == nil {
		variants, err := b.read(ctx, "catalog.product_variant.list", nil)
		if err != nil {
			return 0, false, err
		}
		products, err := b.read(ctx, "catalog.product.list", nil)
		if err != nil {
			return 0, false, err
		}
		b.variantsByID, b.productsByID = map[int64]map[string]interface{}{}, map[int64]map[string]interface{}{}
		for _, row := range variants {
			b.variantsByID[relID(row["id"])] = row
		}
		for _, row := range products {
			b.productsByID[relID(row["id"])] = row
		}
	}
	v, ok := b.variantsByID[variant]
	if !ok {
		return 0, false, nil
	}
	p, ok := b.productsByID[relID(v["product_tmpl_id"])]
	if !ok {
		return 0, false, nil
	}
	price, isNumber := number(p["standard_price"])
	return price, isNumber, nil
}

func (b *books) taxLabel(ctx context.Context, ids []int64) (string, error) {
	if b.taxNames == nil {
		rows, err := b.read(ctx, "finance.tax.list", nil)
		if err != nil {
			return "", err
		}
		b.taxNames = map[int64]string{}
		for _, row := range rows {
			b.taxNames[relID(row["id"])] = text(row["name"])
		}
	}
	if len(ids) == 0 {
		return "không thuế", nil
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if name := b.taxNames[id]; name != "" {
			parts = append(parts, name)
		} else {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
	}
	return strings.Join(parts, " + "), nil
}

// ---- values ------------------------------------------------------------------------------------

// relID is a relation's id, whatever shape it came in: [id, name], {id, display_name} or a number.
func relID(value interface{}) int64 {
	switch v := value.(type) {
	case map[string]interface{}:
		return relID(v["id"])
	case []interface{}:
		if len(v) > 0 {
			return relID(v[0])
		}
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func relName(value interface{}) string {
	switch v := value.(type) {
	case map[string]interface{}:
		return text(v["display_name"])
	case []interface{}:
		if len(v) == 2 {
			return text(v[1])
		}
	}
	return ""
}

func relIDs(value interface{}) []int64 {
	list, _ := value.([]interface{})
	out := make([]int64, 0, len(list))
	for _, item := range list {
		out = append(out, relID(item))
	}
	return out
}

func text(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func number(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func num(value interface{}) float64 {
	n, _ := number(value)
	return n
}

var nonDigits = regexp.MustCompile(`\D`)
var nonAlnum = regexp.MustCompile(`[^0-9A-Z]`)

func digits(value interface{}) string { return nonDigits.ReplaceAllString(text(value), "") }

func lettersDigits(value interface{}) string {
	return nonAlnum.ReplaceAllString(strings.ToUpper(text(value)), "")
}

// money is "12.500.000 đ".
func money(amount float64) string {
	negative := amount < 0
	digits := strconv.FormatInt(int64(math.Round(math.Abs(amount))), 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if negative {
		return "-" + b.String() + " đ"
	}
	return b.String() + " đ"
}

// pct is "18,7%".
func pct(share float64, decimals int) string {
	return strings.Replace(strconv.FormatFloat(share*100, 'f', decimals, 64), ".", ",", 1) + "%"
}

func evidence(operation string, row map[string]interface{}, name string) Evidence {
	if name == "" {
		name = text(row["name"])
	}
	return Evidence{Operation: operation, ID: relID(row["id"]), Name: name}
}

func quoted(name string) string { return fmt.Sprintf("'%s'", name) }
