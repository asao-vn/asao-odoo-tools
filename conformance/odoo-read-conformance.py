#!/usr/bin/env python3
"""Conformance evidence for UNVERIFIED read operations, against an independent provider oracle.

For every UNVERIFIED READ operation of the standalone catalog (curated or surface), this runner:

  * runs it through the real standalone CLI (`odoo execute ... --allow-unverified`), and
  * asks the provider directly over JSON-RPC, through code that shares nothing with the CLI's
    execution path, for the same records (within the fixed provider domain the catalog publishes
    for the operation, if it has one; for a describe of a new record, such as the Settings page, a
    record of the transient model created and read back),

then records whether the two AGREE: a list returns the same ids in the same order (both sorted by
id); a describe returns, for every field the CLI reported, exactly the value the provider reports.
An operation whose model holds no record can only agree on emptiness (AGREE_EMPTY); that proves
the call works, not what it returns, and is never offered for promotion.

The evidence document holds statuses, counts and hashes only: no credential, no record content.
It promotes nothing. Adding a CURRENT_VERIFIED record to a conformance manifest stays a reviewed
source change (docs/architecture/provider-research/odoo19/empirical-research/odoo19-cli-conformance.md).

    ODOO_ORACLE_PASSWORD=... AERP_CREDENTIAL_LAB19=... scripts/dev/odoo-read-conformance.py \\
        --odoo-bin ./odoo --profile lab19 --endpoint http://127.0.0.1:58719 --database lab19 \\
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

LIMIT = 5


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
        with urllib.request.urlopen(request, timeout=120) as response:
            answer = json.loads(response.read())
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
    # Only execution reaches a provider; catalog commands take no profile.
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    completed = subprocess.run([binary, *args, *selection, "--output", "json"],
                               capture_output=True, text=True, timeout=180)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT"}}


def provider_shaped(value, provider_values=None):
    """A curated answer's value in the shape a provider read returns: a relation {id, display_name} as
    [id, name], a list of {id} as ids, a semantic enum value in the provider's spelling."""
    if isinstance(value, dict) and "id" in value:
        return [value["id"], value.get("display_name")] if "display_name" in value else value["id"]
    if isinstance(value, list) and value and all(isinstance(v, dict) and "id" in v for v in value):
        return [v["id"] for v in value]
    if isinstance(value, str) and provider_values:
        return provider_values.get(value, value)
    return value


def same_value(cli_value, oracle_value):
    """Equal, an empty relation (False in a read, null in an answer) included."""
    if cli_value is None and oracle_value is False:
        return True
    return cli_value == oracle_value


