#!/usr/bin/env python3
"""Conformance evidence for the alert operations and for what the alerts make Odoo do.

The alerts of odoo-cli controls (K09-K11, docs/design/CONTROLS_v0_1.md §6) are automation rules
(base_automation) whose only action schedules an activity. This runner drives them through the real
standalone CLI and checks every step against an independent JSON-RPC oracle:

  * the metadata reads (structure.registry_model.list, structure.registry_field.list,
    structure.activity_type.list) answer what Odoo holds;
  * controls plan and apply create the three rules and their actions (structure.alert_rule.create,
    structure.alert_action.create): the oracle reads each field back; a second plan is NO_CHANGE;
  * the list and describe of rules and actions answer what Odoo holds, and never a rule or action
    a person made (one is made for the check, archived, with an Update Record action);
  * an update archives a rule and another changes an action's user (structure.alert_rule.update,
    structure.alert_action.update); the same updates on the person's rule and action are refused;
    an action of another type is refused before Odoo is reached;
  * then Odoo's behaviour: a changed bank account, a posted entry reset to draft and a traded partner
    renamed each schedule one activity for the user named; re-posting and renaming a partner that
    never traded schedule none.

An operation AGREEs only when every one of its checks agreed; the behaviour is recorded per alert,
apart from the operations. The oracle signs in with an API key (ODOO_ORACLE_PASSWORD). The rules and
the records the run makes are removed or archived afterwards. It promotes nothing.

    ODOO_ORACLE_PASSWORD=<api key> AERP_CREDENTIAL_LAB19=... scripts/dev/odoo-alerts-conformance.py \\
        --odoo-bin ./odoo-cli --profile lab19 --endpoint http://127.0.0.1:58719 --database lab19 \\
        --login admin --series 19.0 --image-digest sha256:... --evidence run.json
"""

import argparse
import datetime
import hashlib
import json
import os
import subprocess
import sys
import urllib.request

STAMP = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")
PREFIX = "odoo-cli: "
ALERTS = ("K09", "K10", "K11")
OPERATIONS = ("structure.registry_model.list", "structure.registry_field.list", "structure.activity_type.list",
              "structure.alert_rule.list", "structure.alert_rule.describe", "structure.alert_rule.create", "structure.alert_rule.update",
              "structure.alert_action.list", "structure.alert_action.describe", "structure.alert_action.create",
              "structure.alert_action.update")
RULE_FIELDS = ["name", "model_id", "trigger", "trigger_field_ids", "filter_pre_domain", "filter_domain", "active"]
ACTION_FIELDS = ["name", "model_id", "base_automation_id", "state", "usage", "activity_type_id", "activity_summary",
                 "activity_user_type", "activity_user_id", "activity_date_deadline_range", "activity_date_deadline_range_type"]


def canonical_hash(value):
    return "sha256:" + hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class OdooError(RuntimeError):
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
        with urllib.request.urlopen(request, timeout=300) as response:
            answer = json.loads(response.read())
        if "error" in answer:
            raise OdooError((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        return answer.get("result")

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])


def cli(binary, profile, *args):
    # The catalog's own description needs no Odoo; everything else runs on the profile.
    selection = [] if args[:2] == ("catalog", "describe") else ["--profile", profile]
    completed = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=600)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT", "message": completed.stderr[-300:]}}


def rows(answer):
    data = ((answer.get("data") or {}).get("data")) if answer.get("status") == "OK" else None
    return data if isinstance(data, list) else ([data] if isinstance(data, dict) else [])


def first_id(value):
    if isinstance(value, (list, tuple)) and value:
        return value[0]
    if isinstance(value, dict):
        return value.get("id")
    return value or None


def comparable(value):
    """A many2one as its id, a many2many as its sorted ids, an empty value as None."""
    if isinstance(value, dict):
        return value.get("id")
    if isinstance(value, (list, tuple)):
        if len(value) == 2 and isinstance(value[0], (int, float)) and isinstance(value[1], str):
            return value[0]
        return sorted(first_id(v) for v in value)
    if value in (False, None, ""):
        return None
    return value


