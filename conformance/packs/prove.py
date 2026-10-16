"""prove.py: the live proof of an operation pack (DEC-2026-0176), on a lab with the salon_demo module.

It runs every operation of the module's pack through odoo-cli, and checks each against Odoo itself,
through a JSON-RPC client of its own logged in with the admin's password, as the conformance oracle
is: the list against a search, the create and the update against a read of the record, each button
and the wizard against the state or the time they set, the report against a PDF. It also checks what
a pack must not do: its operations need --allow-unverified, an agent's writes ask the person
(CONFIRM), and a method the pack does not name is no operation. It writes evidence.json and
summary.md, and exits non-zero when a check fails.

prove.sh builds the lab and calls it; run alone, it needs a profile whose Odoo has salon_demo."""
import argparse
import json
import os
import re
import secrets
import subprocess
import sys
import threading
import urllib.parse
import urllib.request

MODULE = "salon_demo"
MODEL = "salon_demo.booking"


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.credential = endpoint.rstrip("/"), database, password
        self.uid = self._rpc("common", "login", [database, login, password])

    def _rpc(self, service, method, args):
        body = json.dumps({"jsonrpc": "2.0", "method": "call", "params": {"service": service, "method": method, "args": args}}).encode()
        request = urllib.request.Request(self.endpoint + "/jsonrpc", body, {"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=60) as response:
            answer = json.loads(response.read())
        if answer.get("error"):
            raise RuntimeError((answer["error"].get("data") or {}).get("message") or answer["error"].get("message"))
        return answer["result"]

    def call(self, model, method, args, kwargs=None):
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.credential, model, method, args, kwargs or {}])

    def read(self, record_id, fields):
        return self.call(MODEL, "read", [[record_id], fields])[0]


def cli(options, *args):
    command = [options.odoo_bin, *args, "--profile", options.profile, "--output", "json"]
    done = subprocess.run(command, capture_output=True, text=True, timeout=300)
    try:
        document = json.loads(done.stdout or "{}")
    except json.JSONDecodeError:
        document = {"unparsed": done.stdout[:500]}
    return done.returncode, document, done.stderr[-500:]


def execute(options, operation, target=None, values=None, *extra):
    args = ["execute", "--operation", operation, "--allow-unverified", *extra]
    if target is not None:
        args += ["--provider-target-id", str(target)]
    for key, value in (values or {}).items():
        args += ["--set", "%s=%s" % (key, value)]
    return cli(options, *args)


class McpHost:
    """A scripted MCP host: it starts odoo-cli mcp serve on the profile, and answers every
    confirmation the server asks for with yes, as a person who accepts would."""

    def __init__(self, odoo_bin, profile, *flags):
        self.process = subprocess.Popen([odoo_bin, "mcp", "serve", "--profile", profile, "--mode", "tools", *flags],
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
        self.next_id = 1
        self.request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {"elicitation": {}},
                                    "clientInfo": {"name": "prove", "version": "0"}})
        self.send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def send(self, message):
        self.process.stdin.write(json.dumps(message) + "\n")
        self.process.stdin.flush()

    def request(self, method, params):
        request_id = self.next_id
        self.next_id += 1
        self.send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        while True:
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError("the server closed: " + self.process.stderr.read()[-500:])
            message = json.loads(line)
            if message.get("method") == "elicitation/create":
                self.send({"jsonrpc": "2.0", "id": message["id"], "result": {"action": "accept", "content": {"confirm": True}}})
                continue
            if message.get("id") == request_id:
                return message.get("result") or {"error": message.get("error")}

    def call(self, tool, arguments):
        return self.request("tools/call", {"name": tool, "arguments": arguments})

    def close(self):
        self.process.stdin.close()
        self.process.wait(timeout=30)


