#!/usr/bin/env python3
"""Conformance evidence for UNVERIFIED print operations, against the browser's own download.

A print operation (StrategyReport) claims to print what the browser prints. For every UNVERIFIED
REPORT operation of the standalone catalog, this runner:

  * picks the newest record inside the operation's provider domain;
  * prints it through the real standalone CLI (`odoo execute ... --allow-unverified`);
  * downloads the same report of the same record the way the browser does, through code that shares
    nothing with the CLI's: a web session opened with /web/session/authenticate, then
    GET /report/pdf/<report>/<id>, the session closed afterwards;
  * compares the two documents: both PDFs, the same page count, the same text (pdftotext -layout).

AGREE means both are PDFs with the same pages and the same text. Other outcomes are never offered
for promotion: DISAGREE (the documents differ), CLI_ERROR, ORACLE_ERROR, NOT_RUN_NO_RECORD.

The evidence holds statuses, page counts, sizes and hashes only: no credential and no document
content. The PDFs exist only in a temporary directory while they are compared. Adding the AGREE
results to a manifest is the reviewed step scripts/dev/odoo-promote-conformance.py.

    ODOO_ORACLE_PASSWORD=... AERP_CREDENTIAL_LAB19DEMO=... scripts/dev/odoo-report-conformance.py \\
        --odoo-bin ./odoo --profile lab19demo --endpoint http://127.0.0.1:58718 --database lab19demo \\
        --login admin --series 19.0 --image-digest sha256:... --evidence report-run.json
"""

import argparse
import base64
import datetime
import hashlib
import http.cookiejar
import json
import os
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request


def canonical_hash(value):
    data = value if isinstance(value, bytes) else json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return "sha256:" + hashlib.sha256(data).hexdigest()


class Oracle:
    """JSON-RPC, for finding the record to print."""

    def __init__(self, endpoint, database, login, password):
        self.url, self.database, self.password = endpoint.rstrip("/") + "/jsonrpc", database, password
        self.version = self._rpc("common", "version", [])
        self.uid = self._rpc("common", "login", [database, login, password])
        if not self.uid:
            raise SystemExit("the oracle login was refused")

    def _rpc(self, service, method, args):
        body = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                           "params": {"service": service, "method": method, "args": args}}).encode()
        answer = json.loads(urllib.request.urlopen(urllib.request.Request(self.url, body, {"Content-Type": "application/json"}),
                                                   timeout=120).read())
        if "error" in answer:
            raise RuntimeError(((answer["error"].get("data") or {}).get("message") or "")[:200])
        return answer.get("result")

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """A redirect from a report route is the login page: refused, never followed."""

    def redirect_request(self, *args, **kwargs):
        return None


class Browser:
    """What the browser does to download a report, written independently of the CLI."""

    def __init__(self, endpoint, database, login, password):
        self.endpoint = endpoint.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), NoRedirect())
        answer = self._json("/web/session/authenticate", {"db": database, "login": login, "password": password})
        if not (answer.get("result") or {}).get("uid"):
            raise SystemExit("the web login was refused")

    def _json(self, path, params):
        body = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1, "params": params}).encode()
        request = urllib.request.Request(self.endpoint + path, body, {"Content-Type": "application/json"})
        return json.loads(self.opener.open(request, timeout=120).read())

    def pdf(self, report, record_id):
        try:
            response = self.opener.open(f"{self.endpoint}/report/pdf/{report}/{record_id}", timeout=300)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"HTTP {error.code}") from None
        if not response.headers.get("Content-Type", "").startswith("application/pdf"):
            raise RuntimeError("not a PDF: " + response.headers.get("Content-Type", ""))
        return response.read()

    def close(self):
        try:
            self._json("/web/session/destroy", {})
        except (urllib.error.URLError, ValueError):
            pass


def cli(binary, profile, *args):
    # Only execution reaches a provider; catalog commands take no profile.
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    done = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=600)
    try:
        return json.loads(done.stdout)
    except ValueError:
        return {"status": "ERROR", "error": {"code": "CLI_OUTPUT_UNREADABLE"}}


