#!/usr/bin/env python3
"""Conformance evidence for the Settings page as code: structure.settings.describe and .update.

The action runner (odoo-action-conformance.py) reaches a dialog through a record and its button; the
Settings page has neither, so it never ran structure.settings.update. This runner drives the page
through the real standalone CLI, one setting at a time, against an independent JSON-RPC oracle that
opens the page as the web client does (a new res.config.settings record):

  * structure.settings.describe: every setting the CLI reports must equal the oracle's value, before
    and after each change;
  * structure.settings.update with a value other than the current one (the other boolean, another
    choice, another number): the oracle must then open the page with that value; then the CLI
    restores the first value and the oracle must see it again.

When Odoo refuses a value through the CLI, the oracle saves the same value through the page itself:
if Odoo refuses it there too, the CLI behaved as the page does and the setting agrees (listed in
refusedByBoth); if the page accepts it, the setting disagrees and the oracle restores it.

An operation AGREEs only when every setting it offers agreed. On the baseline series (19.0) every
setting must exist on the database: a missing one means the lab lacks its module. On a candidate
series, a setting the series removed agrees when the CLI's read leaves it out and its write is
refused, as the page has no such field (listed in absentOnThisSeries). The evidence holds statuses,
setting names and hashes only: no credential, no value. It promotes nothing. Run it on a lab: it
changes each setting and puts it back.

The oracle signs in with an API key (ODOO_ORACLE_PASSWORD), not a password: once two-step sign-in is
required of employees (auth_totp_policy), Odoo refuses an employee's password over RPC, and the run
could no longer read what it set.

    ODOO_ORACLE_PASSWORD=<api key> AERP_CREDENTIAL_LAB19=... scripts/dev/odoo-settings-conformance.py \\
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

OPERATIONS = ("structure.settings.describe", "structure.settings.update")
MODEL = "res.config.settings"
BASELINE = "19.0"


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
        with urllib.request.urlopen(request, timeout=300) as response:
            answer = json.loads(response.read())
        if "error" in answer:
            raise RuntimeError((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        return answer["result"]

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])

    def page(self, names):
        """The values the Settings page opens with."""
        record = self.call(MODEL, "create", [{}])
        return self.call(MODEL, "read", [[record], names])[0]

    def save(self, values):
        """The page's Save with these values."""
        record = self.call(MODEL, "create", [values])
        self.call(MODEL, "execute", [[record]])


def cli(binary, profile, *args):
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    completed = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=300)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT"}}


def same(a, b):
    """Odoo answers an unset choice as False; numbers compare to the cent."""
    if a in (None, False) and b in (None, False):
        return True
    if isinstance(a, (int, float)) and isinstance(b, (int, float)) and not isinstance(a, bool) and not isinstance(b, bool):
        return round(float(a), 2) == round(float(b), 2)
    return a == b


def another(meta, value):
    kind = meta["type"]
    if kind == "boolean":
        return not value
    if kind == "selection":
        options = [key for key, _ in meta["selection"] if key != value]
        return options[0] if options else None
    if kind == "integer":
        return (value or 0) + 1
    if kind in ("float", "monetary"):
        return round((value or 0) + 1000, 2)
    return None


