#!/usr/bin/env python3
"""Conformance evidence for UNVERIFIED action and wizard operations, against an independent oracle.

An action operation claims to do what its Odoo button does. For every UNVERIFIED METHOD_CALL and
WIZARD operation of the surface catalog, and every curated one --only names, this runner:

  * finds two records of the operation's model on which Odoo's form shows the button (its
    `invisible` expression, read from the surface inventory, is false) and which agree on every
    field that expression reads, and on the state, partner, company and kind of record (or, when
    no two records do, on the state and what the expression reads): twins, as far as the button
    and Odoo's most common branches can tell;
  * runs the operation on the first through the real standalone CLI;
  * does to the second what the web client does when the button is clicked, through code that
    shares nothing with the CLI's execution path: calls the method with the button's context, or
    opens the wizard and answers its dialogs the way the operation's catalog entry says it does
    (`catalog describe` -> providerCall), with the defaults the web client would load;
  * reads both records back and compares what changed.

AGREE means both succeeded and the same fields changed, with selection and boolean fields reaching
the same values (a transition, such as state draft -> sale) and relations gaining or losing the same
number of records (a credit note, a return). Other outcomes are never offered for
promotion:
  * AGREE_NO_CHANGE: nothing changed on either record.
  * AGREE_REFUSED: both refused.
  * AGREE_DIALOG: both answered with the same dialog.
  * DISAGREE: the two paths acted differently on twins that did not differ in those fields
    beforehand. It is evidence against the operation.
  * INCONCLUSIVE_TWINS_DIFFER: the difference was already there before either path acted.
  * NOT_RUN_*: the reason the operation was not run.

Where the lab's data holds no twins for an operation that matters, FIXTURES makes them: two fresh,
identical records, made through the oracle and driven to the state where the button shows.

Two kinds of operation are no form button and are compared by their own measure:
  * a chatter operation (a note, an activity: CHATTER) by what each path left on its record;
  * a guarded delete (DESTRUCTIVE_GUARDED) on two fresh records inside its provider domain, one
    deleted through the CLI and one unlinked as the web client's Delete does: AGREE when both are
    gone and the domain holds exactly two records fewer.

Operations whose modules reach a third party are never run here (Viettel SInvoice is
production-only).

The evidence holds statuses, field names, selection transitions, relation counts and hashes, and for
an oracle refusal the provider's message (160 characters). It holds no credential and no other
record content. Adding the AGREE results to a manifest is the reviewed step
scripts/dev/odoo-promote-conformance.py.

    ODOO_ORACLE_PASSWORD=... AERP_CREDENTIAL_LAB19DEMO=... scripts/dev/odoo-action-conformance.py \\
        --odoo-bin ./odoo --profile lab19demo --endpoint http://127.0.0.1:58718 --database lab19demo \\
        --login admin --series 19.0 --image-digest sha256:... --inventory inventory-19.0.json \\
        --evidence action-run.json
"""

import argparse
import ast
import datetime
import hashlib
import json
import os
import subprocess
import sys
import urllib.request

CANDIDATES = 400
STAMP = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
VOLATILE = {"write_date", "write_uid", "create_date", "create_uid", "__last_update", "display_name"}
VOLATILE_PREFIXES = ("message_", "activity_", "website_message", "rating_", "has_message", "my_activity")
COMPARED_TYPES = {"char", "text", "selection", "boolean", "integer", "float", "monetary", "date", "datetime", "many2one"}
# A relation is compared by how many records it holds, which is how an action that creates records
# shows on its source (a credit note in reversal_move_ids, a return in return_ids).
COUNTED_TYPES = {"one2many", "many2many"}
EXACT_TYPES = {"selection", "boolean"}
SCALAR_TYPES = COMPARED_TYPES | {"html"}
EXTERNAL_MODULES = {"l10n_vn_edi_viettel"}
TWIN_FIELDS = ("state", "partner_id", "company_id", "picking_type_id", "move_type", "journal_id", "type", "payment_mode")
MAX_DIALOGS = 4


def canonical_hash(value):
    return "sha256:" + hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class ProviderRefusal(Exception):
    pass


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.password = endpoint.rstrip("/"), database, password
        self.version = self._rpc("common", "version", [])
        self.uid = self._rpc("common", "login", [database, login, password])

    def _rpc(self, service, method, args):
        payload = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                              "params": {"service": service, "method": method, "args": args}}).encode()
        request = urllib.request.Request(self.endpoint + "/jsonrpc", data=payload, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=180) as response:
            answer = json.loads(response.read())
        if "error" in answer:
            raise ProviderRefusal((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        return answer.get("result")

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])


class Unsupported(Exception):
    pass


COMPARE = {
    ast.Eq: lambda a, b: a == b, ast.NotEq: lambda a, b: a != b,
    ast.In: lambda a, b: a in b, ast.NotIn: lambda a, b: a not in b,
    ast.Lt: lambda a, b: a < b, ast.LtE: lambda a, b: a <= b,
    ast.Gt: lambda a, b: a > b, ast.GtE: lambda a, b: a >= b,
}


def evaluate(node, record, uid):
    """Odoo's view expression language, for the literal, field and boolean forms buttons use."""
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.Name):
        if node.id == "uid":
            return uid
        if node.id in record:
            return record[node.id]
        raise Unsupported(node.id)
    if isinstance(node, ast.BoolOp):
        result = isinstance(node.op, ast.And)
        for value in node.values:
            result = evaluate(value, record, uid)
            if bool(result) != isinstance(node.op, ast.And):
                return result
        return result
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return not evaluate(node.operand, record, uid)
    if isinstance(node, ast.Compare):
        left = evaluate(node.left, record, uid)
        for operator, comparator in zip(node.ops, node.comparators):
            right = evaluate(comparator, record, uid)
            try:
                if not COMPARE[type(operator)](left, right):
                    return False
            except TypeError as error:
                raise Unsupported(str(error))
            left = right
        return True
    if isinstance(node, (ast.Tuple, ast.List)):
        return [evaluate(element, record, uid) for element in node.elts]
    raise Unsupported(type(node).__name__)


def expression_names(expression):
    tree = ast.parse(expression or "False", mode="eval")
    return {n.id for n in ast.walk(tree) if isinstance(n, ast.Name)} - {"True", "False", "None", "uid"}


def visible(expression, record, uid):
    if not (expression or "").strip():
        return True
    return not evaluate(ast.parse(expression, mode="eval").body, record, uid)


def literal_context(text, active):
    """A context given as source text: literals and the active record only, as the CLI does."""
    if not (text or "").strip():
        return {}
    # A button's context reads the record as `id`, as the web client evaluates it.
    names = {**active, "id": active.get("active_id")}

    def value(node):
        if isinstance(node, ast.Constant):
            return node.value
        if isinstance(node, ast.Name) and node.id in names:
            return names[node.id]
        if isinstance(node, ast.Name) and node.id in ("True", "False", "None"):
            return {"True": True, "False": False, "None": None}[node.id]
        if isinstance(node, (ast.List, ast.Tuple)):
            return [value(e) for e in node.elts]
        if isinstance(node, ast.Dict):
            return {value(k): value(v) for k, v in zip(node.keys, node.values)}
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub) and isinstance(node.operand, ast.Constant):
            return -node.operand.value
        raise Unsupported(f"context {text!r}")

    parsed = value(ast.parse(text, mode="eval").body)
    if not isinstance(parsed, dict):
        raise Unsupported(f"context {text!r}")
    return parsed


