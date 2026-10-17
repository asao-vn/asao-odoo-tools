# Runs inside `odoo shell` on the review-simulation lab (env is given). It writes six months
# (2026-04-01 .. 2026-09-30) of one small Vietnamese building-materials trader through Odoo's own
# business flows (sale -> delivery -> invoice -> payment, purchase -> receipt -> bill -> payment,
# expenses, time off, scrap), then plants the errors and frauds a review must find, plus look-alikes a
# review must NOT flag. It prints one line "TRUTH=<json>": every planted case, the check that should
# catch it and the records that prove it. Deterministic: the same seed writes the same business.
import datetime
import json
import random

rng = random.Random(20261014)
D = datetime.date
START, END = D(2026, 4, 1), D(2026, 9, 30)
truth = []      # {"case", "check", "kind": "planted"|"decoy", "records": [...], "note"}


def plant(case, check, records, note, kind="planted"):
    truth.append({"case": case, "check": check, "kind": kind, "records": records, "note": note})


def days(start, end):
    d = start
    while d <= end:
        yield d
        d += datetime.timedelta(days=1)


def workdays(start, end):
    return [d for d in days(start, end) if d.weekday() < 6]


company = env.company
VND = company.currency_id
tax = lambda use, amount: env["account.tax"].search([("type_tax_use", "=", use), ("amount", "=", amount),
                                                   ("name", "=", "%d%%" % amount)], limit=1)
SALE10, SALE8, SALE0 = tax("sale", 10), tax("sale", 8), tax("sale", 0)
PUR10, PUR8 = tax("purchase", 10), tax("purchase", 8)
bank = env["account.journal"].search([("type", "=", "bank")], limit=1)
stock_loc = env["stock.warehouse"].search([], limit=1).lot_stock_id
expense_acc = env["account.account"].search([("code", "=like", "642%"), ("account_type", "=", "expense")], limit=1) \
    or env["account.account"].search([("account_type", "=", "expense")], limit=1)

# Purchases above 20 million need a second approval (Odoo's own two-step purchase validation).
company.write({"po_double_validation": "two_step", "po_double_validation_amount": 20_000_000})

# ---- people ------------------------------------------------------------------------------------
user_group = env.ref("base.group_user")


def user(name, login):
    return env["res.users"].create({"name": name, "login": login, "group_ids": [(6, 0, [user_group.id])]})


U = {k: user(n, k) for k, n in [("lan", "Phạm Thị Lan"), ("hung", "Lê Văn Hùng"), ("tuan", "Đỗ Minh Tuấn"),
                                 ("binh", "Trần Văn Bình"), ("mai", "Ngô Thị Mai"), ("khoa", "Võ Đăng Khoa")]}
jobs = {"lan": "Nhân viên kinh doanh", "hung": "Nhân viên kinh doanh", "tuan": "Nhân viên kinh doanh",
        "binh": "Thủ kho kiêm mua hàng", "mai": "Kế toán", "khoa": "Giám đốc"}
E, emp_bank = {}, {}
for i, (key, u) in enumerate(U.items()):
    e = env["hr.employee"].create({"name": u.name, "user_id": u.id, "job_title": jobs[key]})
    acc = env["res.partner.bank"].create({"acc_number": "99990004%05d" % (56789 + i * 1111), "partner_id": e.work_contact_id.id,
                                          "bank_name": "Ngân hàng A (giả lập)"})
    try:
        e.write({"bank_account_ids": [(6, 0, [acc.id])]})
    except Exception:  # field shape differs across 19.x builds; the account still belongs to the contact
        pass
    E[key], emp_bank[key] = e, acc
# The director approves purchases above the threshold, which Odoo checks on the user, not on sudo.
U["khoa"].write({"group_ids": [(4, env.ref("purchase.group_purchase_manager").id)]})

# ---- partners ----------------------------------------------------------------------------------
def partner(name, **kw):
    vals = {"name": name, "is_company": kw.pop("company", True), "country_id": env.ref("base.vn").id}
    vals.update(kw)
    return env["res.partner"].create(vals)


