#!/usr/bin/env python3
"""Conformance evidence for UNVERIFIED create/update operations, against a postcondition oracle.

For every UNVERIFIED FIELD_WRITE operation of the standalone catalog whose model this provider has,
this runner:

  * builds the smallest valid record: every field the provider requires and cannot default, and
    every input the catalog itself requires, taken from the operation's own accepted inputs (a
    related record is the provider's first existing one of the target model; an input the catalog
    holds to a scope gets a value inside it: a path under its prefix, a closed value no record holds
    yet), plus `name` when the operation accepts it;
  * creates it through the real standalone CLI (`odoo execute --operation <x>.create
    --allow-unverified`), then reads it back through an independent JSON-RPC oracle and compares
    every value it wrote;
  * changes one simple field of that same record through the paired update operation and reads it
    back again (never a view's architecture, never a scope-held input; a phone gets a number).

An operation AGREEs only when the provider holds exactly what the CLI was asked to write. A record
the provider cannot create from the operation's inputs (a required field the operation does not
accept, a business constraint) stays UNVERIFIED with the reason recorded. Records created here stay
in the lab database.

The evidence document holds statuses, field names and hashes only: no credential, no record
content. It promotes nothing; promotion stays a reviewed source change.
"""

import argparse
import datetime
import hashlib
import http.client
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
import zoneinfo

STAMP = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S")


def canonical_hash(value):
    return "sha256:" + hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.password = endpoint.rstrip("/"), database, password
        self.version = self._rpc("common", "version", [])
        self.uid = self._rpc("common", "login", [database, login, password])

    def _rpc(self, service, method, args):
        payload = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                              "params": {"service": service, "method": method, "args": args}}).encode()
        request = urllib.request.Request(self.endpoint + "/jsonrpc", data=payload, headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                answer = json.loads(response.read())
        except (http.client.HTTPException, urllib.error.URLError, ConnectionError) as error:
            # A dropped connection fails this operation's oracle call, not the run (Odoo 20 drops one
            # when it cannot encode an answer).
            raise RuntimeError(f"transport: {error.__class__.__name__}") from error
        if "error" in answer:
            raise RuntimeError((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        return answer["result"]

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])


def load_renames(path):
    """model -> {baseline field: series field}, from scripts/dev/odoo-dialect-renames.py's evidence.

    The CLI answers in the 19.0 vocabulary; the oracle reads the provider's own. Comparing a renamed
    field means reading the series name the dialect claims carries the same value."""
    if not path:
        return {}
    renames = {}
    for rename in json.load(open(path, encoding="utf-8"))["renames"]:
        renames.setdefault(rename["model"], {})[rename["baseline"]] = rename["series"]
    return renames


def cli(binary, profile, *args):
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    completed = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=180)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT"}}


def sample(oracle, name, meta, related_cache):
    """A value of the field's type the provider will accept, or None when there is none."""
    kind = meta["type"]
    if kind == "html":
        return f"<p>Conformance {name} {STAMP}</p>"
    if kind in ("char", "text"):
        lowered = name.lower()
        if "phone" in lowered or "mobile" in lowered or lowered == "number":
            return "+8491" + STAMP[-7:]
        size = meta.get("size")
        if size:
            # A short code (a currency, an incoterm, a country): upper case, unlikely to exist.
            return ("Q" + STAMP[::-1])[:size].upper()
        return f"Conformance {name} {STAMP}"
    if kind == "integer":
        return 1
    if kind in ("float", "monetary"):
        return 1.0
    if kind == "boolean":
        return True
    if kind == "selection":
        values = [v for v, _ in (meta.get("selection") or [])]
        return values[0] if values else None
    if kind == "date":
        return datetime.date.today().isoformat()
    if kind == "datetime":
        return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    if kind == "many2one":
        target = meta.get("relation")
        if target not in related_cache:
            try:
                found = oracle.call(target, "search", [[]], {"limit": 1, "order": "id"})
            except RuntimeError:
                found = []
            related_cache[target] = found[0] if found else None
        return related_cache[target]
    if kind == "reference":
        # "model,id": the first model the field allows that holds a record, and its first record.
        for target, _ in meta.get("selection") or []:
            key = ("reference", target)
            if key not in related_cache:
                try:
                    found = oracle.call(target, "search", [[]], {"limit": 1, "order": "id"})
                except RuntimeError:
                    found = []
                related_cache[key] = f"{target},{found[0]}" if found else None
            if related_cache[key]:
                return related_cache[key]
    return None


