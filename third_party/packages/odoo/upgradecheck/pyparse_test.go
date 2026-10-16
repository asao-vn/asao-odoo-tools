package upgradecheck

import (
	"reflect"
	"testing"
)

func TestParseExpressionReadsWhatOdooStores(t *testing.T) {
	for _, text := range []string{
		`[('state', '=', 'draft')]`,
		`['|', ('a', '=', 1), '&', ('b', 'in', [1, 2]), ('c', 'not in', (3,))]`,
		`[('date', '>=', (context_today() - relativedelta(days=7)).strftime('%Y-%m-%d'))]`,
		`[('user_id', '=', uid), ('company_id', 'in', allowed_company_ids)]`,
		`[('line_ids', 'any', [('product_id.type', '=', 'service')])]`,
		`{'group_by': ['stage_id', 'create_date:month'], 'search_default_mine': 1, **ctx}`,
		`{'default_partner_id': active_id, 'active_test': False}`,
		`not parent.is_company or state in ('done', 'cancel')`,
		`partner_id and partner_id.country_id.code != 'VN' and amount_total > 1_000.5`,
		`x if y else (z or None)`,
		`lambda r, s=1, *a, **k: r.name`,
		`[line.price for line in object.order_line if line.price > 0]`,
		`{k: v for k, v in values.items()}`,
		`sum(l.amount for l in lines)`,
		`"""a
b""" 'c' r'\d' b'raw' u'é'`,
		`f'{object.name}'`,
		`a[1:2], a[::2], a[x, y], a[...]`,
		`func(*args, key=value, **kwargs)`,
		`value is not None and other not in items`,
		`-x ** 2 + ~y // 3 % 4 @ m`,
		`0x1F + 0o17 + 0b11 + 1e-3 + .5 + 2j`,
		`(x := 5)`,
		"[\n  ('a', '=', 1),  # a comment\n  ('b', '=', 2),\n]",
	} {
		if _, err := parseExpression(text); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
	for _, text := range []string{`[('a', '=', 1)`, `a b`, `'open`, `foo(`, `)`, `x = 1`, `print "x"`, `a $ b`, `if x`, ``} {
		if _, err := parseExpression(text); err == nil {
			t.Errorf("%q was read", text)
		}
	}
}

func TestParseExpressionBuildsTheTree(t *testing.T) {
	n, err := parseExpression(`[('partner_id.country_id', '=', 'VN'), ('state', 'in', ['a', 'b'])]`)
	if err != nil {
		t.Fatal(err)
	}
	if n.kind != nList || len(n.items) != 2 || n.items[0].kind != nTuple {
		t.Fatalf("not a list of tuples: %+v", n)
	}
	if path, _ := constString(n.items[0].items[0]); path != "partner_id.country_id" {
		t.Fatalf("path %q", path)
	}
	if values := literalValues(n.items[1].items[2]); !reflect.DeepEqual(values, []interface{}{"a", "b"}) {
		t.Fatalf("values %v", values)
	}
	n, err = parseExpression(`object.partner_id.name`)
	if err != nil {
		t.Fatal(err)
	}
	if chain := chainOf(n); !reflect.DeepEqual(chain, []string{"object", "partner_id", "name"}) {
		t.Fatalf("chain %v", chain)
	}
	n, err = parseExpression(`'a\'b\n' "\x41é"`)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := constString(n); value != "a'b\nAé" {
		t.Fatalf("string %q", value)
	}
	if n, _ := parseExpression(`True`); n.kind != nConst || n.value != true {
		t.Fatalf("True is %+v", n)
	}
}

func TestParseCodeReadsServerActions(t *testing.T) {
	code := `# A server action as people write them
for line in records.order_line:
    if line.product_id.type == 'service' and line.qty_delivered < line.product_uom_qty:
        line.write({'qty_delivered': line.product_uom_qty})
    elif line.discount: line.discount = 0; continue
total = sum(records.mapped('amount_total'))
partners, count = env['res.partner'].search([('customer_rank', '>', 0)]), 0
count += 1
try:
    with env.cr.savepoint() as point:
        records.filtered(lambda r: r.state in ('draft', 'sent')).action_confirm()
except UserError as error:
    log("could not confirm: %s" % error, level='warning')
finally:
    pass
def helper(value, default=None):
    return value or default
@decorate(x)
class Box(object):
    pass
action = {
    'type': 'ir.actions.act_window',
    'res_model': 'sale.order',
    'domain': [('id', 'in', records.ids)],
}
if not records: raise UserError(_("Nothing to do"))
x = records \
    .sudo()
del x
assert total >= 0, "negative"
`
	prog, err := parseCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.loops) != 1 || prog.loops[0].target != "line" || !reflect.DeepEqual(chainOf(prog.loops[0].iter), []string{"records", "order_line"}) {
		t.Fatalf("loops %+v", prog.loops)
	}
	lambdas, calls := 0, 0
	for _, expr := range prog.exprs {
		walk(expr, func(n *node) {
			switch n.kind {
			case nLambda:
				lambdas++
			case nCall:
				calls++
			}
		})
	}
	if lambdas != 1 || calls < 10 {
		t.Fatalf("%d lambdas, %d calls", lambdas, calls)
	}
	for _, broken := range []string{"records.write({'a': 1}", "print 'x'", "for x records:", "if x\n    pass", "x = = 1"} {
		if _, err := parseCode(broken); err == nil {
			t.Errorf("%q was read", broken)
		}
	}
}

func TestStartTagsReadsATemplateAsHTML(t *testing.T) {
	tags := startTags(`<p>Dear <t t-out="object.partner_id.name"/>,<!-- <t t-out="object.hidden"/> -->
<a T-ATT-HREF="object.website_url" class=plain>link</a><br><t t-foreach='object.order_line' t-as="line">
<span t-esc="line.product_id.name &amp;&amp; 1"></span></t></p>`)
	var outs []string
	for _, tag := range tags {
		for name, value := range tag {
			if name == "t-out" || name == "t-att-href" || name == "t-esc" || name == "t-foreach" {
				outs = append(outs, name+"="+value)
			}
		}
	}
	want := []string{"t-out=object.partner_id.name", "t-att-href=object.website_url", "t-foreach=object.order_line", "t-esc=line.product_id.name && 1"}
	if !reflect.DeepEqual(outs, want) {
		t.Fatalf("got %v, want %v", outs, want)
	}
}

func TestParseXMLRefusesWhatOdooRefuses(t *testing.T) {
	if _, err := parseXML(`<form><field name="a"/></form>`); err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{`<form>`, `<a/><b/>`, `text<a/>`, `<a>&nbsp;</a>`, ``} {
		if _, err := parseXML(broken); err == nil {
			t.Errorf("%q was read", broken)
		}
	}
}
