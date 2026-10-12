#!/usr/bin/env python3
"""Seed a conformance lab with records for the operations it cannot verify for want of data.

A read on an empty model agrees with nothing (AGREE_EMPTY); an update needs a record to update; an
action needs two records the button shows on. The demo data leaves a few models empty. For every
operation named (or every one a conformance evidence file reports NOT_RUN_NO_RECORD, AGREE_EMPTY or
NOT_RUN_NO_TWINS), this script makes sure the operation's model holds at least --count records inside
the operation's provider domain:

  * the values the model's own defaults give (default_get);
  * every required field still unset, filled generically: a char gets a conformance name, a
    many2one the first record of its relation, a selection its first value, a number 1, a date
    today;
  * the provider domain's simple leaves (field = value);
  * the overrides of SEEDS, for models whose records need more than that.

Records are made through the oracle (JSON-RPC as the given login), never through the CLI under
test. Lab only: it writes. It prints what it made and why it could not, and never a credential.

    ODOO_ORACLE_PASSWORD=... scripts/dev/odoo-lab-seed.py --odoo-bin ./odoo-cli \\
        --endpoint http://127.0.0.1:58718 --database lab19demo --login admin \\
        --from-evidence docs/.../evidence/read-conformance-....json [--operation id ...]
"""

import argparse
import datetime
import json
import os
import subprocess
import sys
import urllib.request

STAMP = datetime.datetime.now().strftime("%Y%m%d%H%M%S")
SEEDED_STATUSES = {"NOT_RUN_NO_RECORD", "AGREE_EMPTY", "NOT_RUN_NO_TWINS"}


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.url, self.database, self.password = endpoint.rstrip("/") + "/jsonrpc", database, password
        self.uid = self._rpc("common", "login", [database, login, password])
        if not self.uid:
            raise SystemExit("the oracle login was refused")

    def _rpc(self, service, method, args):
        body = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                           "params": {"service": service, "method": method, "args": args}}).encode()
        answer = json.loads(urllib.request.urlopen(urllib.request.Request(self.url, body, {"Content-Type": "application/json"}),
                                                   timeout=300).read())
        if "error" in answer:
            raise RuntimeError(((answer["error"].get("data") or {}).get("message") or "provider error")[:300])
        return answer.get("result")

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])

    def first(self, model, domain=None):
        found = self.call(model, "search", [domain or []], {"limit": 1, "order": "id"})
        return found[0] if found else False


# Records a model's required fields alone do not make valid: values chosen by hand, as a function of
# the oracle. Each returns the values to add (they win over the generic ones); it is called again for
# every record, so a seed can tell the records apart.
def _event(o):
    return o.first("event.event") or o.call("event.event", "create", [{
        "name": f"Conformance event {STAMP}", "date_begin": f"{datetime.date.today() + datetime.timedelta(days=30)} 09:00:00",
        "date_end": f"{datetime.date.today() + datetime.timedelta(days=30)} 17:00:00"}])


def _question(o, kind="simple_choice"):
    found = o.first("event.question", [["question_type", "=", kind]])
    if found:
        return found
    return o.call("event.question", "create", [{"title": f"Conformance question {STAMP}", "question_type": kind,
                                                 "event_ids": [[6, 0, [_event(o)]]]}])


def _long_event(o):
    """An event of three whole days in UTC, so a slot on its middle day falls inside it in any time zone."""
    start = datetime.date.today() + datetime.timedelta(days=30)
    found = o.first("event.event", [["name", "=", "Conformance event (three days)"]])
    return found or o.call("event.event", "create", [{
        "name": "Conformance event (three days)", "date_tz": "UTC",
        "date_begin": f"{start} 00:00:00", "date_end": f"{start + datetime.timedelta(days=3)} 00:00:00"}])


def _card_campaign(o):
    """A card is made for one record of its campaign's model, and a campaign holds one card per record."""
    campaign = o.call("card.campaign", "search_read", [[]], {"fields": ["res_model"], "limit": 1, "order": "id"})[0]
    carded = [c["res_id"] for c in o.call("card.card", "search_read", [[["campaign_id", "=", campaign["id"]]]], {"fields": ["res_id"]})]
    return {"campaign_id": campaign["id"], "res_id": o.first(campaign["res_model"], [["id", "not in", carded]])}


