#!/usr/bin/env python3
"""Compare a conformance run with the manifest it is meant to uphold, and say so in one report.

The runners (odoo-read-conformance.py, odoo-write-conformance.py, odoo-action-conformance.py) record,
per operation, what the CLI did and what an independent JSON-RPC oracle saw. This gate reads their
evidence next to a series' conformance manifest and sorts every operation the manifest calls
CURRENT_VERIFIED into:

- RE-AGREED     the run agreed again (AGREE and its qualified forms);
- REGRESSED     the run DISAGREED: the CLI's result and Odoo's differ. This fails the gate;
- UNSTABLE      it agreed in one pass and disagreed in another of the same run (an order- or
                clock-dependent result): reported for a person to look at, not failed;
- STALE         the operation's definition changed since its record was made (definitionHash);
- NOT EXERCISED the run could not exercise it here (no record, no twins, a business rule, an oracle
                or CLI error). A lab's data decides these, so they are reported, never failed.

It also lists the operations the run agreed on that the manifest does not hold yet (promotable with
odoo-promote-conformance.py, which stays a reviewed step). It writes a Markdown summary and a JSON
report, and exits 1 when anything regressed.

    odoo-conformance-gate.py --series 20.0 --manifest packages/odoo/odoo20_conformance_manifest.json \\
        --evidence read.json write.json action.json --summary summary.md --report gate.json
"""
import argparse
import json
import sys

AGREED = {"AGREE", "AGREE_EMPTY", "AGREE_NO_CHANGE", "AGREE_REFUSED", "AGREE_DIALOG", "AGREE_WITH_UNCHECKED_FIELDS"}
REGRESSED = {"DISAGREE"}


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--series", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--evidence", nargs="+", required=True)
    parser.add_argument("--summary", required=True, help="Markdown summary to write")
    parser.add_argument("--report", required=True, help="JSON report to write")
    options = parser.parse_args()

    manifest = {record["operationId"]: record for record in json.load(open(options.manifest, encoding="utf-8"))["records"]}
    # The last evidence that names an operation wins, except that an agreement is never overwritten by a
    # later run that could not exercise the operation (a second read pass after seeding, say).
    results = {}
    samples = {}
    for path in options.evidence:
        for operation, result in json.load(open(path, encoding="utf-8"))["records"].items():
            samples.setdefault(operation, []).append(result.get("status", ""))
            previous = results.get(operation)
            if previous and previous["status"] in AGREED and result.get("status") not in AGREED | REGRESSED:
                continue
            results[operation] = {"status": result.get("status", ""), "definitionHash": result.get("definitionHash", ""), "evidence": path}

    report = {"series": options.series, "verified": len(manifest), "reAgreed": [], "regressed": [], "unstable": [],
              "stale": [], "notExercised": [], "newAgreements": []}
    for operation, record in sorted(manifest.items()):
        result = results.get(operation)
        if result is None:
            report["notExercised"].append({"operation": operation, "status": "NOT_IN_RUN"})
        elif result["definitionHash"] and record.get("definitionHash") and result["definitionHash"] != record["definitionHash"]:
            report["stale"].append({"operation": operation, "status": result["status"]})
        elif any(status in REGRESSED for status in samples[operation]) and any(status in AGREED for status in samples[operation]):
            report["unstable"].append({"operation": operation, "samples": samples[operation]})
        elif result["status"] in REGRESSED:
            report["regressed"].append({"operation": operation, "status": result["status"], "evidence": result["evidence"]})
        elif result["status"] in AGREED:
            report["reAgreed"].append(operation)
        else:
            report["notExercised"].append({"operation": operation, "status": result["status"]})
    report["newAgreements"] = sorted(op for op, result in results.items() if op not in manifest and result["status"] == "AGREE")

    with open(options.report, "w", encoding="utf-8") as out:
        json.dump(report, out, indent=2, ensure_ascii=False)
        out.write("\n")

    verified = report["verified"]
    share = (100.0 * len(report["reAgreed"]) / verified) if verified else 0.0
    lines = [
        f"## Odoo {options.series}: does odoo-cli still do what Odoo does?",
        "",
        f"A fresh Odoo {options.series} was built from the official image with demo data. Every operation was run",
        "through odoo-cli and checked by a separate program that talks to Odoo directly. The manifest holds",
        f"**{verified}** operations already verified on {options.series}; this is what became of them in this run.",
        "",
        "| | Operations | Meaning |",
        "|---|---:|---|",
        f"| Re-agreed with Odoo | {len(report['reAgreed'])} ({share:.1f}%) | verified before, and agreed again |",
        f"| **Regressed** | **{len(report['regressed'])}** | verified before, now disagrees with Odoo. Fails the run. |",
        f"| Unstable | {len(report['unstable'])} | agreed once and disagreed once in this run (timing or order); a person looks |",
        f"| Stale | {len(report['stale'])} | its definition changed since it was verified; to be verified again |",
        f"| Not exercised | {len(report['notExercised'])} | this lab had no record in the right state to try it on; not a failure |",
        f"| Agreed, not in the manifest yet | {len(report['newAgreements'])} | counted as verified only after review |",
        "",
    ]
    if report["regressed"]:
        lines += ["### Regressed", ""] + [f"- `{item['operation']}`" for item in report["regressed"]] + [""]
    if report["unstable"]:
        lines += ["### Unstable", ""] + [f"- `{item['operation']}`: {', '.join(item['samples'])}" for item in report["unstable"]] + [""]
    if report["stale"]:
        lines += ["### Stale", ""] + [f"- `{item['operation']}`" for item in report["stale"]] + [""]
    counts = {}
    for item in report["notExercised"]:
        counts[item["status"]] = counts.get(item["status"], 0) + 1
    if counts:
        lines += ["### Not exercised, by reason", ""] + [f"- {status}: {n}" for status, n in sorted(counts.items(), key=lambda kv: -kv[1])] + [""]
    lines += [
        "How the comparison works, what it does not prove and how to run it yourself:",
        "[docs/conformance.md](https://github.com/asao-vn/asao-odoo-tools/blob/main/docs/conformance.md).",
        "",
    ]
    with open(options.summary, "w", encoding="utf-8") as out:
        out.write("\n".join(lines))
    print(f"{options.series}: {len(report['reAgreed'])}/{verified} re-agreed, {len(report['regressed'])} regressed, "
          f"{len(report['unstable'])} unstable, {len(report['stale'])} stale, {len(report['notExercised'])} not exercised, {len(report['newAgreements'])} new")
    return 1 if report["regressed"] else 0


if __name__ == "__main__":
    sys.exit(main())