def normalized(meta, raw):
    if meta.get("type") == "many2one":
        return raw[0] if isinstance(raw, list) and raw else False
    if isinstance(raw, float):
        return round(raw, 6)
    return raw


def dialog_of(answer):
    if isinstance(answer, dict) and answer.get("type") == "ir.actions.act_window" and answer.get("target") == "new":
        return answer
    return None


def cli(binary, profile, *args):
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    completed = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=300)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT"}}


def cli_outcome(document):
    if document.get("status") == "OK":
        return "OK"
    error = document.get("error") or {}
    detail = error.get("details") or error.get("context") or {}
    if detail.get("dialog_model"):
        return "DIALOG:" + detail["dialog_model"]
    return "REFUSED:" + str(error.get("code"))


class WebClient:
    """What the web client does when a button is clicked, written independently of the CLI."""

    def __init__(self, oracle):
        self.oracle = oracle

    def defaults(self, model, context):
        """The values the web client loads into a new dialog: onchange with no values."""
        meta = self.oracle.call(model, "fields_get", [], {"attributes": ["type"]})
        spec = {name: {} for name, m in meta.items() if m["type"] in SCALAR_TYPES}
        answer = self.oracle.call(model, "onchange", [[], {}, [], spec], {"context": context}) or {}
        values = {}
        for name, value in (answer.get("value") or {}).items():
            if isinstance(value, dict):
                if value.get("id"):
                    values[name] = value["id"]
            elif name in spec and value not in (None,):
                values[name] = value
        return values

    def open(self, model, wizard, context, values):
        merged = {**self.defaults(wizard, context), **values}
        return self.oracle.call(wizard, "create", [merged], {"context": context})

    def click(self, model, target, call, button_context, samples):
        if call.get("method"):
            answer = self.oracle.call(model, call["method"], [[target]], {"context": button_context})
            dialog = dialog_of(answer)
            return "DIALOG:" + dialog.get("res_model", "") if dialog and dialog.get("res_model") not in FOLLOW_UP else "OK"
        active = {"active_model": model, "active_id": target, "active_ids": [target]}
        if not call.get("wizardOpenerMethod"):
            context = {**active, **(call.get("wizardContext") or {})}
            for key, binding in (call.get("wizardContextTarget") or {}).items():
                context[key] = [target] if binding == "ids" else target
            wizard_id = self.open(model, call["wizardModel"], context, {**(call.get("wizardValues") or {}), **samples})
            answer = self.oracle.call(call["wizardModel"], call["wizardMethod"], [[wizard_id]], {"context": context})
            dialog = dialog_of(answer)
            return "DIALOG:" + dialog.get("res_model", "") if dialog and dialog.get("res_model") not in FOLLOW_UP else "OK"
        answer = self.oracle.call(model, call["wizardOpenerMethod"], [[target]], {"context": button_context})
        answered = []
        while True:
            dialog = dialog_of(answer)
            if dialog is None:
                return "OK" if answered or call.get("wizardOpenerActs") else "NOT_OPENED"
            dialog_model = dialog.get("res_model", "")
            if dialog_model == call["wizardModel"]:
                button, values = call["wizardMethod"], {**(call.get("wizardValues") or {}), **samples}
            elif dialog_model in (call.get("wizardAlsoAnswers") or {}):
                button, values = call["wizardAlsoAnswers"][dialog_model], {}
            elif dialog_model in FOLLOW_UP and (answered or call.get("wizardOpenerActs")):
                return "OK"
            else:
                return "DIALOG:" + dialog_model
            if dialog_model in answered or len(answered) == MAX_DIALOGS:
                return "DIALOG:" + dialog_model
            given = dialog.get("context") or {}
            context = {**active, **(literal_context(given, active) if isinstance(given, str) else given)}
            if dialog.get("res_id"):
                wizard_id = dialog["res_id"]
                if values:
                    self.oracle.call(dialog_model, "write", [[wizard_id], values], {"context": context})
            else:
                wizard_id = self.open(model, dialog_model, context, values)
            answer = self.oracle.call(dialog_model, button, [[wizard_id]], {"context": context})
            answered.append(dialog_model)