def inspect(pdf, directory, name):
    """Page count and layout text of a PDF, read with poppler."""
    path = os.path.join(directory, name)
    with open(path, "wb") as out:
        out.write(pdf)
    info = subprocess.run(["pdfinfo", path], capture_output=True, text=True, timeout=120).stdout
    pages = next((int(line.split()[-1]) for line in info.splitlines() if line.startswith("Pages:")), 0)
    text = subprocess.run(["pdftotext", "-layout", path, "-"], capture_output=True, text=True, timeout=120).stdout
    return pages, text


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for name in ("--odoo-bin", "--profile", "--endpoint", "--database", "--login", "--series", "--image-digest", "--evidence"):
        parser.add_argument(name, required=True)
    parser.add_argument("--password-env", default="ODOO_ORACLE_PASSWORD")
    parser.add_argument("--readiness", default="UNVERIFIED", choices=["UNVERIFIED", "ANY"])
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")

    oracle = Oracle(options.endpoint, options.database, options.login, password)
    series = oracle.version.get("server_serie")
    if series != options.series:
        raise SystemExit(f"the provider runs {series}, not {options.series}")
    selection = ["--readiness", "UNVERIFIED"] if options.readiness == "UNVERIFIED" else []
    listing = cli(options.odoo_bin, options.profile, "catalog", "list", "--strategy", "REPORT", *selection)
    operation_ids = sorted(item["id"] for item in (listing.get("data") or {}).get("items", []))
    installed = {r["name"] for r in oracle.call("ir.module.module", "search_read", [[["state", "=", "installed"]]], {"fields": ["name"]})}
    browser = Browser(options.endpoint, options.database, options.login, password)

    results = {}
    try:
        with tempfile.TemporaryDirectory() as directory:
            for operation_id in operation_ids:
                described = (cli(options.odoo_bin, options.profile, "catalog", "describe", "--operation", operation_id).get("data") or {})
                model, report = described.get("providerModel"), described.get("report")
                modules = described.get("requiredModules") or []
                result = {"modules": modules, "definitionHash": described.get("definitionHash", ""), "kind": "report"}
                results[operation_id] = result
                if any(module not in installed for module in modules):
                    result["status"] = "UNSUPPORTED_MODULE"
                    continue
                found = oracle.call(model, "search", [described.get("providerDomain") or []], {"limit": 1, "order": "id desc"})
                if not found:
                    result["status"] = "NOT_RUN_NO_RECORD"
                    continue
                target = found[0]
                answer = cli(options.odoo_bin, options.profile, "execute", "--operation", operation_id,
                             "--provider-target-id", str(target), "--allow-unverified")
                document = ((answer.get("data") or {}).get("data") or {}) if answer.get("status") == "OK" else {}
                printed = base64.b64decode(document.get("content") or "")
                if not printed.startswith(b"%PDF"):
                    result.update({"status": "CLI_ERROR", "detail": (answer.get("error") or {}).get("code") or "NOT_A_PDF"})
                    continue
                try:
                    downloaded = browser.pdf(report, target)
                except (RuntimeError, urllib.error.URLError) as error:
                    result.update({"status": "ORACLE_ERROR", "detail": str(error)[:160]})
                    continue
                cli_pages, cli_text = inspect(printed, directory, "cli.pdf")
                web_pages, web_text = inspect(downloaded, directory, "web.pdf")
                result.update({"recordHash": canonical_hash([model, target]), "cliPages": cli_pages, "browserPages": web_pages,
                               "cliBytes": len(printed), "browserBytes": len(downloaded),
                               "cliTextHash": canonical_hash(cli_text.encode()), "browserTextHash": canonical_hash(web_text.encode()),
                               "status": "AGREE" if cli_pages and cli_pages == web_pages and cli_text == web_text else "DISAGREE"})
                print(f"{operation_id}: {result['status']} pages={cli_pages}/{web_pages}", file=sys.stderr)
    finally:
        browser.close()

    evidence = {
        "schemaVersion": "odoo.report-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": "the newest record inside the operation's provider domain, printed through the CLI and downloaded the "
                  "way the browser does (web session, /report/pdf/<report>/<id>); both PDFs compared by page count and "
                  "layout text (poppler). Statuses, page counts, sizes and hashes only.",
        "records": results,
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False, "documentContent": False},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
    counts = {}
    for result in results.values():
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    print(f"{series}: {len(results)} UNVERIFIED prints -> {counts}", file=sys.stderr)


if __name__ == "__main__":
    main()
