#!/usr/bin/env python3
"""Record the module list of a stock Odoo image: the baseline the provider-fit check compares with.

Conformance evidence proves an operation on one environment, the stock Community image of a series
(DEC-2026-0157). Before acting, the CLI classifies every module installed on the Odoo it reaches as
standard (shipped by that image), Enterprise or third-party. This script records what "shipped by
that image" means, from a lab running the image with nothing on its extra addons path.

It reads every module the image knows (`ir.module.module`, any state):
  * standard: everything except the Enterprise placeholders;
  * enterprise: the placeholders a Community image advertises (`to_buy`) and anything licensed
    OEEL-1. An installed module outside both lists is third-party.

Authenticate with a password over JSON-RPC (--password-env) or with an API key over JSON-2
(--api-key-file). The output holds module names and the image only, no credential.

    ODOO_ORACLE_PASSWORD=... scripts/dev/odoo-standard-modules.py --endpoint http://127.0.0.1:58718 \\
        --database lab19demo --login admin --series 19.0 --image docker.io/library/odoo@sha256:... \\
        --out packages/odoo/odoo_standard_modules.json
"""

import argparse
import datetime
import json
import os
import urllib.request


def jsonrpc(endpoint, service, method, args):
    body = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                       "params": {"service": service, "method": method, "args": args}}).encode()
    request = urllib.request.Request(endpoint.rstrip("/") + "/jsonrpc", body, {"Content-Type": "application/json"})
    answer = json.loads(urllib.request.urlopen(request, timeout=120).read())
    if "error" in answer:
        raise SystemExit(((answer["error"].get("data") or {}).get("message") or "provider error")[:200])
    return answer.get("result")


def modules(options):
    fields = ["name", "license", "to_buy"]
    if options.api_key_file:
        with open(options.api_key_file, encoding="utf-8") as handle:
            key = handle.read().strip()
        request = urllib.request.Request(
            options.endpoint.rstrip("/") + "/json/2/ir.module.module/search_read",
            json.dumps({"domain": [], "fields": fields}).encode(),
            {"Content-Type": "application/json", "Authorization": "bearer " + key, "X-Odoo-Database": options.database})
        return json.loads(urllib.request.urlopen(request, timeout=120).read())
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env} or pass --api-key-file")
    uid = jsonrpc(options.endpoint, "common", "login", [options.database, options.login, password])
    if not uid:
        raise SystemExit("the login was refused")
    return jsonrpc(options.endpoint, "object", "execute_kw",
                   [options.database, uid, password, "ir.module.module", "search_read", [[]], {"fields": fields}])


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for name in ("--endpoint", "--database", "--series", "--image", "--out"):
        parser.add_argument(name, required=True)
    parser.add_argument("--login", default="admin")
    parser.add_argument("--password-env", default="ODOO_ORACLE_PASSWORD")
    parser.add_argument("--api-key-file")
    options = parser.parse_args()

    version = jsonrpc(options.endpoint, "common", "version", [])
    if version.get("server_serie") != options.series:
        raise SystemExit(f"the provider runs {version.get('server_serie')}, not {options.series}")
    records = modules(options)
    enterprise = sorted({r["name"] for r in records if r.get("to_buy") or r.get("license") == "OEEL-1"})
    standard = sorted({r["name"] for r in records} - set(enterprise))

    document = {"schemaVersion": "odoo.standard-modules/v1", "series": {}}
    if os.path.exists(options.out):
        with open(options.out, encoding="utf-8") as handle:
            document = json.load(handle)
    document["series"][options.series] = {
        "image": options.image,
        "serverVersion": version.get("server_version"),
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "standard": standard,
        "enterprise": enterprise,
    }
    with open(options.out, "w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=1, sort_keys=True)
        handle.write("\n")
    print(f"{options.series}: {len(standard)} standard, {len(enterprise)} enterprise placeholders -> {options.out}")


if __name__ == "__main__":
    main()