streets = ["Nguyễn Văn Linh", "Hùng Vương", "Lê Duẩn", "Điện Biên Phủ", "Tôn Đức Thắng", "Ngô Quyền", "Trần Phú"]
customers = []
for i, name in enumerate(["Công ty Xây dựng Phú Mỹ", "Công ty TNHH Kiến trúc Sơn Trà", "Cửa hàng Bảo Ngọc",
                          "Công ty CP Đầu tư Hoà Xuân", "Nhà thầu Lê Thành", "Công ty TNHH Nội thất Mộc An",
                          "Cửa hàng VLXD Thanh Bình", "Công ty Xây lắp Miền Trung", "Hộ kinh doanh Tư Sương",
                          "Công ty TNHH Cơ điện Hải Vân", "Nguyễn Thị Hoa", "Cửa hàng Điện nước Minh Khang",
                          "Công ty CP Xây dựng Ngũ Hành Sơn", "Nhà thầu Phan Văn Tài", "Công ty TNHH Thương mại Cẩm Lệ",
                          "Cửa hàng Kim khí Hiệp Phát", "Công ty TNHH Xây dựng Liên Chiểu", "Trường Mầm non Hoa Sen",
                          "Công ty CP Bất động sản Sông Hàn", "Cửa hàng Sơn Phương Nam"]):
    is_person = name.startswith("Nguyễn")
    customers.append(partner(name, company=not is_person, street="%d %s" % (10 + i * 7, streets[i % 7]), city="Đà Nẵng",
                             phone="0900 %03d %03d" % (100 + i, 200 + i * 3), email="kh%02d@example.com" % i,
                             vat=None if is_person else "99%08d" % (10000 + i * 7),
                             customer_rank=1))
vendor_specs = [("Công ty CP Xi măng Hải Vân Sơn", "9900000101"), ("Công ty TNHH Thép Ngọc Thành", "9900000102"),
                ("Công ty CP Gạch men Hoà Khánh", "9900000103"), ("Công ty TNHH Sơn Mỹ Khê", "9900000104"),
                ("Công ty CP Nhựa Liên Chiểu", "9900000105"), ("Công ty TNHH Dụng cụ điện An Phú", "9900000106")]
vendors = [partner(n, vat=v, street="KCN Hoà Khánh", city="Đà Nẵng", phone="0200 37%05d" % (i * 1234), supplier_rank=1)
           for i, (n, v) in enumerate(vendor_specs)]
landlord = partner("Công ty TNHH Kho vận Hoà Cầm", vat="9900000107", street="Đường số 2, KCN Hoà Cầm", city="Đà Nẵng", supplier_rank=1)
utility = partner("Công ty Điện nước Sông Hàn", vat="9900000108", street="12 Bạch Đằng", city="Đà Nẵng", supplier_rank=1)

# ---- products ----------------------------------------------------------------------------------
P = {}
for key, name, cost, price, vendor in [
        ("ximang", "Xi măng PCB40 (bao 50kg)", 78_000, 92_000, 0), ("thep10", "Thép cuộn D10 (kg)", 15_500, 18_200, 1),
        ("thep16", "Thép thanh D16 (cây 11,7m)", 245_000, 289_000, 1), ("gach60", "Gạch men 60x60 (thùng)", 210_000, 265_000, 2),
        ("gach30", "Gạch ốp 30x60 (thùng)", 165_000, 205_000, 2), ("sonnuoc", "Sơn nước nội thất 17L", 1_450_000, 1_790_000, 3),
        ("sonlot", "Sơn lót chống kiềm 17L", 1_150_000, 1_420_000, 3), ("ong21", "Ống nhựa PVC D21 (cây 4m)", 21_000, 27_000, 4),
        ("ong90", "Ống nhựa PVC D90 (cây 4m)", 98_000, 124_000, 4), ("khoan", "Máy khoan động lực 750W", 1_850_000, 2_290_000, 5),
        ("mai", "Máy mài góc 670W", 890_000, 1_120_000, 5), ("cat", "Cát xây (m3)", 280_000, 360_000, 0)]:
    tmpl = env["product.template"].create({
        "name": name, "type": "consu", "is_storable": True, "list_price": price, "standard_price": cost,
        "invoice_policy": "order", "purchase_method": "purchase",
        "taxes_id": [(6, 0, [SALE10.id])], "supplier_taxes_id": [(6, 0, [PUR10.id])],
        "seller_ids": [(0, 0, {"partner_id": vendors[vendor].id, "price": cost})]})
    P[key] = tmpl.product_variant_id
    env["stock.quant"]._update_available_quantity(P[key], stock_loc, 2000)
env.cr.commit()


# ---- flows -------------------------------------------------------------------------------------
def deliver(pickings, when):
    for picking in pickings.filtered(lambda p: p.state not in ("done", "cancel")):
        picking.action_assign()
        for move in picking.move_ids:
            move.quantity = move.product_uom_qty
            move.picked = True
        picking.with_context(skip_backorder=True, skip_sms=True).button_validate()
        picking.date_done = datetime.datetime.combine(when, datetime.time(10))


def pay(moves, when, journal=bank, amount=None):
    values = {"payment_date": when, "journal_id": journal.id, "group_payment": True}
    if amount:  # a part of what is due; the rest stays open
        values.update({"amount": amount, "payment_difference_handling": "open"})
    wizard = env["account.payment.register"].with_context(active_model="account.move", active_ids=moves.ids).create(values)
    return wizard._create_payments()