# Values Odoo needs that a generic sample cannot give: a field its create reads although it does not
# declare it required, or a value its business rules constrain. Each takes the oracle and the values
# chosen so far, and applies only when the operation accepts the field.
def _open_pos_session(oracle, values):
    found = oracle.call("pos.session", "search", [[["state", "=", "opened"]]], {"limit": 1})
    return found[0] if found else None


def _first(oracle, model, domain):
    found = oracle.call(model, "search", [domain], {"limit": 1, "order": "id"})
    return found[0] if found else None


def _company_country(oracle):
    company = oracle.call("res.company", "search_read", [[]], {"fields": ["account_fiscal_country_id"], "limit": 1})[0]
    return (company.get("account_fiscal_country_id") or [False])[0]


def _tax_group_of_company_country(oracle, values):
    return _first(oracle, "account.tax.group", [["country_id", "=", values.get("country_id") or _company_country(oracle)]])


def _payment_method_line(oracle, values):
    journal = values.get("journal_id") or _first(oracle, "account.journal", [["type", "=", "bank"]])
    kind = values.get("payment_type") or "inbound"
    return _first(oracle, "account.payment.method.line", [["journal_id", "=", journal], ["payment_type", "=", kind]])


def _calendar_tz(oracle, calendar):
    return oracle.call("resource.calendar", "read", [[calendar[0]], ["tz"]])[0]["tz"] if calendar else None


def _attendance_date(oracle, values):
    """Odoo computes an attendance's date from its check-in in the employee's time zone
    (hr.attendance._compute_date, hr.employee._get_tz). The date written is today there, not on the
    runner's clock: between that zone's midnight and UTC's, a runner's date disagreed (odoo-cli 0.15.0's
    release run wrote at 22:05 UTC, 00:05 in Brussels). The zone, as each series looks for it:
    - 19.0: the work calendar's, the employee's, the company calendar's;
    - 20.0, whose calendars hold no zone: the employee's, the user's, the company's.
    A zone that cannot be read leaves the sampled date, as before."""
    try:
        employee_id = values.get("employee_id") or _first(oracle, "hr.employee", [["user_id", "=", oracle.uid]])
        if not employee_id:
            return None
        if "tz" in oracle.call("resource.calendar", "fields_get", [["tz"]], {"attributes": ["type"]}):
            employee = oracle.call("hr.employee", "read", [[employee_id], ["tz", "resource_calendar_id", "company_id"]])[0]
            tz = _calendar_tz(oracle, employee.get("resource_calendar_id")) or employee.get("tz")
            if not tz and employee.get("company_id"):
                company = oracle.call("res.company", "read", [[employee["company_id"][0]], ["resource_calendar_id"]])[0]
                tz = _calendar_tz(oracle, company.get("resource_calendar_id"))
        else:
            employee = oracle.call("hr.employee", "read", [[employee_id], ["tz", "user_partner_id", "company_id"]])[0]
            tz = employee.get("tz")
            if not tz and employee.get("user_partner_id"):
                tz = oracle.call("res.partner", "read", [[employee["user_partner_id"][0]], ["tz"]])[0]["tz"]
            if not tz and employee.get("company_id"):
                tz = oracle.call("res.company", "read", [[employee["company_id"][0]], ["tz"]])[0]["tz"]
        return datetime.datetime.now(zoneinfo.ZoneInfo(tz or "UTC")).date().isoformat()
    except (RuntimeError, KeyError, IndexError, zoneinfo.ZoneInfoNotFoundError):
        return None


def _inside_event(oracle, values):
    if not values.get("event_id"):
        return None
    event = oracle.call("event.event", "read", [[values["event_id"]], ["date_begin"]])[0]
    return event["date_begin"][:10]