class Fixtures:
    """Fresh twin records, made through the oracle, for operations whose state the lab's data does
    not hold twice: two identical records, driven to the state where the button shows."""

    def __init__(self, oracle):
        self.oracle = oracle

    def call(self, model, method, args, kwargs=None):
        return self.oracle.call(model, method, args, kwargs)

    def first(self, model, domain, fields=("id",)):
        found = self.call(model, "search_read", [domain], {"fields": list(fields), "limit": 1, "order": "id"})
        if not found:
            raise Unsupported(f"no {model} matching {domain}")
        return found[0]

    def twice(self, make):
        return [make(), make()]

    def payments(self, state):
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        journal = self.first("account.journal", [["type", "=", "bank"]])["id"]

        def make():
            payment = self.call("account.payment", "create", [{"payment_type": "inbound", "partner_type": "customer",
                                                               "partner_id": partner, "amount": 100.0, "journal_id": journal}])
            if state == "posted":
                self.call("account.payment", "action_post", [[payment]])
            return payment
        return self.twice(make)

    def purchases(self, locked):
        partner = self.first("res.partner", [["supplier_rank", ">", 0]])["id"]
        product = self.first("product.product", [["purchase_ok", "=", True], ["is_storable", "=", True]])["id"]

        def make():
            order = self.call("purchase.order", "create", [{"partner_id": partner, "order_line": [[0, 0, {
                "product_id": product, "product_qty": 1, "price_unit": 10.0}]]}])
            self.call("purchase.order", "button_confirm", [[order]])
            if locked:
                self.call("purchase.order", "button_lock", [[order]])
            return order
        return self.twice(make)

    def sales(self, locked):
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        product = self.first("product.product", [["sale_ok", "=", True], ["type", "=", "service"]])["id"]

        def make():
            order = self.call("sale.order", "create", [{"partner_id": partner, "order_line": [[0, 0, {
                "product_id": product, "product_uom_qty": 1}]]}])
            self.call("sale.order", "action_confirm", [[order]])
            if locked:
                self.call("sale.order", "action_lock", [[order]])
            return order
        return self.twice(make)

    def service(self, policy="order"):
        return self.first("product.product", [["sale_ok", "=", True], ["type", "=", "service"],
                                              ["invoice_policy", "=", policy]])["id"]

    def quotations(self):
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        product = self.service()
        return self.twice(lambda: self.call("sale.order", "create", [{"partner_id": partner, "order_line": [[0, 0, {
            "product_id": product, "product_uom_qty": 1}]]}]))

    def invoiceable_sales(self):
        orders = self.quotations()
        self.call("sale.order", "action_confirm", [orders])
        return orders

    def rfqs(self):
        partner = self.first("res.partner", [["supplier_rank", ">", 0]])["id"]
        product = self.first("product.product", [["purchase_ok", "=", True], ["is_storable", "=", True]])["id"]
        return self.twice(lambda: self.call("purchase.order", "create", [{"partner_id": partner, "order_line": [[0, 0, {
            "product_id": product, "product_qty": 1, "price_unit": 10.0}]]}]))

    def opportunities(self):
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        return self.twice(lambda: self.call("crm.lead", "create", [{"name": f"conformance {STAMP}", "type": "opportunity",
                                                                     "partner_id": partner, "expected_revenue": 1000.0}]))

    def billable_purchases(self):
        """Confirmed orders of a product billed on ordered quantities: a bill can be made at once."""
        partner = self.first("res.partner", [["supplier_rank", ">", 0]])["id"]
        product = self.first("product.product", [["purchase_ok", "=", True], ["purchase_method", "=", "purchase"]])["id"]

        def make():
            order = self.call("purchase.order", "create", [{"partner_id": partner, "order_line": [[0, 0, {
                "product_id": product, "product_qty": 1, "price_unit": 10.0}]]}])
            self.call("purchase.order", "button_confirm", [[order]])
            return order
        return self.twice(make)

    def customer_invoices(self, post):
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        product = self.service()

        def make():
            invoice = self.call("account.move", "create", [{"move_type": "out_invoice", "partner_id": partner, "invoice_line_ids": [[0, 0, {
                "product_id": product, "quantity": 1, "price_unit": 100.0}]]}])
            if post:
                self.call("account.move", "action_post", [[invoice]])
            return invoice
        return self.twice(make)

    def journal_entries(self, post):
        """Miscellaneous entries of 100 between an expense and a current asset account. Posted twins go
        to two general journals: resetting an entry that is not the last of its sequence flags a gap
        (made_sequence_gap), so each must be the last of a sequence of its own."""
        journals = self.call("account.journal", "search", [[["type", "=", "general"]]], {"limit": 2, "order": "id"})
        if len(journals) < (2 if post else 1):
            raise Unsupported("fewer than two general journals")
        expense = self.first("account.account", [["account_type", "=", "expense"]])["id"]
        asset = self.first("account.account", [["account_type", "=", "asset_current"]])["id"]

        def make(journal):
            entry = self.call("account.move", "create", [{"move_type": "entry", "journal_id": journal, "line_ids": [
                [0, 0, {"account_id": expense, "name": f"Conformance {STAMP}", "debit": 100.0, "credit": 0.0}],
                [0, 0, {"account_id": asset, "name": f"Conformance {STAMP}", "debit": 0.0, "credit": 100.0}]]}])
            if post:
                self.call("account.move", "action_post", [[entry]])
            return entry
        return [make(journals[0]), make(journals[1] if post else journals[0])]

    def counted_quants(self):
        """Two stocked quants counted at exactly what is on hand: applying the count moves nothing."""
        quants = self.call("stock.quant", "search_read", [[["location_id.usage", "=", "internal"], ["quantity", ">", 0],
                                                          ["lot_id", "=", False], ["inventory_quantity_set", "=", False]]],
                           {"fields": ["id", "quantity"], "limit": 2, "order": "id"})
        if len(quants) < 2:
            raise Unsupported("fewer than two uncounted stocked quants")
        for quant in quants:
            self.call("stock.quant", "write", [[quant["id"]], {"inventory_quantity": quant["quantity"], "inventory_quantity_set": True}],
                      {"context": {"inventory_mode": True}})
        return [quant["id"] for quant in quants]

    def full_receipts(self):
        """Receipts of 10 with all 10 picked: Validate needs no dialog."""
        kind = self.first("stock.picking.type", [["code", "=", "incoming"]], ("id", "default_location_src_id", "default_location_dest_id"))
        product = self.first("product.product", [["is_storable", "=", True], ["tracking", "=", "none"]], ("id", "uom_id"))
        source, destination = kind["default_location_src_id"][0], kind["default_location_dest_id"][0]

        def make():
            picking = self.call("stock.picking", "create", [{"picking_type_id": kind["id"], "location_id": source, "location_dest_id": destination,
                                                             "move_ids": [[0, 0, {"product_id": product["id"], "product_uom_qty": 10,
                                                                                  "product_uom": product["uom_id"][0], "location_id": source,
                                                                                  "location_dest_id": destination}]]}])
            self.call("stock.picking", "action_confirm", [[picking]])
            move = self.first("stock.move", [["picking_id", "=", picking]])["id"]
            self.call("stock.move", "write", [[move], {"quantity": 10, "picked": True}])
            return picking
        return self.twice(make)

    def unreserved_deliveries(self):
        """Deliveries of 1 of a product with stock, confirmed and then unreserved: Check Availability
        reserves them again, whatever the lab's other deliveries already hold."""
        kind = self.first("stock.picking.type", [["code", "=", "outgoing"]], ("id", "default_location_src_id", "default_location_dest_id"))
        source, destination = kind["default_location_src_id"][0], kind["default_location_dest_id"][0]
        quant = self.first("stock.quant", [["location_id", "=", source], ["quantity", ">=", 10], ["lot_id", "=", False],
                                           ["product_id.tracking", "=", "none"]], ("product_id",))
        product = self.call("product.product", "read", [[quant["product_id"][0]], ["uom_id"]])[0]

        def make():
            picking = self.call("stock.picking", "create", [{"picking_type_id": kind["id"], "location_id": source, "location_dest_id": destination,
                                                             "move_ids": [[0, 0, {"product_id": product["id"], "product_uom_qty": 1,
                                                                                  "product_uom": product["uom_id"][0], "location_id": source,
                                                                                  "location_dest_id": destination}]]}])
            self.call("stock.picking", "action_confirm", [[picking]])
            self.call("stock.picking", "do_unreserve", [[picking]])
            return picking
        return self.twice(make)

    def partial_receipts(self):
        kind = self.first("stock.picking.type", [["code", "=", "incoming"]], ("id", "default_location_src_id", "default_location_dest_id"))
        product = self.first("product.product", [["is_storable", "=", True], ["tracking", "=", "none"]], ("id", "uom_id"))
        source, destination = kind["default_location_src_id"][0], kind["default_location_dest_id"][0]

        def make():
            picking = self.call("stock.picking", "create", [{"picking_type_id": kind["id"], "location_id": source, "location_dest_id": destination,
                                                             "move_ids": [[0, 0, {"product_id": product["id"], "product_uom_qty": 10,
                                                                                  "product_uom": product["uom_id"][0], "location_id": source,
                                                                                  "location_dest_id": destination}]]}])
            self.call("stock.picking", "action_confirm", [[picking]])
            move = self.first("stock.move", [["picking_id", "=", picking]])["id"]
            self.call("stock.move", "write", [[move], {"quantity": 4, "picked": True}])
            return picking
        return self.twice(make)

    def partial_orders(self, consistent, produced=2):
        """Manufacturing orders of 5 with `produced` made. Consistent: the components consumed match
        the bill of materials, so the only question is the backorder; otherwise none are consumed,
        so Odoo first asks about the consumption."""
        bom = self.first("mrp.bom", [["type", "=", "normal"], ["product_tmpl_id.tracking", "=", "none"]], ("id", "product_tmpl_id"))
        variant = self.first("product.product", [["product_tmpl_id", "=", bom["product_tmpl_id"][0]]])["id"]

        def make():
            order = self.call("mrp.production", "create", [{"product_id": variant, "bom_id": bom["id"], "product_qty": 5}])
            self.call("mrp.production", "action_confirm", [[order]])
            self.call("mrp.production", "write", [[order], {"qty_producing": produced}])
            if consistent:
                for move in self.call("stock.move", "search_read", [[["raw_material_production_id", "=", order]]], {"fields": ["product_uom_qty"]}):
                    self.call("stock.move", "write", [[move["id"]], {"quantity": move["product_uom_qty"] * produced / 5, "picked": True}])
            return order
        return self.twice(make)

    def unstocked_scraps(self):
        location = self.first("stock.warehouse", [], ("lot_stock_id",))["lot_stock_id"][0]
        product = self.call("product.product", "create", [{"name": f"conformance scrap {STAMP}", "type": "consu", "is_storable": True}])
        return self.twice(lambda: self.call("stock.scrap", "create", [{"product_id": product, "scrap_qty": 5, "location_id": location}]))

    def own_future_leaves(self):
        employee = self.first("hr.employee", [["user_id", "=", self.oracle.uid]])["id"]
        kind = self.first("hr.leave.type", [["requires_allocation", "=", False]])["id"]
        days = iter(range(60, 400))

        def make():
            while True:
                day = datetime.date.today() + datetime.timedelta(days=next(days))
                if day.weekday() < 5:
                    break
            leave = self.call("hr.leave", "create", [{"employee_id": employee, "holiday_status_id": kind,
                                                      "request_date_from": str(day), "request_date_to": str(day)}])
            for method in ("action_approve", "action_validate"):
                if self.call("hr.leave", "read", [[leave], ["state"]])[0]["state"] != "validate":
                    try:
                        self.call("hr.leave", method, [[leave]])
                    except ProviderRefusal:
                        pass
            return leave
        return self.twice(make)

    def workorders(self, started):
        """The work order of two fresh, identical manufacturing orders of a fresh product, made on a
        work center that is not blocked (the demo blocks the one its bills of materials use), and
        started when the button acts on a running work order (pause, finish)."""
        center = self.first("mrp.workcenter", [["working_state", "!=", "blocked"]])["id"]
        template = self.call("product.template", "create", [{"name": f"Conformance work order {STAMP}", "type": "consu"}])
        product = self.call("product.product", "search", [[["product_tmpl_id", "=", template]]])[0]
        bom = self.call("mrp.bom", "create", [{"product_tmpl_id": template, "product_qty": 1, "type": "normal", "operation_ids": [
            [0, 0, {"name": f"Conformance operation {STAMP}", "workcenter_id": center, "time_cycle_manual": 10}]]}])

        def make():
            order = self.call("mrp.production", "create", [{"product_id": product, "bom_id": bom, "product_qty": 1}])
            self.call("mrp.production", "action_confirm", [[order]])
            found = self.call("mrp.workorder", "search", [[["production_id", "=", order]]], {"order": "id", "limit": 1})
            if not found:
                raise Unsupported("the bill of materials made no work order")
            return found[0]
        twins = self.twice(make)
        if started:
            self.call("mrp.workorder", "button_start", [twins])
        return twins

    def agreements(self, state):
        """Two blanket orders of one line, driven to state: draft, confirmed or cancel."""
        vendor = self.first("res.partner", [["supplier_rank", ">", 0]])["id"]
        product = self.first("product.product", [["purchase_ok", "=", True], ["type", "=", "consu"]])["id"]

        def make():
            agreement = self.call("purchase.requisition", "create", [{"vendor_id": vendor, "requisition_type": "blanket_order",
                                                                       "line_ids": [[0, 0, {"product_id": product, "product_qty": 5, "price_unit": 10.0}]]}])
            if state in ("confirmed", "cancel"):
                self.call("purchase.requisition", "action_confirm", [[agreement]])
            if state == "cancel":
                self.call("purchase.requisition", "action_cancel", [[agreement]])
            return agreement
        return self.twice(make)

    def repairs(self, state, part=None):
        """Two repair orders of a product for a customer, confirmed or under repair; with one storable
        part added, reserved or released, when the button acts on the parts. The product repaired is
        not stocked: a stocked one without stock answers Confirm with the insufficient-quantity
        dialog and stays a draft."""
        partner = self.first("res.partner", [["customer_rank", ">", 0]])["id"]
        product = self.first("product.product", [["type", "=", "consu"], ["is_storable", "=", False]])["id"]
        stocked = None
        if part:
            kind = self.first("stock.picking.type", [["code", "=", "repair_operation"]], ("id", "default_location_src_id"))
            stocked = self.first("stock.quant", [["location_id", "=", kind["default_location_src_id"][0]], ["quantity", ">=", 10],
                                                 ["lot_id", "=", False], ["product_id.tracking", "=", "none"]], ("product_id",))["product_id"][0]

        def make():
            values = {"partner_id": partner, "product_id": product}
            if stocked:
                values["move_ids"] = [[0, 0, {"product_id": stocked, "product_uom_qty": 1, "repair_line_type": "add"}]]
            repair = self.call("repair.order", "create", [values])
            self.call("repair.order", "action_validate", [[repair]])
            if part == "reserved":
                self.call("repair.order", "action_assign", [[repair]])
            if part == "released":
                self.call("repair.order", "action_unreserve", [[repair]])
            if state == "under_repair":
                self.call("repair.order", "action_repair_start", [[repair]])
            return repair
        return self.twice(make)

    def batches(self, confirmed, reserved):
        """Two batch transfers of one delivery each (unreserved_deliveries): draft, or in progress with
        the delivery released or reserved."""
        twins = []
        for picking in self.unreserved_deliveries():
            if reserved:
                self.call("stock.picking", "action_assign", [[picking]])
            batch = self.call("stock.picking.batch", "create", [{"picking_ids": [[6, 0, [picking]]]}])
            if confirmed:
                self.call("stock.picking.batch", "action_confirm", [[batch]])
            twins.append(batch)
        return twins

    def draft_registrations(self):
        """Two registrations of one event, left unconfirmed (draft)."""
        event = self.first("event.event", [["date_end", ">", datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")]])["id"]

        def make():
            registration = self.call("event.registration", "create", [{"event_id": event, "name": f"Conformance attendee {STAMP}",
                                                                         "email": "attendee@example.com"}])
            self.call("event.registration", "write", [[registration], {"state": "draft"}])
            return registration
        return self.twice(make)

    def vehicles_changing_driver(self):
        """Two fresh vehicles of one model whose next drivers are set: two different people, because
        accepting a change takes the new driver off every other vehicle they drive."""
        model = self.first("fleet.vehicle.model", [])["id"]
        drivers = self.call("res.partner", "search", [[["is_company", "=", False]]], {"limit": 2, "order": "id"})
        if len(drivers) < 2:
            raise Unsupported("fewer than two people to drive")
        return [self.call("fleet.vehicle", "create", [{"model_id": model, "future_driver_id": driver,
                                                        "license_plate": f"CF-{STAMP[-6:]}"}]) for driver in drivers]

    def live_surveys(self, started):
        """Two live-session surveys of one question, their session started when the button ends it."""
        def make():
            survey = self.call("survey.survey", "create", [{"title": f"Conformance live {STAMP}", "survey_type": "live_session",
                                                            "question_ids": [[0, 0, {"title": "Conformance question", "question_type": "text_box"}]]}])
            if started:
                self.call("survey.survey", "action_start_session", [[survey]])
            return survey
        return self.twice(make)

    def reserved_deliveries(self):
        """Deliveries of 1 of a product with stock, reserved: Unreserve has something to release."""
        twins = self.unreserved_deliveries()
        self.call("stock.picking", "action_assign", [twins])
        return twins

    def reorder_rules(self, manual_quantity=False):
        """Two manual reorder rules, each on a fresh storable product bought from one vendor and short
        of its minimum, so that Order Once has something to order; with a quantity set by hand when
        the button clears it."""
        vendor = self.first("res.partner", [["supplier_rank", ">", 0]])["id"]
        warehouse = self.first("stock.warehouse", [], ("id", "lot_stock_id"))
        buy = self.first("stock.route", [["name", "=", "Buy"]])["id"]

        def make():
            template = self.call("product.template", "create", [{
                "name": f"Conformance reorder {STAMP}", "type": "consu", "is_storable": True, "purchase_ok": True,
                "seller_ids": [[0, 0, {"partner_id": vendor, "price": 10.0, "min_qty": 1}]]}])
            product = self.call("product.product", "search", [[["product_tmpl_id", "=", template]]])[0]
            rule = self.call("stock.warehouse.orderpoint", "create", [{
                "product_id": product, "warehouse_id": warehouse["id"], "location_id": warehouse["lot_stock_id"][0],
                "route_id": buy, "product_min_qty": 5, "product_max_qty": 10, "trigger": "manual"}])
            if manual_quantity:
                self.call("stock.warehouse.orderpoint", "write", [[rule], {"qty_to_order_manual": 3}])
            return rule
        return self.twice(make)

    def orders_with_operations(self, planned):
        bom = self.first("mrp.bom", [["type", "=", "normal"], ["operation_ids", "!=", False], ["product_tmpl_id.tracking", "=", "none"]],
                         ("id", "product_tmpl_id"))
        variant = self.first("product.product", [["product_tmpl_id", "=", bom["product_tmpl_id"][0]]])["id"]

        def make():
            order = self.call("mrp.production", "create", [{"product_id": variant, "bom_id": bom["id"], "product_qty": 1}])
            self.call("mrp.production", "action_confirm", [[order]])
            if planned:
                self.call("mrp.production", "button_plan", [[order]])
            return order
        return self.twice(make)


# Operations whose twins are made rather than found.
# The method a curated wizard's button calls to open it, where the web client opens it that way
# (Register Payment on an invoice answers with the account.payment.register dialog).
CURATED_OPENERS = {"finance.payment.register": "action_register_payment"}

# A curated operation whose web-client path is not a form button calling its own method: the path the
# web client takes for the same business action, which the oracle takes. "list" marks a header button
# of a list view, which acts on the selected records whatever their state.
CURATED_BUTTONS = {
    # Won calls action_set_won_rainbowman: action_set_won, then the rainbow man.
    "crm.opportunity.win": {"call": {"method": "action_set_won_rainbowman"}},
    # Lost opens the Mark Lost dialog, whose button calls action_set_lost.
    "crm.opportunity.lose": {"call": {"wizardModel": "crm.lead.lost", "wizardMethod": "action_lost_reason_apply",
                                      "wizardContextTarget": {"default_lead_ids": "ids"}}},
    # Create Bills is a header button of the purchase list (purchase_order_view_tree).
    "procurement.bill.create": {"call": {"method": "action_create_invoice"}, "list": True},
    # Apply and Clear are row buttons of the inventory count list (view_stock_quant_tree_inventory_editable).
    "inventory.count.apply": {"call": {"method": "action_apply_inventory"}, "list": True},
    "inventory.count.clear": {"call": {"method": "action_clear_inventory_quantity"}, "list": True},
}

FIXTURES = {
    # The curated money flows, on fresh identical records: the lab's own twins differ in what these
    # buttons branch on (alternative RFQs, loyalty rewards, journal entries next to invoices).
    "sales.order.confirm": lambda f: f.quotations(),
    "sales.invoice.create": lambda f: f.invoiceable_sales(),
    "procurement.order.confirm": lambda f: f.rfqs(),
    "finance.invoice.post": lambda f: f.customer_invoices(post=False),
    "finance.payment.register": lambda f: f.customer_invoices(post=True),
    "inventory.transfer.validate": lambda f: f.full_receipts(),
    "crm.opportunity.win": lambda f: f.opportunities(),
    "crm.opportunity.lose": lambda f: f.opportunities(),
    "procurement.bill.create": lambda f: f.billable_purchases(),
    "finance.payment.post": lambda f: f.payments("draft"),
    "finance.payment.cancel": lambda f: f.payments("draft"),
    "finance.payment.draft": lambda f: f.payments("posted"),
    "finance.payment.validate": lambda f: f.payments("posted"),
    "finance.payment.reject": lambda f: f.payments("posted"),
    "finance.payment.mark_as_sent": lambda f: f.payments("posted"),
    "procurement.order.lock": lambda f: f.purchases(locked=False),
    "procurement.order.unlock": lambda f: f.purchases(locked=True),
    "sales.order.unlock": lambda f: f.sales(locked=True),
    "inventory.transfer.validate_with_backorder": lambda f: f.partial_receipts(),
    "inventory.transfer.validate_without_backorder": lambda f: f.partial_receipts(),
    "manufacturing.order.mark_done_with_backorder": lambda f: f.partial_orders(consistent=True),
    "manufacturing.order.mark_done_without_backorder": lambda f: f.partial_orders(consistent=True),
    "manufacturing.order.mark_done_confirm_consumption": lambda f: f.partial_orders(consistent=False, produced=5),
    "manufacturing.order.mark_done_confirm_consumption_with_backorder": lambda f: f.partial_orders(consistent=False),
    "manufacturing.order.mark_done_confirm_consumption_without_backorder": lambda f: f.partial_orders(consistent=False),
    "inventory.scrap.validate_despite_shortage": lambda f: f.unstocked_scraps(),
    "workforce.leave.cancel": lambda f: f.own_future_leaves(),
    "manufacturing.order.plan": lambda f: f.orders_with_operations(planned=False),
    "manufacturing.order.unplan": lambda f: f.orders_with_operations(planned=True),
    # Check Availability on deliveries that hold nothing yet and can (the lab's are reserved already).
    "inventory.transfer.reserve": lambda f: f.unreserved_deliveries(),
    # Journal entries (move_type entry): the lab's are posted invoices' siblings, not twins.
    "finance.journal_entry.post": lambda f: f.journal_entries(post=False),
    "finance.journal_entry.reset_to_draft": lambda f: f.journal_entries(post=True),
    # A count is made in the inventory list, never stored twice by the demo data.
    "inventory.count.apply": lambda f: f.counted_quants(),
    "inventory.count.clear": lambda f: f.counted_quants(),
    # List buttons: work orders of two fresh identical manufacturing orders, and reorder rules short
    # of their minimum (the demo's own differ in product, vendor and stock).
    "inventory.transfer.unreserve": lambda f: f.reserved_deliveries(),
    # The parked apps (2026-10-11): blanket orders, repairs, batch transfers, event registrations,
    # vehicles and live surveys in the states their buttons show in.
    "procurement.agreement.confirm": lambda f: f.agreements("draft"),
    "procurement.agreement.done": lambda f: f.agreements("confirmed"),
    "procurement.agreement.cancel": lambda f: f.agreements("confirmed"),
    "procurement.agreement.draft": lambda f: f.agreements("cancel"),
    "repair.order.repair_start": lambda f: f.repairs("confirmed"),
    "repair.order.repair_end": lambda f: f.repairs("under_repair"),
    "repair.order.assign": lambda f: f.repairs("confirmed", part="released"),
    "repair.order.unreserve": lambda f: f.repairs("confirmed", part="reserved"),
    "inventory.batch_transfer.confirm": lambda f: f.batches(confirmed=False, reserved=False),
    "inventory.batch_transfer.assign": lambda f: f.batches(confirmed=True, reserved=False),
    "inventory.batch_transfer.done": lambda f: f.batches(confirmed=True, reserved=True),
    "event.registration.confirm": lambda f: f.draft_registrations(),
    "fleet.vehicle.accept_driver_change": lambda f: f.vehicles_changing_driver(),
    "survey.survey.start_session": lambda f: f.live_surveys(started=False),
    "survey.survey.end_session": lambda f: f.live_surveys(started=True),
    "manufacturing.workorder.start": lambda f: f.workorders(started=False),
    "manufacturing.workorder.pause": lambda f: f.workorders(started=True),
    "manufacturing.workorder.finish": lambda f: f.workorders(started=True),
    "inventory.reorder_rule.replenish": lambda f: f.reorder_rules(),
    "inventory.reorder_rule.automate": lambda f: f.reorder_rules(),
    "inventory.reorder_rule.clear_manual_quantity": lambda f: f.reorder_rules(manual_quantity=True),
}


# What the chatter does, which no form button stands for: Log note posts a note, Schedule Activity
# saves an activity from its dialog. The oracle does that on the second twin; what each path left on
# its record (one new note or activity) is compared field by field.
CHATTER = {
    "message_post": {
        "inputs": {"body": f"Conformance note {STAMP}"},
        "left": ("mail.message", lambda model, rid, ctx: [["model", "=", model], ["res_id", "=", rid],
                                                         ["message_type", "=", "comment"], ["subtype_id", "=", ctx["note"]]]),
        "compared": ["message_type", "subtype_id", "body", "author_id"],
    },
    "activity_schedule": {
        "inputs": {"summary": f"Conformance activity {STAMP}",
                   "date_deadline": (datetime.date.today() + datetime.timedelta(days=7)).isoformat()},
        "left": ("mail.activity", lambda model, rid, ctx: [["res_model", "=", model], ["res_id", "=", rid]]),
        "compared": ["activity_type_id", "summary", "user_id", "date_deadline"],
    },
}


def chatter(oracle, options, operation_id, described, model, spec):
    """A chatter operation on twins: the CLI on one, what the chatter does on the other."""
    ids = oracle.call(model, "search", [described.get("providerDomain") or []], {"limit": 2, "order": "id desc"})
    if len(ids) < 2:
        return {"status": "NOT_RUN_NO_TWINS"}
    cli_target, oracle_target = ids
    note = oracle.call("ir.model.data", "check_object_reference", ["mail", "mt_note"])[1]
    todo = oracle.call("ir.model.data", "check_object_reference", ["mail", "mail_activity_data_todo"])[1]
    left_model, left_domain = spec["left"]
    ctx = {"note": note}

    def left(rid):
        return oracle.call(left_model, "search", [left_domain(model, rid, ctx)], {"order": "id"})

    before = {rid: left(rid) for rid in ids}
    args = ["execute", "--operation", operation_id, "--provider-target-id", str(cli_target), "--allow-unverified"]
    for name, value in sorted(spec["inputs"].items()):
        args += ["--set", f"{name}={value}"]
    by_cli = cli_outcome(cli(options.odoo_bin, options.profile, *args))
    try:
        if left_model == "mail.message":
            # The chatter's Log note: a comment with the note subtype, as /mail/message/post posts it.
            oracle.call(model, "message_post", [[oracle_target]], {"body": spec["inputs"]["body"], "message_type": "comment",
                                                                 "subtype_xmlid": "mail.mt_note"})
        else:
            # The Schedule Activity dialog, saved with the To-Do type, the summary and the due date entered.
            model_id = oracle.call("ir.model", "search", [[["model", "=", model]]])[0]
            oracle.call("mail.activity", "create", [{"res_model_id": model_id, "res_id": oracle_target, "activity_type_id": todo,
                                                     "summary": spec["inputs"]["summary"], "date_deadline": spec["inputs"]["date_deadline"]}])
        by_oracle = "OK"
    except ProviderRefusal as error:
        return {"status": "DISAGREE" if by_cli == "OK" else "AGREE_REFUSED", "cliOutcome": by_cli,
                "oracleOutcome": "REFUSED", "oracleRefusal": str(error)[:160]}
    new = {rid: [i for i in left(rid) if i not in before[rid]] for rid in ids}
    outcome = {"kind": "chatter", "cliOutcome": by_cli, "oracleOutcome": by_oracle, "pairHash": canonical_hash(ids),
               "twinBasis": "chatter", "comparedFields": spec["compared"],
               "countChanges": {left_model: len(new[cli_target])}, "oracleCountChanges": {left_model: len(new[oracle_target])}}
    if by_cli != "OK" or len(new[cli_target]) != 1 or len(new[oracle_target]) != 1:
        return {**outcome, "status": "DISAGREE"}
    left_cli = oracle.call(left_model, "read", [new[cli_target], spec["compared"]])[0]
    left_oracle = oracle.call(left_model, "read", [new[oracle_target], spec["compared"]])[0]
    differing = [f for f in spec["compared"] if left_cli.get(f) != left_oracle.get(f)]
    return {**outcome, "differingFields": differing, "leftHash": canonical_hash({f: left_cli.get(f) for f in spec["compared"]}),
            "status": "DISAGREE" if differing else "AGREE"}


def fresh(oracle, model, domain, index):
    """A new record with the smallest values the provider accepts, inside the operation's domain:
    every required field without a default, and each `=` or `=like` leaf of the domain."""
    meta = oracle.call(model, "fields_get", [], {"attributes": ["type", "required", "store", "relation", "selection"]})
    defaults = {k: v for k, v in oracle.call(model, "default_get", [sorted(meta)]).items() if v not in (False, None, [], "")}
    values = {}
    for leaf in domain:
        if not isinstance(leaf, list) or len(leaf) != 3 or "." in leaf[0]:
            raise Unsupported(f"domain leaf {leaf}")
        name, operator, value = leaf
        if operator == "=":
            values[name] = value
        elif operator == "=like" and isinstance(value, str) and value.endswith("%"):
            values[name] = f"{value[:-1]}conformance-delete-{STAMP}-{index}.txt"
        else:
            raise Unsupported(f"domain leaf {leaf}")
    for name, field in sorted(meta.items()):
        if not field.get("required") or not field.get("store") or name in defaults or name in values:
            continue
        # A record makes its own mail alias (mail.alias.mixin); borrowing another record's would tie the two.
        if field.get("relation") == "mail.alias":
            continue
        kind = field["type"]
        if kind in ("char", "text"):
            values[name] = f"Conformance delete {STAMP} {index}"
        elif kind == "many2one":
            found = oracle.call(field["relation"], "search", [[]], {"limit": 1, "order": "id"})
            if not found:
                raise Unsupported(f"no {field['relation']} for {name}")
            values[name] = found[0]
        elif kind == "selection" and field.get("selection"):
            values[name] = field["selection"][0][0]
        elif kind in ("integer", "float", "monetary"):
            values[name] = 1
        elif kind == "boolean":
            values[name] = True
        else:
            raise Unsupported(f"required {kind} field {name}")
    return oracle.call(model, "create", [values])


def deletion(oracle, options, operation_id, described, model):
    """A guarded delete on twins: two fresh records inside the operation's domain, one deleted
    through the CLI, the other the way the web client's Delete does (unlink)."""
    domain = described.get("providerDomain") or []
    try:
        twins = [fresh(oracle, model, domain, index) for index in (1, 2)]
    except (Unsupported, ProviderRefusal) as error:
        return {"status": "NOT_RUN_NO_TWINS", "detail": str(error)[:160]}
    everything = {"context": {"active_test": False}}

    def present(record_id):
        return oracle.call(model, "search_count", [domain + [["id", "=", record_id]]], everything)

    if not all(present(record_id) for record_id in twins):
        return {"status": "NOT_RUN_FIXTURE_NOT_APPLICABLE", "detail": "the made records are outside the domain"}
    before = oracle.call(model, "search_count", [domain], everything)
    cli_target, oracle_target = twins
    by_cli = cli_outcome(cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                             "--provider-target-id", str(cli_target), "--confirm-impact", "--allow-unverified"))
    try:
        oracle.call(model, "unlink", [[oracle_target]])
        by_oracle = "OK"
    except ProviderRefusal as error:
        return {"status": "DISAGREE" if by_cli == "OK" else "AGREE_REFUSED", "cliOutcome": by_cli,
                "oracleOutcome": "REFUSED", "oracleRefusal": str(error)[:160]}
    after = oracle.call(model, "search_count", [domain], everything)
    gone = {"cli": not present(cli_target), "oracle": not present(oracle_target)}
    outcome = {"kind": "delete", "cliOutcome": by_cli, "oracleOutcome": by_oracle, "pairHash": canonical_hash(twins),
               "twinBasis": "fresh", "removed": gone, "countChange": after - before}
    agree = by_cli == "OK" and gone["cli"] and gone["oracle"] and after - before == -2
    return {**outcome, "status": "AGREE" if agree else "DISAGREE"}


# Dialogs Odoo offers after acting (packages/odoo/provider_dialog.go followUpDialogs).
FOLLOW_UP = {"account.autopost.bills.wizard"}


def samples_for(oracle, call, inputs):
    """A value for every text input the operation takes (a reason, a note), the same on both twins.
    Other inputs are left to the defaults the dialog loads."""
    if not call.get("wizardModel") or not inputs:
        return {}
    meta = oracle.call(call["wizardModel"], "fields_get", [], {"attributes": ["type"]})
    return {item["name"]: f"conformance {STAMP}" for item in inputs
            if meta.get(item["name"], {}).get("type") in ("char", "text")}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for name in ("--odoo-bin", "--profile", "--endpoint", "--database", "--login", "--series", "--image-digest", "--inventory", "--evidence"):
        parser.add_argument(name, required=True)
    parser.add_argument("--password-env", default="ODOO_ORACLE_PASSWORD")
    parser.add_argument("--only", help="comma-separated operation ids")
    parser.add_argument("--readiness", default="UNVERIFIED", choices=["UNVERIFIED", "ANY"],
                        help="which operations to run: ANY also re-checks promoted ones (another protocol, another lab)")
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")

    oracle = Oracle(options.endpoint, options.database, options.login, password)
    if oracle.version.get("server_serie") != options.series:
        raise SystemExit(f"the provider runs {oracle.version.get('server_serie')}, not {options.series}")
    web = WebClient(oracle)
    inventory = json.load(open(options.inventory, encoding="utf-8"))
    # The buttons an operation can stand for: the form's, then the list's (the generator takes both;
    # a list button acts on the selected records as a form button acts on the open one).
    buttons_of = {m["model"]: (m.get("views", {}).get("form", {}).get("buttons", [])
                               + m.get("views", {}).get("list", {}).get("buttons", []))
                  for m in inventory["models"] if "error" not in m}
    installed = {r["name"] for r in oracle.call("ir.module.module", "search_read", [[["state", "=", "installed"]]], {"fields": ["name"]})}

    # Surface operations by default. A curated operation runs only when --only names it: the curated
    # money flows (sales.order.confirm, finance.invoice.post, ...) carry historical evidence, and a run
    # on twins is how they gain current evidence.
    only = set(options.only.split(",")) if options.only else set()
    operation_ids = []
    for strategy in ("METHOD_CALL", "WIZARD", "DESTRUCTIVE_GUARDED"):
        selection = ["--readiness", "UNVERIFIED"] if options.readiness == "UNVERIFIED" else []
        listing = cli(options.odoo_bin, options.profile, "catalog", "list", "--strategy", strategy, *selection)
        operation_ids += [item["id"] for item in (listing.get("data") or {}).get("items", []) if item.get("surface") or item["id"] in only]
    if only:
        operation_ids = [i for i in operation_ids if i in only]

    results = {}
    for operation_id in sorted(operation_ids):
        described = (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", operation_id).get("data") or {})
        model, call = described.get("providerModel"), described.get("providerCall") or {}
        modules = described.get("requiredModules") or []
        result = {"modules": modules, "definitionHash": described.get("definitionHash", ""), "kind": "action"}
        results[operation_id] = result
        if described.get("strategy") == "DESTRUCTIVE_GUARDED":
            if any(m not in installed for m in modules):
                result["status"] = "UNSUPPORTED_MODULE"
                continue
            result.update(deletion(oracle, options, operation_id, described, model))
            print(f"{operation_id}: {result['status']}", file=sys.stderr)
            continue
        if not call:
            result["status"] = "NOT_RUN_NO_PROVIDER_CALL"
            continue
        if call.get("wizardLinkField") or call.get("wizardLinkSourceModel"):
            # The CLI hands a curated wizard its records through a link field; the web client opens the
            # same wizard from the record, through the active context or the method its button calls.
            # The oracle does what the web client does, and the outcomes must agree.
            call = {**call, "wizardLinkField": "", "wizardLinkSourceModel": ""}
            if operation_id in CURATED_OPENERS:
                call["wizardOpenerMethod"] = CURATED_OPENERS[operation_id]
        if any(m in EXTERNAL_MODULES for m in modules):
            result["status"] = "NOT_RUN_EXTERNAL_PARTY"
            continue
        if any(m not in installed for m in modules):
            result["status"] = "UNSUPPORTED_MODULE"
            continue
        if call.get("method") in CHATTER:
            result.update(chatter(oracle, options, operation_id, described, model, CHATTER[call["method"]]))
            print(f"{operation_id}: {result['status']}", file=sys.stderr)
            continue
        # The form buttons this operation stands for.
        curated_button = CURATED_BUTTONS.get(operation_id)
        if curated_button:
            call = dict(curated_button["call"])
        buttons = buttons_of.get(model, [])
        if curated_button and curated_button.get("list"):
            mine = [{"type": "object", "name": call["method"], "invisible": ""}]
        elif call.get("method"):
            mine = [b for b in buttons if b["type"] == "object" and b["name"] == call["method"]]
        elif call.get("wizardOpenerMethod"):
            mine = [b for b in buttons if b["type"] == "object" and b["name"] == call["wizardOpenerMethod"]]
        else:
            mine = [b for b in buttons if b["type"] == "action" and (b.get("action") or {}).get("model") == call["wizardModel"]]
        if not mine:
            result["status"] = "NOT_RUN_NO_BUTTON"
            continue
        try:
            meta = oracle.call(model, "fields_get", [], {"attributes": ["type", "store"]})
            button_reads = set().union(*(expression_names(b.get("invisible")) for b in mine))
            missing = sorted(n for n in button_reads if n not in meta and n != "id")
            if missing:
                raise Unsupported(f"names {missing}")
            # Beyond what the button reads, what most often makes Odoo branch: the state, the
            # partner (an SMS to a customer with a phone), the company and the kind of record.
            # Twins agreeing on those are sought first; failing that, twins agreeing on the state
            # and on what the button reads.
            strict = button_reads | {f for f in TWIN_FIELDS if f in meta}
            loose = button_reads | ({"state"} if "state" in meta else set())
            # Archived records too: the form shows them, and some buttons are for them (restore).
            # Inside the operation's provider domain: a record outside it is one the CLI refuses, rightly.
            candidates = oracle.call(model, "search_read", [described.get("providerDomain") or []],
                                     {"fields": sorted(strict | {"id"}), "limit": CANDIDATES,
                                      "order": "id desc", "context": {"active_test": False}})
            shown = []
            for raw in candidates:
                record = {k: normalized(meta.get(k, {}), v) for k, v in raw.items()}
                if any(visible(b.get("invisible"), record, oracle.uid) for b in mine):
                    shown.append(record)
            pair, basis = None, None
            if operation_id in FIXTURES:
                made = FIXTURES[operation_id](Fixtures(oracle))
                rows = oracle.call(model, "read", [made, sorted(strict | {"id"})])
                records = [{k: normalized(meta.get(k, {}), v) for k, v in row.items()} for row in rows]
                if all(any(visible(b.get("invisible"), record, oracle.uid) for b in mine) for record in records):
                    pair, basis = made, "fixture"
                else:
                    result.update({"status": "NOT_RUN_FIXTURE_NOT_APPLICABLE", "detail": "the made records do not show the button"})
                    continue
            for basis_name, keys in (() if pair else (("strict", strict), ("button", loose))):
                groups, order = {}, []
                for record in shown:
                    signature = json.dumps([record.get(f) for f in sorted(keys - {"id"})], sort_keys=True, default=str)
                    if signature not in groups:
                        groups[signature] = []
                        order.append(signature)
                    groups[signature].append(record["id"])
                pair = next((groups[s][:2] for s in order if len(groups[s]) >= 2), None)
                if pair:
                    basis = basis_name
                    break
        except Unsupported as error:
            result.update({"status": "NOT_RUN_UNEVALUABLE", "detail": str(error)[:160]})
            continue
        except ProviderRefusal as error:
            result.update({"status": "ORACLE_ERROR", "detail": str(error)[:160]})
            continue
        if pair is None:
            result["status"] = "NOT_RUN_NO_TWINS"
            continue
        cli_target, oracle_target = pair
        compared = sorted(f for f, m in meta.items() if m.get("store") and m["type"] in COMPARED_TYPES | COUNTED_TYPES
                          and f not in VOLATILE and not f.startswith(VOLATILE_PREFIXES))

        def snapshot(record_id):
            row = oracle.call(model, "read", [[record_id], compared])[0]
            return {f: (len(row.get(f) or []) if meta[f]["type"] in COUNTED_TYPES else normalized(meta[f], row.get(f)))
                    for f in compared}

        before = {cli_target: snapshot(cli_target), oracle_target: snapshot(oracle_target)}
        samples = samples_for(oracle, call, described.get("inputs"))
        args = ["execute", "--operation", operation_id, "--provider-target-id", str(cli_target), "--allow-unverified"]
        for name, value in sorted(samples.items()):
            args += ["--set", f"{name}={value}"]
        document = cli(options.odoo_bin, options.profile, *args)
        by_cli = cli_outcome(document)
        context_text = next((b.get("context") for b in mine if b.get("context")), "")
        try:
            button_context = literal_context(context_text, {"active_model": model, "active_id": oracle_target, "active_ids": [oracle_target]})
            by_oracle = web.click(model, oracle_target, call, button_context, samples)
        except Unsupported as error:
            by_oracle = "UNEVALUABLE:" + str(error)[:80]
        except ProviderRefusal as error:
            by_oracle = "REFUSED"
            result["oracleRefusal"] = str(error)[:160]
        after = {cli_target: snapshot(cli_target), oracle_target: snapshot(oracle_target)}
        changed = {rid: sorted(f for f in compared if before[rid][f] != after[rid][f]) for rid in pair}
        result.update({"cliOutcome": by_cli, "oracleOutcome": by_oracle, "pairHash": canonical_hash(pair), "twinBasis": basis,
                       "changedFields": changed[cli_target], "oracleChangedFields": changed[oracle_target],
                       "transitions": {f: [before[cli_target][f], after[cli_target][f]] for f in changed[cli_target]
                                       if meta[f]["type"] in EXACT_TYPES},
                       "countChanges": {f: after[cli_target][f] - before[cli_target][f] for f in changed[cli_target]
                                        if meta[f]["type"] in COUNTED_TYPES}})
        if by_cli == "OK" and by_oracle == "OK":
            exact_differs = [f for f in changed[cli_target] if meta[f]["type"] in EXACT_TYPES and after[cli_target][f] != after[oracle_target][f]]
            exact_differs += [f for f in changed[cli_target] if meta[f]["type"] in COUNTED_TYPES and f in changed[oracle_target]
                              and after[cli_target][f] - before[cli_target][f] != after[oracle_target][f] - before[oracle_target][f]]
            if not changed[cli_target] and not changed[oracle_target]:
                result["status"] = "AGREE_NO_CHANGE"
            elif changed[cli_target] != changed[oracle_target] or exact_differs:
                differing = sorted(set(changed[cli_target]) ^ set(changed[oracle_target]) | set(exact_differs))
                result["differingFields"] = differing
                # A difference the twins already had before either acted (one lead's probability
                # was already 0) says nothing about the operation: inconclusive, not against it.
                if any(before[cli_target][f] != before[oracle_target][f] for f in differing):
                    result["status"] = "INCONCLUSIVE_TWINS_DIFFER"
                else:
                    result["status"] = "DISAGREE"
            else:
                result["status"] = "AGREE"
        elif by_cli.startswith("DIALOG:") and by_cli == by_oracle:
            result["status"] = "AGREE_DIALOG"
        elif by_cli.startswith("REFUSED") and by_oracle == "REFUSED":
            result["status"] = "AGREE_REFUSED"
        else:
            result["status"] = "DISAGREE"
        print(f"{operation_id}: {result['status']} cli={by_cli} oracle={by_oracle}", file=sys.stderr)

    evidence = {
        "schemaVersion": "odoo.action-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": oracle.version.get("server_serie"),
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": "twin records on which the form shows the button and which agree on what its invisible expression "
                  "reads and on state; the CLI on one, an independent web-client oracle (button context, wizard "
                  "defaults by onchange, the operation's dialog answers) on the other; stored scalar fields compared "
                  "before and after: the same fields changed, selections and booleans to the same values. Twins are sought "
                  "inside the operation's provider domain. A chatter operation (a note, an activity) is compared by what "
                  "each path left on its record: one new note or activity, the same in type, subtype, author, summary, "
                  "user and deadline. A guarded delete runs on two fresh records inside its domain, one deleted through "
                  "the CLI and one unlinked as the web client's Delete does: both gone, nothing else in the domain.",
        "records": results,
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    counts = {}
    for result in results.values():
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    print(f"{oracle.version.get('server_serie')}: {len(results)} UNVERIFIED actions and wizards -> {counts}", file=sys.stderr)


if __name__ == "__main__":
    main()
