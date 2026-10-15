#!/usr/bin/env python3
"""Conformance evidence for the task operations a routine runs: recurring tasks, deadlines, tags, priority.

The generic write runner (odoo-write-conformance.py) writes the fewest fields a record needs, so it
never exercises a task's recurrence. This runner drives the whole workflow through the real
standalone CLI and checks every step against an independent JSON-RPC oracle:

  * work.task.create with every field a routine sets (project and tags by name, priority, deadline,
    description, a weekly recurrence that runs forever): the provider must hold each value, and a
    project.task.recurrence must exist for the task;
  * work.task.describe and work.task.list on that task: every field the CLI reports must equal the
    provider's value, translated the way the contract says (priority high <-> "2", tags as ids);
  * work.task.update to a monthly recurrence with an end date, another priority and other tags: the
    provider must hold the new values;
  * work.task.update state done: the provider must then hold exactly one new open task in the same
    recurrence, its deadline one month later. Odoo makes the next occurrence when one is closed, not
    on a calendar; this step proves the CLI's write triggers it.

An operation AGREEs only when every one of its checks agreed. The evidence holds statuses, field
names and hashes only: no credential, no record content. With --cleanup the tasks it made are
deleted through the oracle afterwards (a lab only). It promotes nothing.

    ODOO_ORACLE_PASSWORD=... AERP_CREDENTIAL_LAB19=... scripts/dev/odoo-task-recurrence-conformance.py \\
        --odoo-bin ./odoo-cli --profile lab19 --endpoint http://127.0.0.1:58719 --database lab19 \\
        --login admin --series 19.0 --image-digest sha256:... --evidence run.json --cleanup
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
OPERATIONS = ("work.task.create", "work.task.describe", "work.task.list", "work.task.update")
PRIORITY = {"low": "0", "medium": "1", "high": "2", "urgent": "3"}


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


def cli(binary, profile, *args):
    selection = ["--profile", profile] if args and args[0] == "execute" else []
    completed = subprocess.run([binary, *args, *selection, "--output", "json"], capture_output=True, text=True, timeout=180)
    try:
        return json.loads(completed.stdout)
    except json.JSONDecodeError:
        return {"status": "ERROR", "error": {"code": "BROKEN_OUTPUT"}}


def records_of(answer):
    data = ((answer.get("data") or {}).get("data")) if answer.get("status") == "OK" else None
    return data if isinstance(data, list) else ([data] if isinstance(data, dict) else [])


def first_id(value):
    if isinstance(value, dict):
        return value.get("id")
    if isinstance(value, list) and value:
        return value[0]
    return value or None


def day(value):
    return (value or "")[:10] or None


class Checks:
    """The comparisons one operation made: field -> agreed, plus why a step could not run."""

    def __init__(self):
        self.fields, self.failures = {}, []

    def same(self, field, expected, observed):
        agreed = expected == observed
        self.fields[field] = self.fields.get(field, True) and agreed
        if not agreed:
            self.failures.append(field)

    def fail(self, reason):
        self.failures.append(reason)

    def result(self, modules, definition_hash):
        status = "AGREE" if self.fields and not self.failures else ("DISAGREE" if self.fields else "NOT_RUN")
        return {"status": status, "modules": modules, "definitionHash": definition_hash,
                "checkedFields": sorted(self.fields), "failures": sorted(set(self.failures))}


def compare_read(checks, reported, stored, recurrence_fields=True):
    """Every field the CLI reported, against the provider's value as the contract translates it."""
    checks.same("name", stored["name"], reported.get("name"))
    checks.same("project", first_id(stored["project_id"]), first_id(reported.get("project")))
    checks.same("due_on", day(stored["date_deadline"]), day(reported.get("due_on")))
    checks.same("priority", stored["priority"], PRIORITY.get(reported.get("priority"), reported.get("priority")))
    checks.same("tags", sorted(stored["tag_ids"]), sorted(first_id(t) for t in reported.get("tags") or []))
    if recurrence_fields:
        checks.same("recurring", stored["recurring_task"], reported.get("recurring"))
        checks.same("repeat_every", stored["repeat_interval"], reported.get("repeat_every"))
        checks.same("repeat_unit", stored["repeat_unit"], reported.get("repeat_unit"))
        checks.same("repeat_type", stored["repeat_type"], reported.get("repeat_type"))
        checks.same("repeat_until", stored["repeat_until"] or None, reported.get("repeat_until") or None)