GIVEN_VALUES = {
    ("purchase.requisition.line", "price_unit"): lambda o, v: 10.0,  # read by the line's create
    ("pos.order", "session_id"): _open_pos_session,  # an order belongs to an open session
    ("mailing.filter", "mailing_model_id"): lambda o, v: o.call("ir.model", "search", [[["model", "=", "res.partner"]]])[0],
    ("mailing.filter", "mailing_domain"): lambda o, v: "[]",  # a domain valid for the recipients' model
    ("event.slot", "start_hour"): lambda o, v: 9.0,
    ("event.slot", "end_hour"): lambda o, v: 11.0,  # later than the start
    ("event.slot", "date"): _inside_event,  # inside the event's dates
    ("hr.attendance", "date"): _attendance_date,  # today where the employee works
    ("event.registration.answer", "value_text_box"): lambda o, v: "Conformance answer",
    ("account.account", "code"): lambda o, v: "9" + STAMP[-5:],  # a code for the company it belongs to
    ("account.bank.statement.line", "journal_id"): lambda o, v: _first(o, "account.journal", [["type", "=", "bank"]]),
    ("account.tax", "tax_group_id"): _tax_group_of_company_country,  # the group's country must be the tax's
    ("account.payment", "journal_id"): lambda o, v: _first(o, "account.journal", [["type", "=", "bank"]]),
    ("account.payment", "payment_method_line_id"): _payment_method_line,  # after the journal it belongs to
    ("account.tax", "country_id"): lambda o, v: _company_country(o),  # the country of the tax group below
    ("account.reconcile.model.line", "amount_string"): lambda o, v: "100",  # a percentage of the line, never 0
    ("calendar.recurrence", "rrule"): lambda o, v: "FREQ=WEEKLY;INTERVAL=1;COUNT=2",
    ("sale.order.template.line", "product_id"): lambda o, v: _first(o, "product.product", [["sale_ok", "=", True]]),
    ("sale.order.template.line", "product_uom_id"): lambda o, v: (
        o.call("product.product", "read", [[v["product_id"]], ["uom_id"]])[0]["uom_id"][0] if v.get("product_id") else None),
    ("loyalty.card", "program_id"): lambda o, v: _first(o, "loyalty.program", []),
}

# An update changes one simple field; these fields hold a format, not free text, so an update gives them
# a valid new value instead of a marked text.
GIVEN_UPDATES = {
    ("calendar.recurrence", "rrule"): "FREQ=WEEKLY;INTERVAL=2;COUNT=3",
    ("account.payment.term.line", "days_next_month"): "15",
    ("account.reconcile.model.line", "amount_string"): "50",
    ("crm.team.member", "assignment_domain"): "[]",
    # Lock dates Odoo accepts on a lab: before every demo document, so no unreconciled statement line
    # or draft entry lies in the locked period (res.company._validate_locks).
    ("res.company", "sale_lock_date"): "2020-12-31",
    ("res.company", "purchase_lock_date"): "2020-12-31",
    ("res.company", "tax_lock_date"): "2020-12-31",
    ("res.company", "fiscalyear_lock_date"): "2020-12-31",
}

# Typed fields an update must not pick: changing them makes the record need other fields first.
UPDATE_SKIP = {("product.pricelist.item", "applied_on"), ("product.pricelist.item", "compute_price"),
               ("product.pricelist.item", "base")}


# Odoo's refusals of a record that already exists: the first related record chosen (the first team,
# the first skill) is taken, and another one may not be.
DUPLICATE_REFUSAL = re.compile(r"duplicate|unique|already exist|overlap|only one time|multiple times", re.I)


def alternatives(oracle, values, meta_of, limit=8):
    """Values that differ from these in one many2one each: its second, third or fourth related
    record, field by field, at most limit of them."""
    out = []
    for name in sorted(values):
        field = meta_of(name)
        if field.get("type") != "many2one" or not field.get("relation"):
            continue
        try:
            others = oracle.call(field["relation"], "search", [[]], {"limit": 4, "order": "id"})
        except RuntimeError:
            continue
        for other in others:
            if other != values[name] and len(out) < limit:
                out.append({**values, name: other})
    return out


def scoped(spec, meta):
    """Whether the catalog holds this input to a scope of its own: a prefix, or closed values on a
    field the provider leaves open (not a selection, whose values the provider itself closes)."""
    return bool(spec.get("prefix") or (spec.get("enumValues") and meta.get("type") != "selection"))


def scoped_value(oracle, model, provider_name, spec):
    """A value inside the input's scope: a path under its prefix, or a closed value no record holds
    yet (a scope-held key is unique). None when every closed value is taken."""
    if spec.get("prefix"):
        return f'{spec["prefix"]}conformance-{STAMP}.txt'
    for value in spec["enumValues"]:
        if not oracle.call(model, "search_count", [[[provider_name, "=", value]]]):
            return value
    return None