SEEDS = {
    "event.slot": lambda o: {"event_id": _long_event(o), "date": str(datetime.date.today() + datetime.timedelta(days=31)),
                             "start_hour": 9.0, "end_hour": 11.0},
    "card.card": _card_campaign,
    # A data-cleaning rule on contacts: the first ir.model ("base") is abstract and has no table.
    "data_recycle.model": lambda o: {"res_model_id": o.first("ir.model", [["model", "=", "res.partner"]])},
    "mailing.filter": lambda o: {"mailing_model_id": o.first("ir.model", [["model", "=", "res.partner"]]), "mailing_domain": "[]"},
    "event.question.answer": lambda o: {"question_id": _question(o), "name": f"Conformance answer {STAMP}"},
    "event.registration.answer": lambda o: {
        "question_id": _question(o, "text_box"), "value_text_box": "Conformance",
        "registration_id": o.first("event.registration") or o.call("event.registration", "create", [{
            "event_id": _event(o), "name": "Conformance attendee", "email": "attendee@example.com"}])},
    "event.type.ticket": lambda o: {"name": f"Conformance ticket {STAMP}",
                                    "event_type_id": o.first("event.type") or o.call("event.type", "create", [{"name": "Conformance type"}])},
    "repair.tags": lambda o: {"name": f"Conformance tag {STAMP}"},
    "delivery.zip.prefix": lambda o: {"name": f"9{STAMP[-5:]}"},
}


def generic_values(o, model, fields, extra, serial=1):
    values = o.call(model, "default_get", [list(fields)])
    for name, meta in fields.items():
        if not meta.get("required") or meta.get("readonly") or values.get(name) not in (None, False, "", []):
            continue
        kind = meta["type"]
        if kind in ("char", "text", "html"):
            values[name] = f"Conformance {model} {STAMP}-{serial}"
        elif kind == "many2one":
            values[name] = o.first(meta["relation"])
        elif kind == "selection" and meta.get("selection"):
            values[name] = meta["selection"][0][0]
        elif kind in ("integer", "float", "monetary"):
            values[name] = 1
        elif kind == "date":
            values[name] = str(datetime.date.today())
        elif kind == "datetime":
            values[name] = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        elif kind == "boolean":
            values[name] = False
    values.update(extra)
    return {k: v for k, v in values.items() if k in fields and not isinstance(v, (list, dict)) or k in extra}


def simple_leaves(domain):
    out = {}
    for leaf in domain or []:
        if isinstance(leaf, list) and len(leaf) == 3 and leaf[1] == "=" and "." not in str(leaf[0]):
            out[leaf[0]] = leaf[2]
    return out


def describe(binary, operation):
    done = subprocess.run([binary, "catalog", "describe", "--operation", operation, "--output", "json"],
                          capture_output=True, text=True, timeout=120)
    return json.loads(done.stdout).get("data") or {}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for name in ("--odoo-bin", "--endpoint", "--database", "--login"):
        parser.add_argument(name, required=True)
    parser.add_argument("--password-env", default="ODOO_ORACLE_PASSWORD")
    parser.add_argument("--from-evidence", action="append", default=[])
    parser.add_argument("--operation", action="append", default=[])
    parser.add_argument("--count", type=int, default=2)
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")
    oracle = Oracle(options.endpoint, options.database, options.login, password)

    operations = list(options.operation)
    for path in options.from_evidence:
        records = json.load(open(path, encoding="utf-8"))["records"]
        operations += [op for op, r in records.items() if r.get("status") in SEEDED_STATUSES]
    seen_models = set()
    for operation in sorted(set(operations)):
        described = describe(options.odoo_bin, operation)
        model = described.get("providerModel")
        if not model or model in seen_models:
            continue
        seen_models.add(model)
        domain = described.get("providerDomain") or []
        try:
            have = oracle.call(model, "search_count", [domain])
            missing = options.count - have
            if missing <= 0:
                print(f"{model}: holds {have}")
                continue
            fields = oracle.call(model, "fields_get", [], {"attributes": ["type", "required", "readonly", "relation", "selection"]})
            made = []
            for serial in range(1, missing + 1):
                extra = dict(simple_leaves(domain))
                if model in SEEDS:
                    extra.update(SEEDS[model](oracle))
                made.append(oracle.call(model, "create", [generic_values(oracle, model, fields, extra, serial)]))
            print(f"{model}: made {made}")
        except RuntimeError as error:
            print(f"{model}: NOT SEEDED: {str(error).splitlines()[0][:200]}")


if __name__ == "__main__":
    main()
