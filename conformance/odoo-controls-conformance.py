#!/usr/bin/env python3
"""Evidence that odoo-cli controls turns Odoo's own controls on, and that Odoo then does what each says.

`odoo-cli controls` (packages/odoo/controls, docs/design/CONTROLS_v0_1.md) plans and applies
Odoo's own switches through verified catalog operations. This runner drives it through the real
standalone CLI on a lab and checks every claim against an independent JSON-RPC oracle:

  * plan and apply K01, K02, K03, K04, K05 and K07 with a plan hash; `controls status` then says ON,
    and the oracle reads each switch where Odoo keeps it (the company, the Settings page);
  * K01: a purchase user's order over the amount waits in To Approve, and its own Approve does nothing;
  * K02: the approved order is locked and the purchase user cannot cancel it;
  * K03: a confirmed sales order is locked and cannot be cancelled;
  * K04: an invoice posted before the lock cannot be reset to draft once its period is locked; a new
    customer invoice or vendor bill dated in the locked period is posted with its document date and
    an accounting date after the lock;
  * K05: a posted vendor bill cannot be deleted, only cancelled;
  * K07: a draft invoice of a customer over its limit carries Odoo's warning;
  * K08, applied last: the purchase user's password is refused over RPC, the API key still works;
  * every setting and lock date is then restored through the CLI, and the oracle sees the first values.

The oracle signs in with the administrator's API key (ODOO_ORACLE_PASSWORD): K08 refuses employees'
passwords over RPC. The purchase user is made by the run, with a random password that never leaves
memory, and archived at the end, with the run's partner and product. The evidence holds statuses and
names only. It promotes nothing: the operations are verified by their own runners.

    ODOO_ORACLE_PASSWORD=<api key> AERP_CREDENTIAL_LAB19=... scripts/dev/odoo-controls-conformance.py \\
        --odoo-bin ./odoo-cli --profile lab19 --endpoint http://127.0.0.1:58719 --database lab19 \\
        --login admin --series 19.0 --image-digest sha256:... --evidence run.json
"""

import argparse
import datetime
import hashlib
import json
import os
import secrets
import subprocess
import sys
import urllib.request

STAMP = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
LOCK_DATE = "2020-12-31"
BEFORE_LOCK = "2020-12-16"
AMOUNT = 1000.0
SETTINGS = ["po_order_approval", "po_double_validation_amount", "lock_confirmed_po", "group_auto_done_setting",
            "restrictive_audit_trail", "account_use_credit_limit", "account_default_credit_limit", "auth_totp_policy"]
LOCKS = ["sale_lock_date", "purchase_lock_date", "tax_lock_date", "fiscalyear_lock_date"]


def canonical_hash(value):
    return "sha256:" + hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class OdooError(RuntimeError):
    pass


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.password = endpoint.rstrip("/"), database, password
        self.version = self._rpc("common", "version", [])
        self.uid = self._rpc("common", "login", [database, login, password])
        if not self.uid:
            raise OdooError(f"{login} could not sign in")

    def _rpc(self, service, method, args):
        payload = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                              "params": {"service": service, "method": method, "args": args}}).encode()
        request = urllib.request.Request(self.endpoint + "/jsonrpc", data=payload, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=300) as response:
            answer = json.loads(response.read())
        if "error" in answer:
            raise OdooError((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        # A method that returns nothing (button_unlock) answers no result.
        return answer.get("result")

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])

    def read1(self, model, record_id, fields, company=None):
        kwargs = {"context": {"allowed_company_ids": [company]}} if company else {}
        return self.call(model, "read", [[record_id], fields], kwargs)[0]

    def page(self, names):
        record = self.call("res.config.settings", "create", [{}])
        return self.call("res.config.settings", "read", [[record], names])[0]


def cli(binary, profile, *args):
    completed = subprocess.run([binary, *args, "--profile", profile, "--output", "json"], capture_output=True, text=True, timeout=600)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT", "message": completed.stderr[-300:]}}


class Control:
    """The checks one control made."""

    def __init__(self):
        self.checked, self.failures = [], []

    def expect(self, name, condition, why=""):
        self.checked.append(name)
        if not condition:
            self.failures.append(f"{name}{': ' + why if why else ''}")
        return condition

    def result(self):
        status = "AGREE" if self.checked and not self.failures else ("DISAGREE" if self.checked else "NOT_RUN")
        return {"status": status, "checked": self.checked, "failures": self.failures}