class Checks:
    def __init__(self):
        self.fields, self.failures, self.refused, self.absent = {}, [], [], []

    def same(self, field, expected, observed, why=""):
        agreed = same(expected, observed)
        self.fields[field] = self.fields.get(field, True) and agreed
        if not agreed:
            self.failures.append(f"{field}{': ' + why if why else ''}")
        return agreed

    def result(self, modules, definition_hash):
        failures = sorted(set(self.failures))
        status = "AGREE" if self.fields and not failures else ("DISAGREE" if self.fields else "NOT_RUN")
        out = {"status": status, "modules": modules, "definitionHash": definition_hash,
               "checkedFields": sorted(self.fields), "failures": failures}
        if self.refused:
            out["refusedByBoth"] = sorted(self.refused)
        if self.absent:
            out["absentOnThisSeries"] = sorted(self.absent)
        return out


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
    parser.add_argument("--only", help="comma-separated settings: run these and no others (the operations do not AGREE then)")
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")
    oracle = Oracle(options.endpoint, options.database, options.login, password)
    series = oracle.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")

    described = {op: (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", op).get("data") or {})
                 for op in OPERATIONS}
    inputs = {item["name"]: item for item in described["structure.settings.update"].get("inputs") or []}
    offered = sorted(inputs)
    if not offered:
        raise SystemExit("the CLI describes no settings for structure.settings.update")
    known = oracle.call(MODEL, "fields_get", [offered], {"attributes": ["type", "selection"]})
    absent = [name for name in offered if name not in known]
    present = [name for name in offered if name in known]
    only = set(options.only.split(",")) if options.only else None
    checks = {op: Checks() for op in OPERATIONS}

    def described_now():
        answer = cli(options.odoo_bin, options.profile, "execute", "--operation", "structure.settings.describe",
                     "--allow-unverified")
        data = (answer.get("data") or {}).get("data") if answer.get("status") == "OK" else None
        return data[0] if isinstance(data, list) and data else None

    def update(values):
        return cli(options.odoo_bin, options.profile, "execute", "--operation", "structure.settings.update",
                   "--values", json.dumps(values), "--allow-unverified")

    first = oracle.page(present)
    reported = described_now()
    if reported is None:
        checks["structure.settings.describe"].failures.append("the describe did not answer")
    else:
        for name in present:
            checks["structure.settings.describe"].same(name, first[name], reported.get(name), "first read")

    # A setting the database does not have: a lab missing its module on the baseline, a removal on a
    # candidate series, where the CLI must leave it out of the read and have its write refused.
    for name in absent:
        if series == BASELINE:
            for op in OPERATIONS:
                checks[op].failures.append(f"{name}: not on this database (the lab lacks its module)")
            continue
        enums = inputs[name].get("enum") or inputs[name].get("values") or []
        value = {"boolean": True, "integer": 1, "number": 1.0}.get(inputs[name].get("type"), enums[0] if enums else "x")
        answer = update({name: value})
        # Refused by Odoo for the field itself (Invalid field 'name'), not for something else.
        refused = answer.get("status") != "OK" and name in ((answer.get("error") or {}).get("message") or "")
        for op, agreed, why in (("structure.settings.describe", reported is not None and name not in reported, "the read reports it"),
                                ("structure.settings.update", refused, "the write was not refused for the field")):
            checks[op].fields[name] = checks[op].fields.get(name, True) and agreed
            if agreed:
                checks[op].absent.append(name)
            else:
                checks[op].failures.append(f"{name}: not on this series, but {why}")

    for name in present:
        if only is not None and name not in only:
            continue
        before = oracle.page([name])[name]
        wanted = another(known[name], before)
        if wanted is None:
            checks["structure.settings.update"].failures.append(f"{name}: no other value to set")
            continue
        answer = update({name: wanted})
        if answer.get("status") != "OK":
            # Odoo's refusal through the CLI: the page itself must refuse the same value.
            try:
                oracle.save({name: wanted})
            except RuntimeError:
                checks["structure.settings.update"].fields[name] = checks["structure.settings.update"].fields.get(name, True)
                checks["structure.settings.update"].refused.append(name)
                continue
            checks["structure.settings.update"].same(name, "refused", "accepted by the page", "the CLI was refused")
            oracle.save({name: before})
            continue
        after = oracle.page([name])[name]
        checks["structure.settings.update"].same(name, wanted, after, "set")
        reported = described_now()
        checks["structure.settings.describe"].same(name, after, (reported or {}).get(name), "read after the change")
        restored = update({name: before})
        if restored.get("status") != "OK":
            checks["structure.settings.update"].failures.append(f"{name}: the CLI could not restore it")
            oracle.save({name: before})
            continue
        checks["structure.settings.update"].same(name, before, oracle.page([name])[name], "restore")

    results = {}
    for op in OPERATIONS:
        result = checks[op].result(described[op].get("requiredModules") or [], described[op].get("definitionHash", ""))
        if only is not None and result["status"] == "AGREE":
            result["status"], result["failures"] = "PARTIAL", ["--only ran a subset of the settings"]
        results[op] = result
    evidence = {
        "schemaVersion": "odoo.settings-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": ("each setting the CLI offers on the Settings page, read through the CLI and through an independent "
                   "JSON-RPC oracle that opens the page as the web client does (a new res.config.settings); set to "
                   "another value through the CLI, read back by both, then restored through the CLI and read back. A "
                   "value Odoo refuses through the CLI is saved through the page itself: refused there too, the "
                   "setting agrees. Setting names, statuses and hashes only; every setting was put back."),
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
        "records": results,
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    for op, result in results.items():
        print(f"{series} {op}: {result['status']} {result['failures'] or ''} {result.get('refusedByBoth') or ''}",
              file=sys.stderr)


if __name__ == "__main__":
    main()