def records_of(document):
    data = (document.get("data") or {}).get("data")
    return data if isinstance(data, list) else []


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
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")

    oracle = Oracle(options.endpoint, options.database, options.login, password)
    series = oracle.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")
    selection = ["--readiness", "UNVERIFIED"] if options.readiness == "UNVERIFIED" else []
    listing = cli(options.odoo_bin, options.profile, "catalog", "list", "--strategy", "READ", *selection)
    operation_ids = sorted(item["id"] for item in (listing.get("data") or {}).get("items", []))
    installed = {r["name"] for r in oracle.call("ir.module.module", "search_read", [[["state", "=", "installed"]]], {"fields": ["name"]})}
    renames = load_renames(options.renames)

    results = {}
    for operation_id in operation_ids:
        described = (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", operation_id).get("data") or {})
        model = described.get("providerModel")
        modules = described.get("requiredModules") or []
        # The definition this run exercises; a promoted record is bound to it.
        result = {"modules": modules, "definitionHash": described.get("definitionHash", "")}
        results[operation_id] = result
        if not model:
            result["status"] = "NOT_RUN_NO_PROVIDER_MODEL"
            continue
        if any(module not in installed for module in modules):
            result["status"] = "UNSUPPORTED_MODULE"
            continue
        # The oracle selects within the operation's fixed provider domain, as the catalog publishes it
        # (an attachment list's res_model, a journal entry's move_type). Without it, it would select a
        # superset: the list would DISAGREE and a describe could pick a record the CLI must refuse.
        domain = described.get("providerDomain") or []
        result["providerDomain"] = bool(domain)
        try:
            oracle_ids = [r["id"] for r in oracle.call(model, "search_read", [domain], {"fields": ["id"], "limit": LIMIT, "order": "id"})]
        except RuntimeError as error:
            result["status"] = "ORACLE_ERROR"
            result["detail"] = str(error)[:200]
            continue
        if operation_id.endswith(".describe") and described.get("targetRequirement") == "NONE":
            # A describe of a NEW record (the Settings page): what Odoo shows before anything is saved.
            # The oracle creates a record of the model and reads it back; only a transient model is
            # created this way (res.config.settings saves nothing by being created, only by `execute`).
            transient = oracle.call("ir.model", "search_read", [[["model", "=", model]]], {"fields": ["transient"]})
            if not transient or not transient[0].get("transient"):
                result["status"] = "NOT_RUN_NEW_RECORD_NOT_TRANSIENT"
                continue
            answer = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id, "--allow-unverified")
            records = records_of(answer)
            if answer.get("status") != "OK" or len(records) != 1:
                result["status"] = "CLI_ERROR"
                result["detail"] = (answer.get("error") or {}).get("code")
                continue
            cli_record = records[0]
            try:
                provider_fields = set(oracle.call(model, "fields_get", [], {"attributes": ["type"]}))
                fields = sorted(f for f in cli_record if f in provider_fields and f != "id")
                unchecked = sorted(f for f in cli_record if f not in provider_fields and f != "id")
                new_id = oracle.call(model, "create", [{}])
                oracle_record = oracle.call(model, "read", [[new_id], fields])[0]
            except RuntimeError as error:
                result["status"] = "ORACLE_ERROR"
                result["detail"] = str(error)[:200]
                continue
            mismatched = [f for f in fields if cli_record.get(f) != oracle_record.get(f)]
            status = "DISAGREE" if mismatched else ("AGREE_WITH_UNCHECKED_FIELDS" if unchecked else "AGREE")
            result.update({"kind": "describe-new", "fieldCount": len(fields), "cliHash": canonical_hash({f: cli_record.get(f) for f in fields}),
                           "oracleHash": canonical_hash({f: oracle_record.get(f) for f in fields}), "mismatchedFields": mismatched,
                           "uncheckedFields": unchecked, "renamedFields": [], "status": status})
            continue
        if operation_id.endswith(".describe") or described.get("targetRequirement") == "REQUIRED":
            if not oracle_ids:
                result["status"] = "NOT_RUN_NO_RECORD"
                continue
            target = oracle_ids[0]
            answer = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                         "--provider-target-id", str(target), "--allow-unverified")
            records = records_of(answer)
            if answer.get("status") != "OK" or len(records) != 1:
                result["status"] = "CLI_ERROR"
                result["detail"] = (answer.get("error") or {}).get("code")
                continue
            # A curated describe speaks the contract's names (stock_managed for is_storable) and enum
            # values (goods for consu), and shapes a relation as {id, display_name}: both are read back
            # through what `catalog describe` publishes, and a relation is compared by ids and names.
            outputs = {f["name"]: f for f in described.get("outputs") or [] if f.get("providerField")}
            cli_record = {outputs[k]["providerField"] if k in outputs else k: provider_shaped(v, (outputs.get(k) or {}).get("providerValues"))
                          for k, v in records[0].items()}
            # The oracle reads only what the provider itself has. A field the CLI reports that the
            # provider does not (one a series dialect rebuilds, e.g. 20.0's uom.uom.rounding) is not
            # checked here, and an operation with such a field is not offered for promotion.
            # A field the dialect renames is compared with the series field it claims carries the value.
            series_name = renames.get(model, {})
            try:
                provider_fields = set(oracle.call(model, "fields_get", [], {"attributes": ["type"]}))
                fields = sorted(f for f in cli_record if series_name.get(f, f) in provider_fields)
                unchecked = sorted(f for f in cli_record if series_name.get(f, f) not in provider_fields)
                oracle_record = oracle.call(model, "read", [[target], sorted({series_name.get(f, f) for f in fields})])[0]
            except RuntimeError as error:
                result["status"] = "ORACLE_ERROR"
                result["detail"] = str(error)[:200]
                continue
            mismatched = [f for f in fields if not same_value(cli_record.get(f), oracle_record.get(series_name.get(f, f)))]
            renamed = sorted(f for f in fields if f in series_name)
            status = "DISAGREE" if mismatched else ("AGREE_WITH_UNCHECKED_FIELDS" if unchecked else "AGREE")
            result.update({"kind": "describe", "fieldCount": len(fields), "cliHash": canonical_hash(cli_record),
                           "oracleHash": canonical_hash(oracle_record), "mismatchedFields": mismatched,
                           "uncheckedFields": unchecked, "renamedFields": renamed, "status": status})
        else:
            answer = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                         "--limit", str(LIMIT), "--order", "id", "--allow-unverified")
            if answer.get("status") != "OK":
                result["status"] = "CLI_ERROR"
                result["detail"] = (answer.get("error") or {}).get("code")
                continue
            cli_ids = [r.get("id") for r in records_of(answer)]
            result.update({"kind": "list", "recordCount": len(cli_ids), "cliHash": canonical_hash(cli_ids),
                           "oracleHash": canonical_hash(oracle_ids)})
            if cli_ids != oracle_ids:
                result["status"] = "DISAGREE"
            else:
                result["status"] = "AGREE" if cli_ids else "AGREE_EMPTY"

    evidence = {
        "schemaVersion": "odoo.read-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": "CLI execution vs independent JSON-RPC oracle, within the operation's published provider domain; ids for lists (order id, limit 5), "
                  "field-by-field values for describes (a field the dialect renames against the series field it "
                  "names). Hashes and statuses only.",
        "renames": options.renames or None,
        "records": results,
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    counts = {}
    for result in results.values():
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    print(f"{series}: {len(results)} UNVERIFIED reads -> {counts}", file=sys.stderr)


if __name__ == "__main__":
    main()