def data_access_checks(options, oracle, check):
    """DEC-2026-0178 on the same lab: what an agent gets from mcp serve at act and at operate."""
    partner = oracle.call("res.partner", "search", [[["name", "=", "Pack Proof Customer"]]], {"limit": 1})[0]
    oracle.call("res.partner", "write", [[partner], {"email": "proof@example.com", "phone": "+84 900 000 000",
                                                   "comment": "<p>Call after five</p>"}])
    host = McpHost(options.odoo_bin, options.profile, "--data-access", "act")
    try:
        answer = host.call("explore_search", {"model": "res.partner", "filter": [["id", "=", partner]],
                                              "fields": ["name", "email", "phone", "comment"]})
        records = ((answer.get("structuredContent") or {}).get("data") or {}).get("records") or [{}]
        record = records[0]
        check("data access act: personal data and free text withheld", record.get("name") == "Pack Proof Customer"
              and record.get("email") == "[withheld: personal]" and record.get("phone") == "[withheld: personal]"
              and record.get("comment") == "[withheld: free text]", record or answer)
    finally:
        host.close()
    host = McpHost(options.odoo_bin, options.profile, "--data-access", "operate", "--allow-writes", "--allow-unverified")
    try:
        tools = {tool["name"] for tool in host.request("tools/list", {}).get("tools", [])}
        check("data access operate: no read tool but discovery", "explore_search" not in tools and "salon_demo_booking_list" not in tools
              and "catalog_describe" in tools and "salon_demo_booking_create" in tools, sorted(tools)[:40])
        answer = host.call("salon_demo_booking_create", {"name": "Operate booking", "seats": 2})
        document = answer.get("structuredContent") or {}
        inner = (document.get("data") or {}).get("data") or {}
        created = inner.get("id")
        stored = oracle.call(MODEL, "search_read", [[["id", "=", created or 0]]], {"fields": ["name", "seats"]})
        check("data access operate: the write runs, the agent gets its id only", bool(created) and set(inner) == {"id"}
              and stored and stored[0]["name"] == "Operate booking" and stored[0]["seats"] == 2, document or answer)
    finally:
        host.close()


def type_on_page(stream, value):
    """The person: when the server announces its one-time page, open it and submit value."""
    for line in stream:
        found = re.search(r"(http://127\.0\.0\.1:\d+/[0-9a-f]+)", line)
        if not found:
            continue
        address = found.group(1)
        page = urllib.request.urlopen(address, timeout=30).read().decode()
        token = re.search(r'name="token" value="([0-9a-f]+)"', page).group(1)
        form = urllib.parse.urlencode({"token": token, "value": value}).encode()
        origin = address[:address.rindex("/")]
        request = urllib.request.Request(address, form, {"Content-Type": "application/x-www-form-urlencoded", "Origin": origin})
        urllib.request.urlopen(request, timeout=30).read()
        return