def sale(customer, salesperson, when, lines, pay_on=None):
    so = env["sale.order"].with_user(U[salesperson]).sudo().create({
        "partner_id": customer.id, "user_id": U[salesperson].id,
        "order_line": [(0, 0, {"product_id": P[k].id, "product_uom_qty": q, "price_unit": price if price else P[k].lst_price,
                               "tax_ids": [(6, 0, [(t or SALE10).id])]}) for k, q, price, t in lines]})
    so.action_confirm()
    so.date_order = datetime.datetime.combine(when, datetime.time(9))
    deliver(so.picking_ids, when)
    inv = so._create_invoices()
    inv.write({"invoice_date": when, "invoice_payment_term_id": env.ref("account.account_payment_term_30days").id})
    inv.with_user(U[salesperson]).sudo().action_post()
    if pay_on:
        pay(inv, pay_on)
    return so, inv


def purchase(vendor, when, lines, ref, pay_on=None, by="binh"):
    po = env["purchase.order"].with_user(U[by]).sudo().create({
        "partner_id": vendor.id, "date_order": datetime.datetime.combine(when, datetime.time(8)),
        "order_line": [(0, 0, {"product_id": P[k].id, "product_qty": q, "price_unit": price or P[k].standard_price})
                       for k, q, price in lines]})
    po.button_confirm()
    if po.state == "to approve":
        po.with_user(U["khoa"]).sudo().button_approve()
    assert po.state == "purchase", "%s stayed %s" % (po.name, po.state)
    deliver(po.picking_ids, when)
    po.action_create_invoice()
    bill = po.invoice_ids[-1:]
    assert bill.amount_total > 0, "%s billed nothing" % po.name
    bill.write({"invoice_date": when, "ref": ref})
    bill.with_user(U[by]).sudo().action_post()
    if pay_on:
        pay(bill, pay_on)
    return po, bill


def bill(vendor, when, ref, amount, label, pay_on=None, by="mai", taxes=()):
    move = env["account.move"].with_user(U[by]).sudo().create({
        "move_type": "in_invoice", "partner_id": vendor.id, "invoice_date": when, "ref": ref,
        "invoice_line_ids": [(0, 0, {"name": label, "quantity": 1, "price_unit": amount, "account_id": expense_acc.id,
                                     "tax_ids": [(6, 0, [t.id for t in taxes])]})]})
    move.action_post()
    if pay_on:
        pay(move, pay_on)
    return move


AS_OF = END
sellers = ["lan", "hung", "tuan"]
# Regular sales: about two orders a working day, paid 5..40 days later (a few never: the receivables).
for d in workdays(START, END):
    for _ in range(rng.choice([1, 2, 2, 3])):
        customer = rng.choice(customers)
        keys = rng.sample(list(P), rng.randint(1, 3))
        lines = [(k, rng.choice([5, 10, 20, 50, 100]) if P[k].lst_price < 400_000 else rng.randint(1, 6), None, None) for k in keys]
        paid = d + datetime.timedelta(days=rng.randint(5, 40))
        sale(customer, rng.choice(sellers), d, lines, pay_on=paid if paid <= AS_OF and rng.random() > 0.08 else None)
    env.cr.commit()

# Regular purchases: each supplier about every two weeks, 25..60 million (so the director approves each),
# paid within 30 days.
supplier_items = {0: ["ximang", "cat"], 1: ["thep10", "thep16"], 2: ["gach60", "gach30"], 3: ["sonnuoc", "sonlot"],
                  4: ["ong21", "ong90"], 5: ["khoan", "mai"]}
