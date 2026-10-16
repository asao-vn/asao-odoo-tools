"""summary.py <spec.json> <plant-source.json> <findings.json> <compare.txt>: measure.sh's result in
Markdown, for a CI job summary and the evidence."""
import ast
import json
import sys

spec = json.load(open(sys.argv[1], encoding="utf-8"))
planted = {r["key"]: r for r in json.load(open(sys.argv[2], encoding="utf-8"))}
check = json.load(open(sys.argv[3], encoding="utf-8"))
compare = open(sys.argv[4], encoding="utf-8").read().rstrip("\n")
outcomes = ast.literal_eval(compare.splitlines()[-1])

# compare.py's own matching: a default value by its field, anything else by its name.
names = {item["name"] for item in spec}
default_fields = {item["field"] for item in spec if item["kind"] == "default"}
unplanted = [f for f in check["findings"]
             if f.get("name") not in names and not (f["kind"] == "default value" and f["path"] in default_fields)]
failed = [key for key, result in planted.items() if not result["create"]["ok"] or not result.get("use", {}).get("ok")]


def n(*keys):
    return sum(outcomes.get(key, 0) for key in keys)


source = check.get("source", {})
print("## odoo-cli upgrade check, measured on real Odoo\n")
print("Source: Odoo %s, %s modules installed. Target: Odoo %s." % (
    source.get("serverVersion", "?"), source.get("installedModules", "?"), check.get("to", "?")))
print("%d customizations were made on the source as a person would make them, each used once. The check "
      "then read %s customizations there. The same customizations were made and used on the target, and "
      "rolled back.\n" % (len(spec), check.get("customizations", "?")))
print("| Odoo %s | count | odoo-cli upgrade check |" % check.get("to", "?"))
print("|---|---|---|")
print("| refuses it | %d | %d BREAKS or RENAME, %d CHECK only, %d missed |" % (
    n("caught", "flagged as CHECK", "MISSED"), n("caught"), n("flagged as CHECK"), n("MISSED")))
print("| runs it | %d | %d BREAKS or RENAME (false alarm), %d CHECK, %d quiet |\n" % (
    n("false alarm", "check, works", "quiet, works"), n("false alarm"), n("check, works"), n("quiet, works")))
print("- Customizations that already failed on the source: %s." % (", ".join(failed) or "none"))
print("- Findings on customizations nobody planted (the lab's own): %d." % len(unplanted))
for finding in unplanted[:20]:
    print("  - %s %s `%s` %s" % (finding["severity"], finding["kind"], finding.get("path", ""), finding["problem"]))
print("\n<details><summary>Each customization</summary>\n\n```\n%s\n```\n</details>" % compare)