def secret_page_checks(options, check):
    """DEC-2026-0179: an agent asks for a secret, the person types it on the one-time page, and the
    agent gets "stored" only."""
    target = os.path.join(options.out, "proof.env")
    with open(target, "w") as handle:
        handle.write("A=1\n")
    typed = "typed-on-the-page-" + secrets.token_hex(4)
    host = McpHost(options.odoo_bin, options.profile, "--allow-writes")
    try:
        threading.Thread(target=type_on_page, args=(host.process.stderr, typed), daemon=True).start()
        answer = host.call("secret_env", {"file": target, "name": "PROOF_KEY", "browser": True})
        document = answer.get("structuredContent") or {}
        content = open(target).read()
        check("secret page: the person types it, the agent gets stored only", content == "A=1\nPROOF_KEY=" + typed + "\n"
              and typed not in json.dumps(answer) and (document.get("data") or {}).get("stored") is True
              and oct(os.stat(target).st_mode & 0o777) == "0o600", document or answer)
    finally:
        host.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--odoo-bin", required=True)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--database", required=True)
    parser.add_argument("--login", required=True)
    parser.add_argument("--out", required=True)
    options = parser.parse_args()
    oracle = Oracle(options.endpoint, options.database, options.login, os.environ.get("ODOO_ORACLE_PASSWORD", "admin"))
    checks = []

    def check(name, ok, detail=""):
        checks.append({"check": name, "status": "AGREE" if ok else "DISAGREE", "detail": str(detail)[:400]})
        print("%-44s %s %s" % (name, "AGREE" if ok else "DISAGREE", "" if ok else str(detail)[:300]), flush=True)

    code, generated, err = cli(options, "pack", "generate", "--module", MODULE, "--file", os.path.join(options.out, "generated-pack.json"))
    ids = sorted(op["id"] for op in ((generated.get("data") or {}).get("pack") or {}).get("operations", []))
    want = ["salon_demo.booking.cancel", "salon_demo.booking.confirm", "salon_demo.booking.create", "salon_demo.booking.done",
            "salon_demo.booking.list", "salon_demo.booking.update"]
    check("generate: the module's reads, writes and buttons", code == 0 and ids == want, ids or generated or err)

    code, synced, err = cli(options, "pack", "sync")
    packs = (synced.get("data") or {}).get("packs") or []
    found = [p for p in packs if p.get("module") == MODULE]
    check("sync: finds the static pack and keeps it", code == 0 and found and found[0].get("status") in ("ADDED", "UNCHANGED", "CHANGED")
          and found[0].get("operations") == 8, packs or synced or err)

    code, shown, err = cli(options, "pack", "show", "--module", MODULE)
    by_id = {op["id"]: op for op in (shown.get("data") or {}).get("operations", [])}
    check("show: risks after the floors", code == 0 and by_id.get("salon_demo.booking.confirm", {}).get("risk") == "MEDIUM"
          and by_id.get("salon_demo.booking.cancel", {}).get("risk") == "HIGH" and by_id.get("salon_demo.booking.list", {}).get("risk") == "LOW", by_id)

    code, tools, err = cli(options, "mcp", "tools", "--allow-writes", "--confirm-writes=false")
    access = {tool["name"]: tool["agentAccess"] for tool in (tools.get("data") or {}).get("tools", [])}
    check("mcp: reads READ, writes always CONFIRM", access.get("salon_demo_booking_list") == "READ" and access.get("salon_demo_booking_create") == "CONFIRM"
          and access.get("salon_demo_booking_confirm") == "CONFIRM" and access.get("salon_demo_booking_reschedule") == "CONFIRM",
          {k: v for k, v in access.items() if k.startswith(MODULE)})

    code, refused, err = cli(options, "execute", "--operation", "salon_demo.booking.list")
    check("unverified: refused without --allow-unverified", code != 0 and "UNVERIFIED" in json.dumps(refused), refused or err)

    code, listed, err = execute(options, "salon_demo.booking.list", None, None, "--limit", "50")
    records = ((listed.get("data") or {}).get("data") or {})
    records = records.get("records", records) if isinstance(records, dict) else records
    got = sorted(r["id"] for r in records) if isinstance(records, list) else []
    expected = sorted(oracle.call(MODEL, "search", [[]], {"limit": 50}))
    check("list: the records Odoo has", code == 0 and got == expected and len(expected) >= 2, {"cli": got, "odoo": expected, "err": err})

    values = {"name": "Proof booking", "partner_id": "Pack Proof Customer", "start": "2026-11-02 09:00:00", "seats": "3", "vip": "true", "note": "first"}
    code, created, err = execute(options, "salon_demo.booking.create", None, values)
    booking = ((created.get("data") or {}).get("data") or {}).get("id")
    if booking:
        record = oracle.read(booking, ["name", "partner_id", "start", "seats", "vip", "note", "state"])
        partner = oracle.call("res.partner", "search", [[["name", "=", "Pack Proof Customer"]]], {"limit": 1})
        ok = (record["name"] == "Proof booking" and record["partner_id"] and record["partner_id"][0] == partner[0]
              and record["start"] == "2026-11-02 09:00:00" and record["seats"] == 3 and record["vip"] is True and record["state"] == "draft")
        check("create: the record as written, the customer by name", code == 0 and ok, record)
    else:
        check("create: the record as written, the customer by name", False, created or err)
        booking = oracle.call(MODEL, "create", [{"name": "Proof booking (oracle)"}])

    code, updated, err = execute(options, "salon_demo.booking.update", booking, {"seats": "4", "note": "moved"})
    record = oracle.read(booking, ["seats", "note"])
    check("update: seats and note", code == 0 and record["seats"] == 4 and record["note"] == "moved", record if code == 0 else updated or err)

    code, confirmed, err = execute(options, "salon_demo.booking.confirm", booking)
    check("button: confirm", code == 0 and oracle.read(booking, ["state"])["state"] == "confirmed", confirmed or err)

    code, moved, err = execute(options, "salon_demo.booking.reschedule", booking, {"start": "2026-11-03 10:30:00"})
    check("wizard: reschedule", code == 0 and oracle.read(booking, ["start"])["start"] == "2026-11-03 10:30:00", moved or err)

    code, finished, err = execute(options, "salon_demo.booking.done", booking)
    check("button: done", code == 0 and oracle.read(booking, ["state"])["state"] == "done", finished or err)

    code, again, err = execute(options, "salon_demo.booking.confirm", booking)
    check("button: Odoo's refusal is reported, nothing changes", code != 0 and oracle.read(booking, ["state"])["state"] == "done", again or err)

    morning = oracle.call(MODEL, "search", [[["name", "=", "Morning cut"]]], {"limit": 1})[0]
    code, cancelled, err = execute(options, "salon_demo.booking.cancel", morning)
    check("button: cancel", code == 0 and oracle.read(morning, ["state"])["state"] == "cancel", cancelled or err)

    pdf = os.path.join(options.out, "booking.pdf")
    code, printed, err = execute(options, "salon_demo.booking.print", booking, None, "--save-file", pdf)
    head = open(pdf, "rb").read(5) if os.path.exists(pdf) else b""
    check("report: the booking as a PDF", code == 0 and head == b"%PDF-", printed or err)

    code, unknown, err = cli(options, "execute", "--operation", "salon_demo.booking.unlink", "--allow-unverified", "--provider-target-id", str(morning))
    still = oracle.call(MODEL, "search_count", [[["id", "=", morning]]])
    check("no operation for a method the pack does not name", code != 0 and still == 1, unknown or err)

    try:
        data_access_checks(options, oracle, check)
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        check("data access: the scripted MCP host ran", False, error)
    try:
        secret_page_checks(options, check)
    except (RuntimeError, OSError, ValueError, AttributeError, subprocess.SubprocessError) as error:
        check("secret page: the scripted MCP host ran", False, error)

    agreed = sum(1 for c in checks if c["status"] == "AGREE")
    json.dump({"module": MODULE, "checks": checks, "agreed": agreed, "total": len(checks)},
              open(os.path.join(options.out, "evidence.json"), "w"), indent=1)
    with open(os.path.join(options.out, "summary.md"), "w") as summary:
        summary.write("## Operation packs, data levels and the secret page, proven on a real Odoo (DEC-2026-0176, 0178, 0179)\n\n")
        summary.write("A custom module, `salon_demo`, ships an odoo-cli pack. Every operation of the pack ran through odoo-cli\n")
        summary.write("and was checked by a separate JSON-RPC client against Odoo itself: %d of %d checks agree.\n\n" % (agreed, len(checks)))
        summary.write("| Check | Result |\n|---|---|\n")
        for c in checks:
            summary.write("| %s | %s |\n" % (c["check"], c["status"]))
    print("%d of %d checks agree" % (agreed, len(checks)))
    sys.exit(0 if agreed == len(checks) else 1)


if __name__ == "__main__":
    main()