bill_no = 1000
for d in workdays(START, END):
    for v, items in supplier_items.items():
        if (d.toordinal() + v * 3) % 14 == 0:
            bill_no += 1
            k = rng.choice(items)
            qty = max(1, int(rng.uniform(25_000_000, 60_000_000) // P[k].standard_price))
            paid = d + datetime.timedelta(days=rng.randint(7, 30))
            purchase(vendors[v], d, [(k, qty, None)], "HD%07d" % (bill_no * 37), pay_on=paid if paid <= AS_OF else None)
    env.cr.commit()

# Monthly overheads: warehouse rent (the same 15 million every month: a DECOY for duplicate checks) and power.
rent_bills = []
for month in range(4, 10):
    d = D(2026, month, 5)
    rent_bills.append(bill(landlord, d, "TK-%02d/2026" % month, 15_000_000, "Thuê kho tháng %d/2026" % month,
                           pay_on=d + datetime.timedelta(days=3)).name)
    bill(utility, D(2026, month, 12), "PE%02d2026-%04d" % (month, 300 + month), rng.randint(3_200_000, 4_800_000),
         "Tiền điện tháng %d" % month, pay_on=D(2026, month, 15), taxes=[PUR10])
plant("rent-same-amount", "O01", rent_bills, "15 triệu mỗi tháng, số tham chiếu khác nhau: hợp lệ, không được báo", kind="decoy")

# Expenses: everyone claims a few a month, odd amounts.
cats = ["Xăng xe công tác", "Gửi xe", "Tiếp khách", "Văn phòng phẩm", "Cước điện thoại"]


CATEGORY = {}
for label, cat in [("Xăng xe công tác", "Công tác phí"), ("Gửi xe", "Công tác phí"), ("Tiếp khách", "Tiếp khách"),
                   ("Văn phòng phẩm", "Văn phòng phẩm"), ("Cước điện thoại", "Cước điện thoại")]:
    CATEGORY[label] = env["product.product"].search([("name", "=", cat), ("can_be_expensed", "=", True)], limit=1) or \
        env["product.product"].create({"name": cat, "type": "service", "can_be_expensed": True, "standard_price": 0,
                                       "supplier_taxes_id": [(6, 0, [])]})


def expense(key, when, amount, label):
    category = next((c for prefix, c in CATEGORY.items() if label.startswith(prefix)), CATEGORY["Văn phòng phẩm"])
    exp = env["hr.expense"].with_user(U[key]).sudo().create({
        "name": label, "employee_id": E[key].id, "product_id": category.id, "total_amount_currency": amount,
        "date": when, "payment_mode": "own_account"})
    for step in ("action_submit", "action_approve"):
        try:
            getattr(exp.with_user(U["khoa"]).sudo(), step)()
        except Exception:
            pass
    return exp


for key in ["lan", "hung", "tuan", "binh"]:
    for month in range(4, 10):
        for _ in range(rng.randint(2, 4)):
            when = D(2026, month, rng.randint(1, 28))
            expense(key, when, rng.randrange(85_000, 1_400_000, 1_000) + rng.choice([0, 500]), rng.choice(cats))
env.cr.commit()

# Normal scrap: a few broken bags and tiles by different people.
def scrap(key, product, qty, when):
    s = env["stock.scrap"].with_user(U[key]).sudo().create({"product_id": P[product].id, "scrap_qty": qty,
                                                             "location_id": stock_loc.id})
    s.action_validate()
    s.date_done = datetime.datetime.combine(when, datetime.time(16))
    return s


for key, product, qty, when in [("lan", "ximang", 3, D(2026, 4, 22)), ("binh", "gach60", 2, D(2026, 5, 9)),
                                ("hung", "ximang", 2, D(2026, 6, 17)), ("binh", "ong21", 6, D(2026, 7, 3)),
                                ("mai", "gach30", 1, D(2026, 8, 21))]:
    scrap(key, product, qty, when)

# ====================================== planted cases ===========================================
# 1. A supplier bill entered twice: same supplier, same reference, same amount, six days apart, both paid.
_, b1 = purchase(vendors[0], D(2026, 5, 14), [("ximang", 400, None)], "HD0001234", pay_on=D(2026, 5, 25))
b2 = bill(vendors[0], D(2026, 5, 20), "HD0001234", b1.amount_untaxed, "Xi măng PCB40 theo HĐ 0001234", pay_on=D(2026, 5, 28), by="binh", taxes=[PUR10])
plant("bill-twice-same-ref", "O01", [b1.name, b2.name], "Cùng NCC, cùng số HĐ HD0001234, cùng số tiền, cả hai đã trả: trả tiền hai lần")
# 2. The same, disguised: the reference re-typed with a dash, nine days later.
_, b3 = purchase(vendors[3], D(2026, 7, 8), [("sonnuoc", 30, None)], "HD0045871", pay_on=D(2026, 7, 20))
b4 = bill(vendors[3], D(2026, 7, 17), "HD-0045871", b3.amount_untaxed, "Sơn nội thất theo HĐ 45871", pay_on=D(2026, 7, 24), by="binh", taxes=[PUR10])
plant("bill-twice-retyped-ref", "O01", [b3.name, b4.name], "Số HĐ gõ lại có gạch nối (HD0045871 / HD-0045871), cùng số tiền, cả hai đã trả")

# 3. A customer payment recorded twice (the second one sits unreconciled as a credit).
_, inv = sale(customers[2], "lan", D(2026, 6, 3), [("gach60", 80, None, None)], pay_on=D(2026, 6, 10))
dup = env["account.payment"].create({"payment_type": "inbound", "partner_type": "customer", "partner_id": customers[2].id,
                                     "amount": inv.amount_total, "date": D(2026, 6, 11), "journal_id": bank.id,
                                     "memo": inv.name})
dup.action_post()
plant("customer-paid-twice", "O22", [inv.name, dup.name], "Thu tiền khách 2 lần cho cùng một hoá đơn, lần sau chưa đối soát")

# 4. A shell supplier: created by the storekeeper, no tax code, no address, paid into the storekeeper's own
#    bank account, five transport bills of 14..18 million in three months.
shell = env["res.partner"].with_user(U["binh"]).sudo().create({"name": "Dịch vụ Vận tải Hoà Phát", "is_company": True,
                                                               "phone": "0900 777 123", "supplier_rank": 1})
env["res.partner.bank"].create({"acc_number": emp_bank["binh"].acc_number, "partner_id": shell.id, "bank_name": "Ngân hàng A (giả lập)"})
shell_bills = []
for when, amount in [(D(2026, 7, 6), 14_800_000), (D(2026, 7, 21), 16_200_000), (D(2026, 8, 11), 17_500_000),
                     (D(2026, 8, 28), 15_900_000), (D(2026, 9, 15), 18_300_000)]:
    shell_bills.append(bill(shell, when, "VC%s" % when.strftime("%m%d"), amount, "Cước vận chuyển hàng", by="binh",
                            pay_on=when + datetime.timedelta(days=1)).name)
plant("vendor-bank-is-employee", "O17", [shell.name, E["binh"].name], "TK ngân hàng NCC trùng TK của thủ kho Trần Văn Bình")
plant("new-vendor-no-tax-code-paid-fast", "O18", [shell.name] + shell_bills, "NCC mới, không MST, không địa chỉ, nhận 82,7 triệu trong 10 tuần, trả trong 1 ngày")

# 5. A duplicate supplier record: the same tax code under a shorter name, created later by the storekeeper.
twin = env["res.partner"].with_user(U["binh"]).sudo().create({"name": "Cty Thép Ngọc Thành", "is_company": True, "vat": "9900000102",
                                                              "supplier_rank": 1, "country_id": env.ref("base.vn").id})
plant("supplier-twin-same-tax-code", "O13", [vendors[1].name, twin.name], "Hai hồ sơ NCC cùng MST 9900000102")
# ... and a duplicate customer: the same phone number under a nickname.
nick = partner("Chị Hoa (Hải Châu)", company=False, phone=customers[10].phone, customer_rank=1)
plant("customer-twin-same-phone", "O13", [customers[10].name, nick.name], "Hai hồ sơ khách cùng số điện thoại")

# 6. Splitting to stay under the 20-million second approval: three orders from one supplier in three days.
split = []
for i, when in enumerate([D(2026, 8, 12), D(2026, 8, 13), D(2026, 8, 14)]):
    # 73 x 245.000 = 17,9 million before VAT, 19,7 million with it: Odoo compares the total with VAT.
    po, b = purchase(vendors[1], when, [("thep16", 73, None)], "HD08%d1990" % i, pay_on=when + datetime.timedelta(days=10))
    assert not po.date_approve or po.amount_total < 20_000_000
    split.append(po.name)
plant("split-under-approval", "O16", split, "3 đơn mua thép cùng NCC trong 3 ngày, mỗi đơn 19,7 triệu (gồm VAT) < ngưỡng duyệt 20 triệu, tổng 59 triệu")

# 7. Selling below cost: one salesperson, four lines, a single customer.
below = []
for when, k, price in [(D(2026, 6, 24), "khoan", 1_690_000), (D(2026, 7, 15), "sonnuoc", 1_300_000),
                       (D(2026, 8, 19), "khoan", 1_650_000), (D(2026, 9, 9), "sonnuoc", 1_250_000)]:
    _, inv = sale(customers[14], "tuan", when, [(k, 5, price, None)], pay_on=when + datetime.timedelta(days=12))
    below.append(inv.name)
plant("below-cost-one-seller", "O19", below, "Tuấn bán máy khoan / sơn dưới giá vốn cho cùng một khách, 4 lần")

# 8. A wrong tax rate: 0% VAT on goods that carry 10%.
_, inv = sale(customers[7], "tuan", D(2026, 8, 6), [("thep16", 40, None, SALE0)], pay_on=D(2026, 8, 30))
plant("zero-vat-on-taxable-goods", "O20", [inv.name], "Hoá đơn bán thép xuất thuế 0%, sản phẩm chịu 10%")

# 9. Scrap of expensive goods, always by the same person, in the last two months.
heavy = [scrap("binh", k, q, when).name for k, q, when in [
    ("khoan", 2, D(2026, 8, 4)), ("khoan", 3, D(2026, 8, 18)), ("mai", 4, D(2026, 8, 25)),
    ("khoan", 2, D(2026, 9, 2)), ("sonnuoc", 3, D(2026, 9, 12)), ("khoan", 3, D(2026, 9, 23))]]
plant("scrap-high-value-one-person", "O12", heavy, "Bình huỷ 6 lần hàng giá trị cao (máy khoan, máy mài, sơn) trong 2 tháng")

# 10. The same expense claimed twice.
e1 = expense("hung", D(2026, 7, 18), 1_250_000, "Tiếp khách công trình Hoà Xuân")
e2 = expense("hung", D(2026, 7, 18), 1_250_000, "Tiếp khách CT Hoà Xuân")
plant("expense-claimed-twice", "O23", [e1.name, e2.name], "Hùng kê 2 lần cùng khoản tiếp khách 1.250.000 ngày 18/7")

# 11. An expense on a day the employee was on approved leave.
leave_type = env["hr.leave.type"].search([("requires_allocation", "=", False)], limit=1)
leave = env["hr.leave"].sudo().create({"employee_id": E["lan"].id, "holiday_status_id": leave_type.id,
                                       "request_date_from": D(2026, 8, 5), "request_date_to": D(2026, 8, 7),
                                       "name": "Nghỉ việc riêng"})
leave.with_user(U["khoa"]).sudo().action_approve()
e3 = expense("lan", D(2026, 8, 6), 450_000, "Xăng xe công tác Hội An")
plant("expense-on-leave-day", "O24", [e3.name, "leave %s 05-07/08" % E["lan"].name], "Lan kê xăng công tác ngày 6/8 khi đang nghỉ phép (trạng thái nghỉ: %s)" % leave.state)

# 12. Round amounts only: one person's expenses are all exact hundreds of thousands.
rounds = [expense("tuan", D(2026, m, dd), amt, rng.choice(cats)).name for m, dd, amt in [
    (7, 3, 500_000), (7, 14, 1_000_000), (7, 29, 2_000_000), (8, 8, 1_500_000), (8, 20, 1_000_000),
    (9, 4, 2_000_000), (9, 17, 1_000_000), (9, 26, 1_500_000)]]
plant("round-amount-expenses", "O25", rounds, "Tuấn: 8 khoản chi liền đều là số tròn")

# 13. A full credit note after the invoice was paid, refunded out to the customer.
_, inv = sale(customers[0], "tuan", D(2026, 6, 16), [("thep16", 120, None, None)], pay_on=D(2026, 6, 20))
reversal = env["account.move.reversal"].with_context(active_model="account.move", active_ids=inv.ids).create(
    {"date": D(2026, 7, 3), "journal_id": inv.journal_id.id, "reason": "Khách trả hàng"})
reversal.refund_moves()
refund = inv.reversal_move_ids[:1]
if refund.state == "draft":
    refund.action_post()
pay(refund, D(2026, 7, 4))
plant("credit-note-after-payment", "O21", [inv.name, refund.name], "Hoá đơn đã thu đủ, 2 tuần sau giảm trừ toàn bộ và chi trả lại tiền")

# 14. Receivables left unpaid: picked up by O15 from the regular sales (8% never paid).
plant("overdue-receivables", "O15", ["(mọi hoá đơn bán chưa thu, quá hạn tại 30/09/2026)"], "Công nợ quá hạn tự nhiên của kịch bản")

env.cr.commit()

# ====================================== 2026-10-15: more planted cases ===========================
cash = env["account.journal"].search([("type", "=", "cash")], limit=1) or env["account.journal"].create(
    {"name": "Tiền mặt", "type": "cash", "code": "TM"})
misc = env["account.journal"].search([("type", "=", "general")], limit=1)
accrual_acc = env["account.account"].search([("code", "=like", "335%")], limit=1) or \
    env["account.account"].search([("account_type", "=", "liability_current")], limit=1)


def entry(when, amount, label, by="mai"):
    move = env["account.move"].with_user(U[by]).sudo().create({
        "move_type": "entry", "journal_id": misc.id, "date": when, "ref": label,
        "line_ids": [(0, 0, {"account_id": expense_acc.id, "debit": amount, "name": label}),
                     (0, 0, {"account_id": accrual_acc.id, "credit": amount, "name": label})]})
    move.action_post()
    return move


def credit(invoice, when, share=1.0):
    reversal = env["account.move.reversal"].with_context(active_model="account.move", active_ids=invoice.ids).create(
        {"date": when, "journal_id": invoice.journal_id.id, "reason": "Khách trả hàng"})
    reversal.refund_moves()
    refund = invoice.reversal_move_ids[:1]
    if refund.state == "draft":
        if share < 1:
            for line in refund.invoice_line_ids:
                line.quantity = round(line.quantity * share)
        refund.invoice_date = when
        refund.action_post()
    return refund


stamps = {}  # account.move id -> when it was really created, for the backdated cases

# 15. A customer invoiced twice for one sale (the invoice copied and posted again).
_, d1 = sale(customers[5], "lan", D(2026, 7, 21), [("gach30", 40, None, None)])
d2 = d1.copy({"invoice_date": D(2026, 7, 21)})
d2.action_post()
plant("customer-invoiced-twice", "O02", [d1.name, d2.name], "Hoá đơn bán gạch cho Mộc An bị sao chép và ghi sổ lại cùng ngày 21/7")
# ... and two look-alikes: a duplicate already reversed by a credit note, and the same order two weeks apart.
_, d3 = sale(customers[6], "hung", D(2026, 8, 4), [("ong90", 30, None, None)])
d4 = d3.copy({"invoice_date": D(2026, 8, 4)})
d4.action_post()
credit(d4, D(2026, 8, 6))
plant("duplicate-reversed", "O02", [d3.name, d4.name], "Hoá đơn trùng đã được giảm trừ huỷ: đã xử lý, không báo", kind="decoy")
_, r1 = sale(customers[9], "tuan", D(2026, 5, 6), [("ximang", 50, None, None)], pay_on=D(2026, 5, 20))
_, r2 = sale(customers[9], "tuan", D(2026, 5, 20), [("ximang", 50, None, None)], pay_on=D(2026, 6, 3))
plant("same-order-two-weeks-apart", "O02", [r1.name, r2.name], "Cùng đơn xi măng cách 2 tuần: mua lại, không phải trùng", kind="decoy")

# 16. One bill paid in three parts over two days, each under the 20-million limit.
split_bill = bill(vendors[4], D(2026, 9, 2), "HD0098765", 30_000_000, "Ống nhựa theo HĐ 98765", by="binh", taxes=[PUR10])
parts = [pay(split_bill, when, amount=11_000_000).name for when in (D(2026, 9, 8), D(2026, 9, 8), D(2026, 9, 9))]
plant("payment-split-under-limit", "O07", parts, "Hoá đơn 33 triệu trả làm 3 lần 11 triệu trong 2 ngày, mỗi lần dưới hạn mức duyệt 20 triệu")
# ... and three bills of one vendor paid one by one in three days: ordinary.
separate = []
for i, when in enumerate([D(2026, 9, 14), D(2026, 9, 15), D(2026, 9, 16)]):
    b = bill(vendors[2], when, "HD00777%02d" % i, 8_000_000, "Gạch ốp theo HĐ 777%02d" % i, by="binh", taxes=[PUR10])
    separate.append(pay(b, when).name)
plant("three-bills-paid-one-by-one", "O07", separate, "3 hoá đơn riêng của cùng NCC, mỗi hoá đơn trả một lần: bình thường", kind="decoy")

# 17. Backdating: a sale invoiced on 12/7 dated 30/6, and a manual entry made on 20/9 dated 31/8.
_, late_inv = sale(customers[11], "hung", D(2026, 6, 30), [("khoan", 6, None, None)])
stamps[late_inv.id] = datetime.datetime(2026, 7, 12, 9, 30)
plant("invoice-backdated-into-june", "O26", [late_inv.name], "Hoá đơn bán tạo ngày 12/7 nhưng ghi ngày 30/6: doanh thu dời vào tháng 6")
late_entry = entry(D(2026, 8, 31), 60_000_000, "Trích trước chi phí vận chuyển tháng 8")
stamps[late_entry.id] = datetime.datetime(2026, 9, 20, 16, 0)
plant("entry-backdated-into-august", "O26", [late_entry.name], "Bút toán tay 60 triệu tạo ngày 20/9 nhưng ghi ngày 31/8")
# ... an accrual made five days after month end (closing), and a vendor bill entered late: ordinary.
closing = entry(D(2026, 7, 31), 45_000_000, "Trích trước tiền điện tháng 7")
stamps[closing.id] = datetime.datetime(2026, 8, 5, 10, 0)
plant("month-end-closing-entry", "O26", [closing.name], "Bút toán trích trước làm ngày 5/8 cho tháng 7: khoá sổ bình thường", kind="decoy")
late_bill = bill(utility, D(2026, 5, 28), "PE-LATE-0528", 2_400_000, "Hoá đơn nước tháng 5 đến muộn", pay_on=D(2026, 6, 25), taxes=[PUR10])
stamps[late_bill.id] = datetime.datetime(2026, 6, 20, 11, 0)
plant("vendor-bill-arrived-late", "O26", [late_bill.name], "Hoá đơn nhà cung cấp đến muộn, nhập sau: bình thường", kind="decoy")

# 18. Quarter-end revenue taken back in July, and a month-end sale 60% credited early next month.
_, q_inv = sale(customers[4], "hung", D(2026, 6, 29), [("sonnuoc", 60, None, None)])
q_ref = credit(q_inv, D(2026, 7, 6))
plant("quarter-end-revenue-reversed", "O27", [q_inv.name, q_ref.name], "Hoá đơn 29/6 (cuối quý) bị giảm trừ toàn bộ ngày 6/7")
_, m_inv = sale(customers[12], "lan", D(2026, 8, 28), [("thep16", 80, None, None)])
m_ref = credit(m_inv, D(2026, 9, 3), share=0.6)
plant("month-end-revenue-partly-reversed", "O27", [m_inv.name, m_ref.name], "Hoá đơn 28/8 bị giảm trừ 60% ngày 3/9")
# ... a mid-month sale credited early next month, and a month-end sale credited a month later.
_, x_inv = sale(customers[13], "tuan", D(2026, 7, 15), [("gach60", 30, None, None)])
x_ref = credit(x_inv, D(2026, 8, 2))
plant("mid-month-sale-credited", "O27", [x_inv.name, x_ref.name], "Hoá đơn giữa tháng 7 bị giảm trừ đầu tháng 8: không phải dồn doanh thu cuối tháng", kind="decoy")
_, y_inv = sale(customers[15], "lan", D(2026, 5, 30), [("cat", 40, None, None)])
y_ref = credit(y_inv, D(2026, 6, 25))
plant("month-end-sale-credited-late", "O27", [y_inv.name, y_ref.name], "Hoá đơn 30/5 bị giảm trừ ngày 25/6, hơn 15 ngày sau", kind="decoy")

# 19. Input VAT lost to cash: an 11-million bill paid in cash (Nghị định 181/2025, Điều 26).
tm_bill = bill(vendors[5], D(2026, 8, 18), "HD-TM-0818", 10_000_000, "Máy khoan, trả tiền mặt", by="binh", taxes=[PUR10])
pay(tm_bill, D(2026, 8, 18), journal=cash)
plant("bill-over-5m-paid-in-cash", "O28", [tm_bill.name], "Hoá đơn 11 triệu (gồm VAT) trả tiền mặt: 1 triệu VAT đầu vào không được khấu trừ")
ck_bill = bill(vendors[5], D(2026, 8, 25), "HD-CK-0825", 10_000_000, "Máy khoan, chuyển khoản", by="binh", taxes=[PUR10])
pay(ck_bill, D(2026, 8, 26))
plant("bill-over-5m-paid-by-bank", "O28", [ck_bill.name], "Hoá đơn 11 triệu chuyển khoản: hợp lệ", kind="decoy")
# ... three small bills of one seller on one day, 5,5 million together, paid in cash (Điều 26 khoản 3).
stationer = partner("Cửa hàng Văn phòng phẩm Thanh Khê", vat="9900000109", street="45 Điện Biên Phủ", city="Đà Nẵng", supplier_rank=1)
same_day = [bill(stationer, D(2026, 8, 20), "VPP-0820-%d" % i, amount, "Văn phòng phẩm", taxes=[PUR10]) for i, amount in
            enumerate([2_000_000, 2_000_000, 1_000_000], 1)]
pay(same_day[0] | same_day[1] | same_day[2], D(2026, 8, 20), journal=cash)
plant("same-day-bills-over-5m-in-cash", "O29", [b.name for b in same_day], "3 hoá đơn VPP cùng ngày 20/8, cộng 5,5 triệu, trả tiền mặt")
small_day = [bill(stationer, D(2026, 9, 10), "VPP-0910-%d" % i, 2_000_000, "Văn phòng phẩm", taxes=[PUR10]) for i in (1, 2)]
pay(small_day[0] | small_day[1], D(2026, 9, 10), journal=cash)
plant("same-day-bills-under-5m-in-cash", "O29", [b.name for b in small_day], "2 hoá đơn cùng ngày, cộng 4,4 triệu: dưới ngưỡng, hợp lệ", kind="decoy")
bank_day = [bill(stationer, D(2026, 9, 17), "VPP-0917-%d" % i, amount, "Văn phòng phẩm", taxes=[PUR10]) for i, amount in
            enumerate([2_000_000, 2_000_000, 1_000_000], 1)]
pay(bank_day[0] | bank_day[1] | bank_day[2], D(2026, 9, 17))
plant("same-day-bills-over-5m-by-bank", "O29", [b.name for b in bank_day], "3 hoá đơn cùng ngày cộng 5,5 triệu, chuyển khoản: hợp lệ", kind="decoy")

# A business writes its documents on their day: Odoo stamped every record with the moment this script ran,
# so each one's creation moves to its accounting date, and the backdated cases to when they were really made.
env.cr.execute("UPDATE account_move SET create_date = date + time '09:00' WHERE date IS NOT NULL")
for move_id, when in stamps.items():
    env.cr.execute("UPDATE account_move SET create_date = %s WHERE id = %s", (when, move_id))
env.invalidate_all()

env.cr.commit()
print("SIM sales=%d purchases=%d bills=%d payments=%d expenses=%d scraps=%d" % (
    env["sale.order"].search_count([]), env["purchase.order"].search_count([]),
    env["account.move"].search_count([("move_type", "=", "in_invoice")]), env["account.payment"].search_count([]),
    env["hr.expense"].search_count([]), env["stock.scrap"].search_count([])))
print("TRUTH=" + json.dumps({"from": str(START), "asOf": str(AS_OF), "cases": truth}, ensure_ascii=False))