def update_inputs(described):
    """The update operation's inputs by name."""
    return {f["name"]: f for f in (described or {}).get("inputs") or []}


def simple_fields(accepted, series_meta, inputs):
    """Writable text fields an update can change and read back: `name` first. Never a view's
    architecture (an arbitrary text there is not a view), and never an input the catalog holds to a
    scope (a changed key or url would leave the operation's own records)."""
    def simple(field):
        meta = series_meta(field)
        return (meta.get("type") in ("char", "text") and not meta.get("readonly") and field not in ("arch", "arch_db")
                and not scoped(inputs.get(field, {}), meta))
    candidates = [f for f in ("name",) if f in accepted and simple(f)]
    return candidates + sorted(f for f in accepted if simple(f) and f not in candidates)


TYPED_KINDS = ("integer", "float", "monetary", "boolean", "date", "selection", "many2one")


def typed_fields(accepted, series_meta, inputs):
    """Writable fields of other simple types, for a model without a text field to change: numbers,
    booleans, dates and selections first, then many2one. Never `state` (a state is a button's to
    change) nor an input the catalog holds to a scope."""
    def typed(field):
        meta = series_meta(field)
        # Stored only: a computed field that is not stored may ignore a write and still read back
        # the value by chance.
        return (meta.get("type") in TYPED_KINDS and not meta.get("readonly") and meta.get("store") and field != "state"
                and not scoped(inputs.get(field, {}), meta))
    return sorted((f for f in accepted if typed(f)), key=lambda f: (series_meta(f)["type"] == "many2one", f))


def updatable(model, candidates):
    return [f for f in candidates if (model, f) not in UPDATE_SKIP]


def typed_value(oracle, meta, current):
    """A value of the field's type that differs from the current one, or None when there is none."""
    kind = meta["type"]
    if kind == "integer":
        return (current or 0) + 1
    if kind in ("float", "monetary"):
        return round((current or 0) + 1.5, 2)
    if kind == "boolean":
        return not current
    if kind == "date":
        for days in (7, 8):
            value = (datetime.date.today() + datetime.timedelta(days=days)).isoformat()
            if value != current:
                return value
    if kind == "selection":
        return next((v for v, _ in meta.get("selection") or [] if v != current), None)
    if kind == "many2one":
        held = current[0] if isinstance(current, list) and current else current
        try:
            found = oracle.call(meta["relation"], "search", [[["id", "!=", held or 0]]], {"limit": 1, "order": "id"})
        except RuntimeError:
            return None
        return found[0] if found else None
    return None


def update_value(field, meta=None):
    """A new value for a text field: a dialable number for a phone field, a short code for a field
    with a size (Odoo cuts a longer value), a marked text otherwise."""
    lowered = field.lower()
    if "phone" in lowered or "mobile" in lowered or lowered == "number":
        return "+8492" + STAMP[-7:]
    size = (meta or {}).get("size")
    if size:
        return ("U" + STAMP[::-1])[:size].upper()
    return f"Conformance update {field} {STAMP}"


# Fields Odoo normalises on every write, the same whoever writes: the stored value is compared with
# the written one normalised the same way.
NORMALISED = {
    ("delivery.zip.prefix", "name"): str.upper,  # zip prefixes are stored in upper case
}


def normalised(model, field, value):
    rule = NORMALISED.get((model, field))
    return rule(value) if rule and isinstance(value, str) else value


