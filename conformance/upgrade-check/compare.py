"""compare.py <spec.json> <findings.json> <replay-target.json>: what the scanner predicted for each
planted customization, against what the target series actually did with it."""
import collections
import json
import sys

spec = json.load(open(sys.argv[1], encoding="utf-8"))
findings = json.load(open(sys.argv[2], encoding="utf-8"))["findings"]
replay = {r["key"]: r for r in json.load(open(sys.argv[3], encoding="utf-8"))}
RANK = ["BREAKS", "RENAME", "CHECK", "REVIEW", "UNKNOWN", "INFO"]


def predicted(item):
    if item["kind"] == "default":
        mine = [f for f in findings if f["kind"] == "default value" and f["path"] == item["field"]]
    else:
        mine = [f for f in findings if f.get("name") == item["name"]]
    severities = [f["severity"] for f in mine]
    for severity in RANK:
        if severity in severities:
            return severity, mine
    return "OK", mine


def actual(item):
    result = replay[item["key"]]
    if not result["create"]["ok"]:
        return "ERROR", "on create: " + result["create"]["error"]
    if not result.get("use", {}).get("ok"):
        return "ERROR", "on use: " + result["use"]["error"]
    return "WORKS", ""


outcomes = collections.Counter()
for item in spec:
    guess, mine = predicted(item)
    truth, why = actual(item)
    flagged = guess in ("BREAKS", "RENAME")
    if truth == "ERROR":
        outcome = "caught" if flagged else ("flagged as CHECK" if guess == "CHECK" else "MISSED")
    else:
        outcome = "false alarm" if flagged else ("check, works" if guess == "CHECK" else "quiet, works")
    outcomes[outcome] += 1
    print("%-26s %-10s expected %-7s scanner %-7s target %-6s -> %-16s %s" % (
        item["key"], item["kind"], item.get("expect", "?"), guess, truth, outcome, why[:110]))
    for finding in mine:
        print("%30s %s %s %s %s" % ("", finding["problem"], finding["path"], finding.get("to") or "", finding.get("values") or ""))
print(dict(outcomes))