STORED_FIELDS = ["name", "project_id", "date_deadline", "priority", "tag_ids", "description", "recurring_task",
                 "recurrence_id", "repeat_interval", "repeat_unit", "repeat_type", "repeat_until", "state"]


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
    parser.add_argument("--cleanup", action="store_true", help="delete the tasks this run made (a lab only)")
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
    checks = {op: Checks() for op in OPERATIONS}
    made = []

    def stored(task_id):
        return oracle.call("project.task", "read", [[task_id], STORED_FIELDS])[0]

    def run(op, *args):
        return cli(options.odoo_bin, options.profile, "execute", "--operation", op, "--allow-unverified", *args)

    try:
        project = oracle.call("project.project", "search_read", [[]], {"fields": ["name"], "limit": 1, "order": "id"})[0]
        tags = oracle.call("project.tags", "search_read", [[]], {"fields": ["name"], "limit": 3, "order": "id"})
        if len(tags) < 3:
            raise SystemExit("the provider needs three project tags")
        due = (datetime.date.today() + datetime.timedelta(days=5)).isoformat()
        created = {"name": f"Conformance routine {STAMP}", "project": project["name"],
                   "tags": [tags[0]["name"], tags[1]["name"]], "priority": "high", "due_on": due,
                   "description": f"<p>Conformance routine {STAMP}</p>",
                   "recurring": True, "repeat_every": 1, "repeat_unit": "week", "repeat_type": "forever"}

        # create
        answer = run("work.task.create", "--values", json.dumps(created))
        task_id = ((answer.get("data") or {}).get("data") or {}).get("id") if answer.get("status") == "OK" else None
        if not task_id:
            checks["work.task.create"].fail("CLI_ERROR:" + ((answer.get("error") or {}).get("code") or "NO_ID"))
            raise StopIteration
        made.append(task_id)
        held = stored(task_id)
        c = checks["work.task.create"]
        c.same("name", created["name"], held["name"])
        c.same("project", project["id"], first_id(held["project_id"]))
        c.same("tags", sorted([tags[0]["id"], tags[1]["id"]]), sorted(held["tag_ids"]))
        c.same("priority", "2", held["priority"])
        c.same("due_on", due, day(held["date_deadline"]))
        c.same("description", True, f"Conformance routine {STAMP}" in (held["description"] or ""))
        c.same("recurring", True, held["recurring_task"])
        c.same("recurrence", True, bool(held["recurrence_id"]))
        c.same("repeat_every", 1, held["repeat_interval"])
        c.same("repeat_unit", "week", held["repeat_unit"])
        c.same("repeat_type", "forever", held["repeat_type"])

        # describe and list
        reported = records_of(run("work.task.describe", "--provider-target-id", str(task_id)))
        if len(reported) != 1:
            checks["work.task.describe"].fail("NO_RECORD")
        else:
            compare_read(checks["work.task.describe"], reported[0], held)
            checks["work.task.describe"].same("occurrences", 1, reported[0].get("occurrences"))
        listed = records_of(run("work.task.list", "--filter", json.dumps([["id", "=", task_id]])))
        if [r.get("id") for r in listed] != [task_id]:
            checks["work.task.list"].fail("IDS_DIFFER")
        else:
            compare_read(checks["work.task.list"], listed[0], held)

        # update: another recurrence, priority and tags
        until = (datetime.date.today() + datetime.timedelta(days=120)).isoformat()
        changed = {"priority": "urgent", "tags": [tags[2]["id"]], "repeat_unit": "month", "repeat_type": "until",
                   "repeat_until": until, "description": f"<p>Conformance update {STAMP}</p>"}
        answer = run("work.task.update", "--provider-target-id", str(task_id), "--values", json.dumps(changed))
        c = checks["work.task.update"]
        if answer.get("status") != "OK":
            c.fail("CLI_ERROR:" + ((answer.get("error") or {}).get("code") or ""))
            raise StopIteration
        held = stored(task_id)
        c.same("priority", "3", held["priority"])
        c.same("tags", [tags[2]["id"]], sorted(held["tag_ids"]))
        c.same("repeat_unit", "month", held["repeat_unit"])
        c.same("repeat_type", "until", held["repeat_type"])
        c.same("repeat_until", until, held["repeat_until"])
        c.same("description", True, f"Conformance update {STAMP}" in (held["description"] or ""))

        # update state done: Odoo makes the next occurrence, a month later
        recurrence = first_id(held["recurrence_id"])
        answer = run("work.task.update", "--provider-target-id", str(task_id), "--values", json.dumps({"state": "done"}))
        if answer.get("status") != "OK":
            c.fail("CLI_ERROR_ON_CLOSE")
            raise StopIteration
        siblings = oracle.call("project.task", "search_read", [[["recurrence_id", "=", recurrence], ["id", "!=", task_id]]],
                               {"fields": ["date_deadline", "state"], "order": "id"})
        made.extend(s["id"] for s in siblings)
        c.same("state", "1_done", stored(task_id)["state"])
        c.same("next_occurrence", 1, len(siblings))
        if len(siblings) == 1:
            expected = datetime.date.fromisoformat(due)
            month = expected.month % 12 + 1
            expected = expected.replace(year=expected.year + (expected.month == 12), month=month)
            c.same("next_occurrence_due_on", expected.isoformat(), day(siblings[0]["date_deadline"]))
            c.same("next_occurrence_open", True, siblings[0]["state"] not in ("1_done", "1_canceled"))
    except StopIteration:
        pass
    finally:
        if options.cleanup and made:
            oracle.call("project.task", "unlink", [sorted(set(made))])

    results = {op: checks[op].result(described[op].get("requiredModules") or [], described[op].get("definitionHash", ""))
               for op in OPERATIONS}
    evidence = {
        "schemaVersion": "odoo.task-recurrence-conformance-run/v1",
        "recordedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provider": {"version": oracle.version.get("server_version"), "series": series,
                     "imageDigest": options.image_digest, "endpointHash": canonical_hash(options.endpoint)},
        "method": "a recurring task created through the CLI with project and tags by name, priority, deadline, "
                  "description and a weekly recurrence; read back by an independent JSON-RPC oracle and through the "
                  "CLI's describe and list, compared field by field; updated to a monthly recurrence with an end date, "
                  "another priority and tags, read back; then closed through the CLI, after which the provider must "
                  "hold exactly one new open task in the same recurrence, due one month later. Field names, statuses "
                  "and hashes only." + (" The tasks were deleted afterwards." if options.cleanup else ""),
        "records": results,
        "contentPolicy": {"credentialMaterial": False, "rawProviderResponses": False},
    }
    with open(options.evidence, "w", encoding="utf-8") as out:
        json.dump(evidence, out, indent=1, sort_keys=True)
        out.write("\n")
    for op, result in results.items():
        print(f"{series} {op}: {result['status']} {result['failures'] or ''}", file=sys.stderr)


if __name__ == "__main__":
    main()
