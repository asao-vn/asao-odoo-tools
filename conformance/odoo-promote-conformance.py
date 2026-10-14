#!/usr/bin/env python3
"""Promote the AGREE results of a conformance run into a conformance manifest.

This is the reviewed step the runners leave out on purpose (scripts/dev/odoo-read-conformance.py,
scripts/dev/odoo-write-conformance.py): run it, read the diff, commit it. It adds one
CURRENT_VERIFIED record per operation the evidence says AGREE that the manifest does not hold yet.
Each record cites the evidence and is bound to the operation definition the run exercised
(definitionHash), so a later change to that definition voids it. Nothing else is promoted: not an
agreement on an empty model, not a describe with fields the oracle could not check.

    scripts/dev/odoo-promote-conformance.py --evidence docs/.../evidence/run.json \\
        --manifest packages/odoo/odoo19_conformance_manifest.json

A regeneration that changes an operation's definition (a module adds a field to a form) voids its
record: --drop-stale --odoo-bin ./odoo removes, before anything is promoted, every record whose
operation now has another definition than the one it verified, or no longer exists, so the
operation can be run again and promoted anew.
"""

import argparse
import json
import subprocess



def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--evidence", help="repository-relative path of the evidence document")
    parser.add_argument("--drop-stale", action="store_true", help="drop records whose operation's definition changed")
    parser.add_argument("--odoo-bin", help="the standalone CLI, for --drop-stale")
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--only", help="comma-separated operation ids: promote these AGREE results and no others "
                                       "(an executor change after the run voids the rest)")
    options = parser.parse_args()
    only = set(options.only.split(",")) if options.only else None

    manifest = json.load(open(options.manifest, encoding="utf-8"))
    if options.drop_stale:
        if not options.odoo_bin:
            raise SystemExit("--drop-stale needs --odoo-bin")
        kept, dropped = [], []
        for record in manifest["records"]:
            if record.get("definitionHash") and current_definition(options.odoo_bin, record["operationId"]) != record["definitionHash"]:
                dropped.append(record["operationId"])
            else:
                kept.append(record)
        manifest["records"] = kept
        print(f"dropped {len(dropped)} records verified against another definition: {sorted(dropped)}")
    if not options.evidence:
        write(options.manifest, manifest)
        return
    evidence = json.load(open(options.evidence, encoding="utf-8"))
    existing = {record["operationId"] for record in manifest["records"]}
    added, skipped = [], []
    for operation_id, result in sorted(evidence["records"].items()):
        if result.get("status") != "AGREE" or operation_id in existing or (only is not None and operation_id not in only):
            continue
        # An operation id names a catalog operation (finance.payment_token.create), never material: the
        # manifest parser exempts it, and the guard stays on every other field.
        if not result.get("definitionHash"):
            skipped.append(operation_id)
            continue
        added.append({
            "operationId": operation_id,
            "providerVersion": evidence["provider"]["version"],
            "imageDigest": evidence["provider"]["imageDigest"],
            "modules": sorted(set(result["modules"])),
            "status": "CURRENT_VERIFIED",
            "evidenceRef": options.evidence,
            "verifiedAt": evidence["recordedAt"],
            "definitionHash": result["definitionHash"],
        })
    manifest["records"].extend(added)
    write(options.manifest, manifest)
    print(f"promoted {len(added)}; skipped {len(skipped)} (no definition hash): {skipped}")


def current_definition(binary, operation_id):
    """The operation's current definitionHash, or None when the catalog no longer has it."""
    completed = subprocess.run([binary, "catalog", "describe", "--operation", operation_id, "--output", "json"],
                               capture_output=True, text=True, timeout=60)
    try:
        return (json.loads(completed.stdout).get("data") or {}).get("definitionHash")
    except json.JSONDecodeError:
        return None


def write(path, manifest):
    with open(path, "w", encoding="utf-8") as out:
        json.dump(manifest, out, indent=2, ensure_ascii=False)
        out.write("\n")


if __name__ == "__main__":
    main()
