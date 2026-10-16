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
import subprocess
import sys
import urllib.request

MODULE = "salon_demo"
MODEL = "salon_demo.booking"


class Oracle:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.password = endpoint.rstrip("/"), database, password
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
        return self._rpc("object", "execute_kw", [self.database, self.uid, self.password, model, method, args, kwargs or {}])

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

    agreed = sum(1 for c in checks if c["status"] == "AGREE")
    json.dump({"module": MODULE, "checks": checks, "agreed": agreed, "total": len(checks)},
              open(os.path.join(options.out, "evidence.json"), "w"), indent=1)
    with open(os.path.join(options.out, "summary.md"), "w") as summary:
        summary.write("## Operation packs, proven on a real Odoo (DEC-2026-0176)\n\n")
        summary.write("A custom module, `salon_demo`, ships an odoo-cli pack. Every operation of the pack ran through odoo-cli\n")
        summary.write("and was checked by a separate JSON-RPC client against Odoo itself: %d of %d checks agree.\n\n" % (agreed, len(checks)))
        summary.write("| Check | Result |\n|---|---|\n")
        for c in checks:
            summary.write("| %s | %s |\n" % (c["check"], c["status"]))
    print("%d of %d checks agree" % (agreed, len(checks)))
    sys.exit(0 if agreed == len(checks) else 1)


if __name__ == "__main__":
    main()