def same(written, stored, kind):
    if kind == "many2one":
        stored = stored[0] if isinstance(stored, list) and stored else stored
        return stored == written
    if kind in ("float", "monetary"):
        return isinstance(stored, (int, float)) and abs(stored - written) < 1e-6
    return stored == written


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
    parser.add_argument("--renames", help="dialect renames evidence (dialect-renames-<series>.json) for a candidate series")
    parser.add_argument("--readiness", default="UNVERIFIED", choices=["UNVERIFIED", "ANY"],
                        help="which operations to run; the listed readiness is the baseline series', so a candidate series runs ANY")
    parser.add_argument("--only", help="comma-separated models' bases (catalog.pricelist_item): run their create and update "
                                       "and nothing else, to try a fixture without the whole pass")
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")

    oracle = Oracle(options.endpoint, options.database, options.login, password)
    series = oracle.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")
    selection = ["--readiness", "UNVERIFIED"] if options.readiness == "UNVERIFIED" else []
    listing = cli(options.odoo_bin, options.profile, "catalog", "list", "--strategy", "FIELD_WRITE", *selection)
    operations = {item["id"]: item for item in (listing.get("data") or {}).get("items", [])}
    related_cache = {}
    results = {}
    renames = load_renames(options.renames)

    only = {name.strip() for name in (options.only or "").split(",") if name.strip()}
    for operation_id in sorted(i for i in operations if i.endswith(".create")):
        base = operation_id[: -len(".create")]
        if only and base not in only:
            continue
        update_id = base + ".update"
        described = (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", operation_id).get("data") or {})
        model = described.get("providerModel")
        accepted = {f["name"] for f in described.get("inputs") or []}
        modules = described.get("requiredModules") or []
        # The definitions this run exercises; a promoted record is bound to them.
        create_result = {"modules": modules, "definitionHash": described.get("definitionHash", "")}
        results[operation_id] = create_result
        update_described = ((cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", update_id).get("data") or {})
                            if update_id in operations else {})
        update_hash = update_described.get("definitionHash", "")
        update_result = {"modules": modules, "definitionHash": update_hash} if update_id in operations else None
        if update_result is not None:
            results[update_id] = update_result
        try:
            meta = oracle.call(model, "fields_get", [], {"attributes": ["type", "required", "readonly", "store", "relation", "selection", "size"]})
            # A default of False is no default: the field still has to be supplied.
            # Never a binary field's default (an image): Odoo 20 cannot encode it over JSON-RPC and drops
            # the connection.
            defaults = {k: v for k, v in oracle.call(model, "default_get", [sorted(f for f in meta if meta[f]["type"] != "binary")]).items()
                        if v not in (False, None, [], "")}
        except RuntimeError as error:
            create_result.update({"status": "ORACLE_ERROR", "detail": str(error)[:200]})
            if update_result is not None:
                update_result.update({"status": "NOT_RUN_NO_RECORD"})
            continue

        # The CLI takes baseline names; the oracle speaks the provider's. A field the dialect renames
        # is written under its baseline name and read back under the series name it claims.
        to_series = renames.get(model, {})
        to_baseline = {series: baseline for baseline, series in to_series.items()}
        catalog_inputs = {f["name"]: f for f in described.get("inputs") or []}
        # unoffered: required fields the catalog does not take, because Odoo sets them itself (a
        # warehouse's locations). The create is tried all the same; only Odoo's refusal makes it
        # unsatisfiable.
        values, missing, unoffered = {}, [], []
        for name, field in sorted(meta.items()):
            if field.get("required") and field.get("store") and name not in defaults:
                baseline = to_baseline.get(name, name)
                if baseline not in accepted:
                    unoffered.append(baseline)
                    continue
                spec = catalog_inputs.get(baseline, {})
                value = scoped_value(oracle, model, name, spec) if scoped(spec, field) else sample(oracle, name, field, related_cache)
                if value is None:
                    missing.append(baseline)
                    continue
                values[baseline] = value
        # An input the catalog itself requires (a prefix-scoped url, a scope-held key) is supplied too:
        # a prefix-scoped one as a path under its prefix, a closed one as a value no record holds yet.
        for name, spec in sorted(catalog_inputs.items()):
            if not spec.get("required") or name in values or name in missing:
                continue
            provider_name = to_series.get(name, name)
            field = meta.get(provider_name, {})
            value = scoped_value(oracle, model, provider_name, spec) if scoped(spec, field) else (
                sample(oracle, provider_name, field, related_cache) if field else None)
            if value is None:
                missing.append(name)
            else:
                values[name] = value
        if "name" in accepted and "name" not in values and meta.get("name", {}).get("type") in ("char", "text"):
            values["name"] = sample(oracle, "name", meta["name"], related_cache)
        if missing:
            create_result.update({"status": "NOT_RUN_UNSATISFIABLE", "missingFields": missing})
            if update_result is not None:
                update_result.update({"status": "NOT_RUN_NO_RECORD"})
            continue
        if not values:
            # Nothing is required: the create writes one field of its own, a text field if it has one.
            def create_meta(field):
                return meta.get(to_series.get(field, field), {})
            for own in (simple_fields(accepted, create_meta, catalog_inputs) + typed_fields(accepted, create_meta, catalog_inputs)):
                value = sample(oracle, to_series.get(own, own), create_meta(own), related_cache)
                if value is not None:
                    values[own] = value
                    break
        if not values:
            create_result.update({"status": "NOT_RUN_NOTHING_TO_WRITE"})
            if update_result is not None:
                update_result.update({"status": "NOT_RUN_NO_RECORD"})
            continue

        for (given_model, given_field), give in sorted(GIVEN_VALUES.items()):
            if given_model == model and given_field in accepted:
                given = give(oracle, values)
                if given is not None:
                    values[given_field] = given
        answer = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                     "--values", json.dumps(values), "--allow-unverified")
        # A refusal of a duplicate: the same create with another related record, one field at a time.
        if answer.get("status") != "OK" and DUPLICATE_REFUSAL.search((answer.get("error") or {}).get("message") or ""):
            for attempt, other in enumerate(alternatives(oracle, values, lambda f: meta.get(to_series.get(f, f), {})), 1):
                retry = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                            "--values", json.dumps(other), "--allow-unverified")
                if retry.get("status") == "OK":
                    answer, values = retry, other
                    create_result["relatedRetries"] = attempt
                    break
        created = ((answer.get("data") or {}).get("data") or {}) if answer.get("status") == "OK" else {}
        record_id = created.get("id") if isinstance(created, dict) else None
        if not record_id:
            create_result.update({"status": "NOT_RUN_UNSATISFIABLE" if unoffered else "CLI_ERROR",
                                  "detail": ((answer.get("error") or {}).get("code") or "NO_ID"),
                                  "message": ((answer.get("error") or {}).get("message") or "")[:200]})
            if unoffered:
                create_result["missingFields"] = unoffered
            if update_result is not None:
                update_result.update({"status": "NOT_RUN_NO_RECORD"})
            continue
        stored = oracle.call(model, "read", [[record_id], sorted({to_series.get(f, f) for f in values})])[0]
        mismatched = [f for f in sorted(values) if not same(normalised(model, f, values[f]), stored.get(to_series.get(f, f)),
                                                            meta[to_series.get(f, f)]["type"])]
        create_result.update({"kind": "create", "writtenFields": sorted(values), "mismatchedFields": mismatched,
                              "renamedFields": sorted(f for f in values if f in to_series),
                              "valuesHash": canonical_hash(values), "status": "DISAGREE" if mismatched else "AGREE"})

        if update_result is None:
            continue
        def series_meta(field):
            return meta.get(to_series.get(field, field), {})
        # The update's own inputs: a field only the create accepts (a scope-held key) is not the update's to change.
        update_accepts = update_inputs(update_described)
        candidates = simple_fields(accepted & set(update_accepts), series_meta, update_accepts)
        typed = not candidates
        if typed:
            candidates = updatable(model, typed_fields(accepted & set(update_accepts), series_meta, update_accepts))
        if not candidates:
            update_result.update({"status": "NOT_RUN_NO_SIMPLE_FIELD"})
            continue
        field = candidates[0]
        if typed:
            current = oracle.call(model, "read", [[record_id], [to_series.get(field, field)]])[0].get(to_series.get(field, field))
            new_value = typed_value(oracle, series_meta(field), current)
            if new_value is None:
                update_result.update({"status": "NOT_RUN_NO_SIMPLE_FIELD"})
                continue
        else:
            new_value = GIVEN_UPDATES.get((model, field)) or update_value(field, series_meta(field))
        answer = cli(options.odoo_bin, options.profile, "execute", "--operation", update_id, "--provider-target-id", str(record_id),
                     "--values", json.dumps({field: new_value}), "--allow-unverified")
        if answer.get("status") != "OK":
            update_result.update({"status": "CLI_ERROR", "detail": (answer.get("error") or {}).get("code"),
                                  "message": ((answer.get("error") or {}).get("message") or "")[:200]})
            continue
        stored = oracle.call(model, "read", [[record_id], [to_series.get(field, field)]])[0]
        landed = same(normalised(model, field, new_value), stored.get(to_series.get(field, field)), series_meta(field)["type"])
        update_result.update({"kind": "update", "writtenFields": [field], "fieldType": series_meta(field)["type"],
                              "status": "AGREE" if landed else "DISAGREE"})

    # An update whose create made no record (refused, unsatisfiable, nothing to write) falls back to
    # the path below: the newest existing record inside its domain, updated and restored.
    for update_id in [i for i, r in results.items() if i.endswith(".update") and r.get("status") == "NOT_RUN_NO_RECORD"]:
        del results[update_id]

    # An update without a create beside it is a record Odoo provisions (the company) or a curated
    # family's missing verb: one simple field of the newest existing record is updated through the
    # CLI, read back by the oracle, and restored, so the lab keeps its data.
    for update_id in sorted(i for i in operations if i.endswith(".update") and i not in results):
        if only and update_id[: -len(".update")] not in only:
            continue
        described = (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", update_id).get("data") or {})
        model = described.get("providerModel")
        accepted = {f["name"] for f in described.get("inputs") or []}
        result = {"modules": described.get("requiredModules") or [], "definitionHash": described.get("definitionHash", "")}
        results[update_id] = result
        to_series = renames.get(model, {})
        try:
            meta = oracle.call(model, "fields_get", [], {"attributes": ["type", "readonly", "store", "size", "relation", "selection"]})
        except RuntimeError as error:
            result.update({"status": "ORACLE_ERROR", "detail": str(error)[:200]})
            continue
        def series_meta(field):
            return meta.get(to_series.get(field, field), {})
        candidates = simple_fields(accepted, series_meta, update_inputs(described))
        typed = not candidates
        if typed:
            candidates = updatable(model, typed_fields(accepted, series_meta, update_inputs(described)))
        if not candidates:
            result.update({"status": "NOT_RUN_NO_SIMPLE_FIELD"})
            continue
        field = candidates[0]
        provider_field = to_series.get(field, field)
        # The newest record inside the operation's provider domain: one outside it is refused, rightly.
        existing = oracle.call(model, "search_read", [described.get("providerDomain") or []],
                               {"fields": [provider_field], "limit": 1, "order": "id desc"})
        if not existing:
            result.update({"status": "NOT_RUN_NO_RECORD"})
            continue
        record_id, original = existing[0]["id"], existing[0].get(provider_field)
        new_value = (typed_value(oracle, meta[provider_field], original) if typed
                     else GIVEN_UPDATES.get((model, field)) or update_value(field, meta.get(provider_field)))
        if new_value is None:
            result.update({"status": "NOT_RUN_NO_SIMPLE_FIELD"})
            continue
        if isinstance(original, list):
            original = original[0] if original else False
        answer = cli(options.odoo_bin, options.profile, "execute", "--operation", update_id, "--provider-target-id", str(record_id),
                     "--values", json.dumps({field: new_value}), "--allow-unverified")
        if answer.get("status") != "OK":
            result.update({"status": "CLI_ERROR", "detail": (answer.get("error") or {}).get("code"),
                           "message": ((answer.get("error") or {}).get("message") or "")[:200]})
            continue
        stored = oracle.call(model, "read", [[record_id], [provider_field]])[0]
        oracle.call(model, "write", [[record_id], {provider_field: original}])
        landed = same(normalised(model, field, new_value), stored.get(provider_field), meta[provider_field]["type"])
        result.update({"kind": "update-existing", "writtenFields": [field], "restored": True, "fieldType": meta[provider_field]["type"],
                       "status": "AGREE" if landed else "DISAGREE"})

    evidence = {
        "schemaVersion": "odoo.write-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": "create through the CLI with every required, non-defaulted field (related records: the provider's "
                  "first of the target model), read back by an independent JSON-RPC oracle and compared value by value; "
                  "then one simple field updated through the CLI and read back; an update with no create beside it updates "
                  "one simple field of the newest existing record inside the operation's provider domain, reads it back and restores it. "
                  "Inputs the catalog holds to a scope are written inside it. A field the dialect renames is written "
                  "under its baseline name and read back under the series name. Field names, statuses and hashes only.",
        # The file's name, not its path: evidence is published, and a path names the machine's folders.
        "renames": os.path.basename(options.renames) if options.renames else None,
        "records": results,
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    counts = {}
    for result in results.values():
        counts[result.get("status", "UNKNOWN")] = counts.get(result.get("status", "UNKNOWN"), 0) + 1
    print(f"{series}: {len(results)} UNVERIFIED writes -> {counts}", file=sys.stderr)


if __name__ == "__main__":
    main()