def refused(call, *words):
    """Whether Odoo refused the call, with one of the words in its message."""
    try:
        call()
    except OdooError as error:
        return not words or any(word in str(error) for word in words)
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--odoo-bin", required=True)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--database", required=True)
    parser.add_argument("--login", required=True)
    parser.add_argument("--password-env", default="ODOO_ORACLE_PASSWORD")
    parser.add_argument("--series", required=True)
    parser.add_argument("--image-digest", required=True)
    parser.add_argument("--evidence", required=True)
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")
    admin = Oracle(options.endpoint, options.database, options.login, password)
    series = admin.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")
    unverified = [] if series == "19.0" else ["--allow-unverified"]
    company = admin.call("res.users", "read", [[admin.uid], ["company_id"]])[0]["company_id"][0]
    ctx = {"context": {"allowed_company_ids": [company]}}
    results = {f"K0{n}": Control() for n in range(1, 9)}
    run = Control()

    def ctl(*args):
        return cli(options.odoo_bin, options.profile, "controls", *args, "--company", str(company), *unverified)

    # What the run restores, and what it makes.
    first_settings = admin.page(SETTINGS)
    first_locks = admin.read1("res.company", company, LOCKS, company)
    hashed = [["restrict_mode_hash_table", "=", True], ["company_id", "=", company]]
    hashed_before = admin.call("account.journal", "search_count", [hashed])
    group_field = "group_ids" if "group_ids" in admin.call("res.users", "fields_get", [], {"attributes": ["type"]}) else "groups_id"
    purchase_user = admin.call("ir.model.data", "search_read", [[["module", "=", "purchase"], ["name", "=", "group_purchase_user"]]],
                               {"fields": ["res_id"]})[0]["res_id"]
    vendor = admin.call("res.partner", "create", [{"name": f"Controls conformance vendor {STAMP}", "company_id": company}])
    customer = admin.call("res.partner", "create", [{"name": f"Controls conformance customer {STAMP}", "company_id": company}])
    product = admin.call("product.product", "create", [{"name": f"Controls conformance service {STAMP}", "type": "service",
                                                         "purchase_ok": True, "sale_ok": True, "list_price": 100.0,
                                                         "taxes_id": [(6, 0, [])], "supplier_taxes_id": [(6, 0, [])]}])
    user_login = f"controls-conformance-{STAMP}"
    user_password = secrets.token_urlsafe(24)
    user = admin.call("res.users", "create", [{"name": f"Controls conformance buyer {STAMP}", "login": user_login,
                                               "password": user_password, "company_id": company, "company_ids": [(6, 0, [company])],
                                               group_field: [(4, purchase_user)]}])
    buyer = Oracle(options.endpoint, options.database, user_login, user_password)

    def invoice(move_type, day):
        return admin.call("account.move", "create", [{"move_type": move_type, "partner_id": vendor if move_type == "in_invoice" else customer,
                                                      "invoice_date": day, "company_id": company, "invoice_line_ids": [
            (0, 0, {"product_id": product, "quantity": 1, "price_unit": 100.0, "tax_ids": [(6, 0, [])]})]}], ctx)

    # An invoice of the period the run will lock, posted while it is still open.
    earlier = invoice("out_invoice", BEFORE_LOCK)
    admin.call("account.move", "action_post", [[earlier]], ctx)
    made = [earlier]
    try:
        # 1. Plan and apply everything but K08, with the plan's hash.
        selection = ["--enable", "K01,K02,K03,K04,K05,K07", "--param", f"K01.amount={AMOUNT}", "--param", f"K04.date={LOCK_DATE}"]
        planned = ctl("plan", *selection)
        plan_hash = ((planned.get("data") or {}).get("plan") or {}).get("planHash")
        run.expect("plan", planned.get("status") == "OK" and plan_hash, str((planned.get("error") or {}).get("message"))[:200])
        applied = ctl("apply", *selection, "--plan-hash", plan_hash or "none")
        run.expect("apply", applied.get("status") == "OK", str((applied.get("error") or {}).get("message"))[:200])
        stale = ctl("apply", *selection, "--plan-hash", "sha256:" + "0" * 64)
        run.expect("apply refuses another plan", (stale.get("error") or {}).get("code") == "CONTROLS_PLAN_CHANGED")
        status = {c["id"]: c["state"] for c in ((ctl("status").get("data") or {}).get("controls") or [])}
        for control in ("K01", "K02", "K03", "K04", "K05", "K07"):
            results[control].expect("status ON", status.get(control) == "ON", str(status.get(control)))
        results["K06"].expect("read, never applied", status.get("K06") in ("OFF", "PARTIAL", "ON") and
                              admin.call("account.journal", "search_count", [hashed]) == hashed_before, "a journal was hashed")

        # 2. Odoo holds each switch where it keeps it.
        held = admin.read1("res.company", company, ["po_double_validation", "po_double_validation_amount", "po_lock",
                                                    "restrictive_audit_trail", "account_use_credit_limit"] + LOCKS, company)
        results["K01"].expect("two-step approval", held["po_double_validation"] == "two_step" and held["po_double_validation_amount"] == AMOUNT)
        results["K02"].expect("orders lock", held["po_lock"] == "lock")
        results["K03"].expect("sales lock", admin.page(["group_auto_done_setting"])["group_auto_done_setting"] is True)
        results["K04"].expect("lock dates", all(held[f] == LOCK_DATE for f in LOCKS), str({f: held[f] for f in LOCKS}))
        results["K05"].expect("audit trail", held["restrictive_audit_trail"] is True)
        results["K07"].expect("credit limit", held["account_use_credit_limit"] is True)

        # 3. What Odoo then does.
        order = buyer.call("purchase.order", "create", [{"partner_id": vendor, "company_id": company, "order_line": [
            (0, 0, {"product_id": product, "product_qty": 1, "price_unit": AMOUNT * 3})]}])
        buyer.call("purchase.order", "button_confirm", [[order]])
        results["K01"].expect("over the amount waits", buyer.read1("purchase.order", order, ["state"])["state"] == "to approve")
        buyer.call("purchase.order", "button_approve", [[order]])
        results["K01"].expect("the buyer cannot approve", buyer.read1("purchase.order", order, ["state"])["state"] == "to approve")
        admin.call("purchase.order", "button_approve", [[order]], ctx)
        approved = admin.read1("purchase.order", order, ["state", "locked"], company)
        results["K02"].expect("approved and locked", approved["state"] == "purchase" and approved["locked"] is True, str(approved))
        results["K02"].expect("the buyer cannot cancel", refused(lambda: buyer.call("purchase.order", "button_cancel", [[order]]), "unlock"))
        admin.call("purchase.order", "button_unlock", [[order]], ctx)
        admin.call("purchase.order", "button_cancel", [[order]], ctx)

        sale = admin.call("sale.order", "create", [{"partner_id": customer, "company_id": company, "order_line": [
            (0, 0, {"product_id": product, "product_uom_qty": 1, "price_unit": 100.0})]}], ctx)
        admin.call("sale.order", "action_confirm", [[sale]], ctx)
        results["K03"].expect("confirmed and locked", admin.read1("sale.order", sale, ["locked"], company)["locked"] is True)
        results["K03"].expect("cannot cancel", refused(lambda: admin.call("sale.order", "action_cancel", [[sale]], ctx), "unlock", "locked"))
        admin.call("sale.order", "action_unlock", [[sale]], ctx)
        admin.call("sale.order", "action_cancel", [[sale]], {"context": {"allowed_company_ids": [company], "disable_cancel_warning": True}})

        results["K04"].expect("an invoice of the locked period cannot be reset to draft",
                              refused(lambda: admin.call("account.move", "button_draft", [[earlier]], ctx), "prior to"))
        for move_type in ("out_invoice", "in_invoice"):
            late = invoice(move_type, BEFORE_LOCK)
            made.append(late)
            admin.call("account.move", "action_post", [[late]], ctx)
            posted = admin.read1("account.move", late, ["state", "date", "invoice_date"], company)
            results["K04"].expect(f"a new {move_type} dated in the locked period is posted after the lock",
                                  posted["state"] == "posted" and posted["date"] > LOCK_DATE and posted["invoice_date"] == BEFORE_LOCK, str(posted))
        bill = made[-1]
        admin.call("account.move", "button_draft", [[bill]], ctx)
        results["K05"].expect("a posted bill cannot be deleted",
                              refused(lambda: admin.call("account.move", "unlink", [[bill]], ctx), "audit trail"))
        admin.call("account.move", "button_cancel", [[bill]], ctx)

        admin.call("res.partner", "write", [[customer], {"credit_limit": 1.0}], ctx)
        draft = invoice("out_invoice", datetime.date.today().isoformat())
        warning = admin.read1("account.move", draft, ["partner_credit_warning"], company)["partner_credit_warning"]
        results["K07"].expect("a customer over its limit is warned on a draft", bool(warning))
        admin.call("account.move", "unlink", [[draft]], ctx)

        # 4. K08 last: it refuses employees' passwords over RPC.
        planned = ctl("plan", "--enable", "K08")
        k08_hash = ((planned.get("data") or {}).get("plan") or {}).get("planHash")
        applied = ctl("apply", "--enable", "K08", "--plan-hash", k08_hash or "none")
        results["K08"].expect("applied", applied.get("status") == "OK", str((applied.get("error") or {}).get("message"))[:200])
        status = {c["id"]: c["state"] for c in ((ctl("status").get("data") or {}).get("controls") or [])}
        results["K08"].expect("status ON, the CLI still reads with its API key", status.get("K08") == "ON")
        try:
            Oracle(options.endpoint, options.database, user_login, user_password)
            results["K08"].expect("an employee's password is refused over RPC", False, "it signed in")
        except OdooError:
            results["K08"].expect("an employee's password is refused over RPC", True)
        results["K08"].expect("the administrator's API key still works", bool(admin.page(["auth_totp_policy"])))
    finally:
        # 5. Restore, through the CLI, and archive what the run made.
        back = cli(options.odoo_bin, options.profile, "execute", "--operation", "structure.settings.update", "--values",
                   json.dumps({k: (v if v is not None else False) for k, v in first_settings.items() if k != "id"}), "--allow-unverified")
        run.expect("settings restored through the CLI", back.get("status") == "OK", str((back.get("error") or {}).get("message"))[:200])
        locks = cli(options.odoo_bin, options.profile, "execute", "--operation", "structure.company.update", "--provider-target-id",
                    str(company), "--values", json.dumps({f: first_locks[f] for f in LOCKS}), "--allow-unverified")
        run.expect("lock dates restored through the CLI", locks.get("status") == "OK", str((locks.get("error") or {}).get("message"))[:200])
        now = admin.page(SETTINGS)
        run.expect("the oracle sees the first settings", all(now[k] == first_settings[k] for k in SETTINGS),
                   str({k: (first_settings[k], now[k]) for k in SETTINGS if now[k] != first_settings[k]}))
        held = admin.read1("res.company", company, LOCKS, company)
        run.expect("the oracle sees the first lock dates", all(held[f] == first_locks[f] for f in LOCKS))
        for move in made:
            # Cancelled, not deleted: they were posted (Odoo keeps a posted entry's trace).
            if admin.read1("account.move", move, ["state"], company)["state"] == "posted":
                admin.call("account.move", "button_draft", [[move]], ctx)
            if admin.read1("account.move", move, ["state"], company)["state"] == "draft":
                admin.call("account.move", "button_cancel", [[move]], ctx)
        admin.call("res.users", "write", [[user], {"active": False}])
        admin.call("product.product", "write", [[product], {"active": False}])
        admin.call("res.partner", "write", [[vendor, customer], {"active": False}])

    records = {control: check.result() for control, check in results.items()}
    records["run"] = run.result()
    evidence = {
        "schemaVersion": "odoo.controls-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": admin.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": ("odoo-cli controls plan, apply (with the plan hash) and status for K01-K05 and K07, then K08, on one company; "
                   "each switch read where Odoo keeps it, and Odoo's behaviour behind each control exercised through an "
                   "independent JSON-RPC oracle, as the administrator and as a purchase user the run made; every setting "
                   "and lock date restored through the CLI afterwards. Statuses and names only."),
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
        "records": records,
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    for control, result in records.items():
        print(f"{series} {control}: {result['status']} {result['failures'] or ''}", file=sys.stderr)


if __name__ == "__main__":
    main()