class Checks:
    def __init__(self):
        self.fields, self.failures = {}, []

    def same(self, name, expected, observed):
        agreed = comparable(expected) == comparable(observed)
        self.fields[name] = self.fields.get(name, True) and agreed
        if not agreed:
            self.failures.append(name)
        return agreed

    def expect(self, name, condition, why=""):
        self.fields[name] = self.fields.get(name, True) and bool(condition)
        if not condition:
            self.failures.append(f"{name}{': ' + why if why else ''}")
        return bool(condition)

    def result(self, modules, definition_hash):
        status = "AGREE" if self.fields and not self.failures else ("DISAGREE" if self.fields else "NOT_RUN")
        return {"status": status, "modules": modules, "definitionHash": definition_hash,
                "checkedFields": sorted(self.fields), "failures": sorted(set(self.failures))}


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
    oracle = Oracle(options.endpoint, options.database, options.login, password)
    series = oracle.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")
    if oracle.call("ir.module.module", "search_count", [[["name", "=", "base_automation"], ["state", "=", "installed"]]]) != 1:
        raise SystemExit("base_automation is not installed on this lab")
    company = oracle.call("res.users", "read", [[oracle.uid], ["company_id", "name"]])[0]
    user_name, company = company["name"], company["company_id"][0]
    ctx = {"context": {"allowed_company_ids": [company]}}
    described = {op: (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", op).get("data") or {})
                 for op in OPERATIONS}
    checks = {op: Checks() for op in OPERATIONS}
    behaviour = {alert: Checks() for alert in ALERTS}

    def execute(op, *args):
        return cli(options.odoo_bin, options.profile, "execute", "--operation", op, "--allow-unverified", *args)

    def controls(*args):
        return cli(options.odoo_bin, options.profile, "controls", *args, "--company", str(company), "--allow-unverified")

    def activities(model, res_id, rule):
        summary = oracle.call("ir.actions.server", "search_read", [[["base_automation_id", "=", rule]]], {"fields": ["activity_summary"]})
        return oracle.call("mail.activity", "search_count", [[["res_model", "=", model], ["res_id", "=", res_id],
                                                              ["summary", "=", summary[0]["activity_summary"]], ["user_id", "=", oracle.uid]]])

    # A clean start: odoo-cli's rules from an earlier run are removed through the oracle.
    old = oracle.call("base.automation", "search", [[["name", "=like", PREFIX + "%"]]], {"context": {"active_test": False}})
    if old:
        oracle.call("base.automation", "unlink", [old])

    # The person's own rule and action, which no alert operation may reach.
    partner_model = oracle.call("ir.model", "search", [[["model", "=", "res.partner"]]])[0]
    manual_rule = oracle.call("base.automation", "create", [{"name": f"Manual rule {STAMP}", "model_id": partner_model,
                                                             "trigger": "on_create_or_write", "active": False}])
    manual_action = oracle.call("ir.actions.server", "create", [{"name": f"Manual action {STAMP}", "model_id": partner_model,
                                                                 "base_automation_id": manual_rule, "usage": "base_automation",
                                                                 "state": "object_write"}])
    made_partners = []
    try:
        # 1. The metadata reads.
        names = ["res.partner.bank", "account.move", "res.partner"]
        listed = rows(execute("structure.registry_model.list", "--filter", json.dumps([["model", "in", names]])))
        stored = oracle.call("ir.model", "search_read", [[["model", "in", names]]], {"fields": ["model", "is_mail_activity"]})
        check = checks["structure.registry_model.list"]
        check.expect("count", len(listed) == len(stored) == 3, f"{len(listed)} vs {len(stored)}")
        by_model = {row["model"]: row for row in listed}
        for row in stored:
            check.same(f"{row['model']}.id", row["id"], (by_model.get(row["model"]) or {}).get("id"))
            check.same(f"{row['model']}.is_mail_activity", row["is_mail_activity"], (by_model.get(row["model"]) or {}).get("is_mail_activity"))
        fields = ["acc_number", "account_number", "partner_id", "allow_out_payment"]
        listed = rows(execute("structure.registry_field.list", "--filter",
                              json.dumps([["model", "=", "res.partner.bank"], ["name", "in", fields]])))
        stored = oracle.call("ir.model.fields", "search_read", [[["model", "=", "res.partner.bank"], ["name", "in", fields]]], {"fields": ["name", "ttype"]})
        check = checks["structure.registry_field.list"]
        check.expect("count", len(listed) == len(stored) and len(stored) >= 3, f"{len(listed)} vs {len(stored)}")
        by_name = {row["name"]: row for row in listed}
        for row in stored:
            check.same(f"{row['name']}.id", row["id"], (by_name.get(row["name"]) or {}).get("id"))
            check.same(f"{row['name']}.ttype", row["ttype"], (by_name.get(row["name"]) or {}).get("ttype"))
        listed = rows(execute("structure.activity_type.list", "--filter", json.dumps([["res_model", "=", False]])))
        stored = oracle.call("mail.activity.type", "search_read", [[["res_model", "=", False]]], {"fields": ["name", "category"]})
        check = checks["structure.activity_type.list"]
        check.expect("count", len(listed) == len(stored) and len(stored) > 0, f"{len(listed)} vs {len(stored)}")
        by_id = {row["id"]: row for row in listed}
        for row in stored:
            check.same(f"{row['id']}.category", row["category"], (by_id.get(row["id"]) or {}).get("category"))

        # 2. Plan and apply the three alerts.
        selection = ["--enable", ",".join(ALERTS)] + [p for alert in ALERTS for p in ("--param", f"{alert}.user={user_name}")]
        planned = controls("plan", *selection)
        plan_hash = ((planned.get("data") or {}).get("plan") or {}).get("planHash")
        applied = controls("apply", *selection, "--plan-hash", plan_hash or "none")
        create_rule, create_action = checks["structure.alert_rule.create"], checks["structure.alert_action.create"]
        create_rule.expect("applied", applied.get("status") == "OK", str((applied.get("error") or {}).get("message"))[:200])
        create_action.expect("applied", applied.get("status") == "OK")
        rules = oracle.call("base.automation", "search_read", [[["name", "=like", PREFIX + "%"]]], {"fields": RULE_FIELDS + ["action_server_ids"]})
        create_rule.expect("three rules", len(rules) == 3, str(len(rules)))
        by_alert = {rule["name"].split()[1]: rule for rule in rules}
        expected = {"K09": ("res.partner.bank", None, None), "K10": ("account.move", "[('state', '=', 'posted')]", "[('state', '=', 'draft')]"),
                    "K11": ("res.partner", "[('invoice_ids', '!=', False)]", "[('invoice_ids', '!=', False)]")}
        for alert, (model, before, after) in expected.items():
            rule = by_alert.get(alert) or {}
            create_rule.same(f"{alert}.model", oracle.call("ir.model", "search", [[["model", "=", model]]])[0], rule.get("model_id"))
            create_rule.same(f"{alert}.trigger", "on_create_or_write", rule.get("trigger"))
            create_rule.same(f"{alert}.filter_pre_domain", before, rule.get("filter_pre_domain"))
            create_rule.same(f"{alert}.filter_domain", after, rule.get("filter_domain"))
            create_rule.same(f"{alert}.active", True, rule.get("active"))
            create_rule.expect(f"{alert}.trigger_field_ids", len(rule.get("trigger_field_ids") or []) > 0)
            actions = oracle.call("ir.actions.server", "read", [rule.get("action_server_ids") or [], ACTION_FIELDS])
            create_action.expect(f"{alert}.one action", len(actions) == 1, str(len(actions)))
            for action in actions[:1]:
                create_action.same(f"{alert}.state", "next_activity", action["state"])
                create_action.same(f"{alert}.usage", "base_automation", action["usage"])
                create_action.same(f"{alert}.activity_user_type", "specific", action["activity_user_type"])
                create_action.same(f"{alert}.activity_user_id", oracle.uid, action["activity_user_id"])
                create_action.expect(f"{alert}.activity_type_id", bool(action["activity_type_id"]))
                create_action.expect(f"{alert}.activity_summary", bool(action["activity_summary"]))
        again = controls("plan", *selection)
        steps = ((again.get("data") or {}).get("plan") or {}).get("steps") or []
        create_rule.expect("a second plan is NO_CHANGE", steps and all(s["action"] == "NO_CHANGE" for s in steps),
                           str([s["action"] for s in steps]))

        # 3. Reads of rules and actions: what Odoo holds, never the person's own.
        for entity, model, fields_read in (("alert_rule", "base.automation", RULE_FIELDS), ("alert_action", "ir.actions.server", ACTION_FIELDS)):
            listed = rows(execute(f"structure.{entity}.list", "--filter", json.dumps([]), "--limit", "50"))
            mine = oracle.call(model, "search_read", [[["name", "=like", PREFIX + "%"]]], {"fields": fields_read})
            check = checks[f"structure.{entity}.list"]
            check.expect("only odoo-cli's", {r["id"] for r in listed} == {r["id"] for r in mine},
                         f"{sorted(r['id'] for r in listed)} vs {sorted(r['id'] for r in mine)}")
            check.expect("never the person's", manual_rule not in {r["id"] for r in listed} and manual_action not in {r["id"] for r in listed})
            by_id = {r["id"]: r for r in listed}
            for record in mine:
                for field in fields_read:
                    if field in by_id.get(record["id"], {}):
                        check.same(f"{field}", record[field], by_id[record["id"]][field])
            check = checks[f"structure.{entity}.describe"]
            for record in mine:
                answer = rows(execute(f"structure.{entity}.describe", "--provider-target-id", str(record["id"])))
                for field in fields_read:
                    if answer and field in answer[0]:
                        check.same(f"{field}", record[field], answer[0][field])
                check.expect("answered", len(answer) == 1)
            outside = execute(f"structure.{entity}.describe", "--provider-target-id", str(manual_rule if entity == "alert_rule" else manual_action))
            check.expect("the person's is out of reach", outside.get("status") != "OK" or not rows(outside))

        # 4. Updates, held to odoo-cli's own.
        rule_id = (by_alert.get("K10") or {}).get("id")
        update = checks["structure.alert_rule.update"]
        answer = execute("structure.alert_rule.update", "--provider-target-id", str(rule_id), "--values", json.dumps({"active": False}))
        update.expect("archived", answer.get("status") == "OK", str((answer.get("error") or {}).get("message"))[:200])
        update.same("active", False, oracle.call("base.automation", "read", [[rule_id], ["active"]], {"context": {"active_test": False}})[0]["active"])
        refused = execute("structure.alert_rule.update", "--provider-target-id", str(manual_rule), "--values", json.dumps({"active": True}))
        update.expect("the person's rule is refused", refused.get("status") != "OK")
        update.same("the person's rule unchanged", False,
                    oracle.call("base.automation", "read", [[manual_rule], ["active"]], {"context": {"active_test": False}})[0]["active"])
        reapplied = controls("apply", *selection, "--plan-hash", ((controls("plan", *selection).get("data") or {}).get("plan") or {}).get("planHash") or "none")
        update.expect("apply turns it on again", reapplied.get("status") == "OK" and
                      oracle.call("base.automation", "read", [[rule_id], ["active"]])[0]["active"] is True)
        action_id = ((by_alert.get("K09") or {}).get("action_server_ids") or [None])[0]
        other = oracle.call("res.users", "search", [[["share", "=", False], ["id", "!=", oracle.uid]]], {"limit": 1})
        update = checks["structure.alert_action.update"]
        if other:
            answer = execute("structure.alert_action.update", "--provider-target-id", str(action_id), "--values", json.dumps({"activity_user_id": other[0]}))
            update.expect("another user", answer.get("status") == "OK", str((answer.get("error") or {}).get("message"))[:200])
            update.same("activity_user_id", other[0], oracle.call("ir.actions.server", "read", [[action_id], ["activity_user_id"]])[0]["activity_user_id"])
            execute("structure.alert_action.update", "--provider-target-id", str(action_id), "--values", json.dumps({"activity_user_id": oracle.uid}))
        refused = execute("structure.alert_action.update", "--provider-target-id", str(manual_action), "--values", json.dumps({"activity_summary": "x"}))
        update.expect("the person's action is refused", refused.get("status") != "OK")
        coded = execute("structure.alert_action.create", "--values", json.dumps({"name": PREFIX + "code " + STAMP, "state": "code",
                                                                                   "model_id": "res.partner", "base_automation_id": rule_id}))
        checks["structure.alert_action.create"].expect("another action type is refused", coded.get("status") != "OK")
        orphan = execute("structure.alert_action.create", "--values", json.dumps({"name": PREFIX + "orphan " + STAMP, "model_id": "res.partner"}))
        checks["structure.alert_action.create"].expect("an action without its rule is refused", orphan.get("status") != "OK")
        # A rule on "base", which every model inherits, would react to every create and write.
        everywhere = execute("structure.alert_rule.create", "--values", json.dumps({"name": PREFIX + "base " + STAMP, "model_id": "base",
                                                                                     "trigger": "on_create_or_write"}))
        checks["structure.alert_rule.create"].expect("a model without activities is refused", everywhere.get("status") != "OK")
        checks["structure.alert_rule.create"].same("nothing made on base", 0, oracle.call(
            "base.automation", "search_count", [[["name", "=", PREFIX + "base " + STAMP]]], {"context": {"active_test": False}}))

        # 5. What Odoo then does.
        vendor = oracle.call("res.partner", "create", [{"name": f"Alerts vendor {STAMP}"}])
        made_partners.append(vendor)
        # 20.0 calls a bank account's number account_number (the dialect's rename).
        number = "acc_number" if "acc_number" in oracle.call("res.partner.bank", "fields_get", [], {"attributes": ["type"]}) else "account_number"
        bank = oracle.call("res.partner.bank", "create", [{"partner_id": vendor, number: f"9704{STAMP[-8:]}"}])
        rule = (by_alert.get("K09") or {}).get("id")
        before = activities("res.partner.bank", bank, rule)
        oracle.call("res.partner.bank", "write", [[bank], {number: f"9705{STAMP[-8:]}"}])
        behaviour["K09"].expect("a changed account schedules one activity", activities("res.partner.bank", bank, rule) - before == 1)
        rule = (by_alert.get("K10") or {}).get("id")
        customer = oracle.call("res.partner", "create", [{"name": f"Alerts customer {STAMP}"}])
        made_partners.append(customer)
        move = oracle.call("account.move", "create", [{"move_type": "out_invoice", "partner_id": customer, "company_id": company,
                                                       "invoice_line_ids": [(0, 0, {"name": "Alerts", "quantity": 1, "price_unit": 100.0, "tax_ids": [(6, 0, [])]})]}], ctx)
        oracle.call("account.move", "action_post", [[move]], ctx)
        before = activities("account.move", move, rule)
        oracle.call("account.move", "button_draft", [[move]], ctx)
        behaviour["K10"].expect("a reset schedules one activity", activities("account.move", move, rule) - before == 1)
        oracle.call("account.move", "action_post", [[move]], ctx)
        behaviour["K10"].expect("posting again schedules none", activities("account.move", move, rule) - before == 1)
        rule = (by_alert.get("K11") or {}).get("id")
        before = activities("res.partner", customer, rule)
        oracle.call("res.partner", "write", [[customer], {"name": f"Alerts customer {STAMP} renamed"}])
        behaviour["K11"].expect("a traded partner renamed schedules one activity", activities("res.partner", customer, rule) - before == 1)
        quiet = oracle.call("res.partner", "create", [{"name": f"Alerts quiet {STAMP}"}])
        made_partners.append(quiet)
        oracle.call("res.partner", "write", [[quiet], {"name": f"Alerts quiet {STAMP} renamed"}])
        behaviour["K11"].expect("a partner that never traded is not reported", activities("res.partner", quiet, rule) == 0)
        oracle.call("account.move", "button_draft", [[move]], ctx)
        oracle.call("account.move", "button_cancel", [[move]], ctx)
    finally:
        # The run's rules are removed, the person's too, and the records it made are archived.
        mine = oracle.call("base.automation", "search", [[["name", "=like", PREFIX + "%"]]], {"context": {"active_test": False}})
        oracle.call("base.automation", "unlink", [mine + [manual_rule]])
        if made_partners:
            oracle.call("res.partner", "write", [made_partners, {"active": False}])

    results = {op: checks[op].result(described[op].get("requiredModules") or [], described[op].get("definitionHash", "")) for op in OPERATIONS}
    evidence = {
        "schemaVersion": "odoo.alerts-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": ("the alert operations driven through the CLI (controls plan and apply for K09-K11, then list, describe and "
                   "update) and read back by an independent JSON-RPC oracle; a rule and an action a person made checked out of "
                   "reach; an action of another type refused; then a changed bank account, a reset entry and a renamed traded "
                   "partner each checked to schedule one activity. Names, statuses and hashes only; the run's rules removed."),
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
        "records": results,
        "alerts": {alert: behaviour[alert].result([], "") for alert in ALERTS},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    for name, result in list(results.items()) + list(evidence["alerts"].items()):
        print(f"{series} {name}: {result['status']} {result['failures'] or ''}", file=sys.stderr)


if __name__ == "__main__":
    main()
