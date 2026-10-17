#!/usr/bin/env python3
"""Score a review against the simulation's truth: which planted cases a finding caught, which decoys a
finding wrongly flagged, and which findings the truth does not explain (each one listed, to be read:
a random business can produce a real case nobody planted).

    score.py --truth truth.json --review review.json [--markdown score.md]

A planted case is CAUGHT when a finding of its check names one of its records. A decoy is CLEAN when no
finding of its check names any of its records. Exit 1 when a planted case was missed or a decoy flagged."""
import argparse
import json
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--truth", required=True)
    parser.add_argument("--review", required=True)
    parser.add_argument("--markdown")
    options = parser.parse_args()
    truth = json.load(open(options.truth, encoding="utf-8"))["cases"]
    review = json.load(open(options.review, encoding="utf-8"))
    findings = review["findings"]

    def names(finding):
        return {e["name"] for e in finding["evidence"]}

    rows, explained, failed = [], set(), False
    for case in truth:
        records = set(case["records"])
        hits = [i for i, f in enumerate(findings) if f["check"] == case["check"] and names(f) & records]
        if case["check"] == "O15" and case["kind"] == "planted":
            hits = [i for i, f in enumerate(findings) if f["check"] == "O15"]
        explained.update(hits)
        if case["kind"] == "planted":
            ok = bool(hits)
            verdict = "BẮT ĐƯỢC" if ok else "BỎ SÓT"
        else:
            ok = not hits
            verdict = "KHÔNG BÁO NHẦM" if ok else "BÁO NHẦM"
        failed |= not ok
        severity = ", ".join(sorted({findings[i]["severity"] for i in hits})) or "—"
        rows.append((case["case"], case["check"], case["kind"], verdict, severity, case["note"], case.get("set", "")))
    # The prototype lists the checks that ran; odoo-cli review lists every check with its "ran" flag.
    run_checks = set(review["ran"]) if "ran" in review else {c["id"] for c in review.get("checks", []) if c.get("ran")}
    unexplained = [f for i, f in enumerate(findings) if i not in explained and f["check"] != "O15"]

    planted = [r for r in rows if r[2] == "planted"]
    caught = sum(1 for r in planted if r[3] == "BẮT ĐƯỢC")
    decoys = [r for r in rows if r[2] == "decoy"]
    clean = sum(1 for r in decoys if r[3] == "KHÔNG BÁO NHẦM")
    lines = ["| Ca | Phép kiểm tra | Loại | Kết quả | Mức | Mô tả |", "|---|---|---|---|---|---|"]
    lines += [f"| {r[0]} | `{r[1]}` | {'cài sẵn' if r[2] == 'planted' else 'mồi nhử'} | **{r[3]}** | {r[4]} | {r[5]} |" for r in rows]
    lines += ["", f"Bắt được **{caught}/{len(planted)}** ca cài sẵn; **{clean}/{len(decoys)}** mồi nhử không bị báo nhầm; "
              f"**{len(unexplained)}** phát hiện ngoài đáp án (xem từng cái bên dưới).", ""]
    fresh = [r for r in rows if r[6] == "fresh"]
    if fresh:
        fp = [r for r in fresh if r[2] == "planted"]
        fd = [r for r in fresh if r[2] == "decoy"]
        lines += [f"Riêng **bộ ca mới** (viết sau lần sửa cuối, chưa dùng để chỉnh): bắt được "
                  f"**{sum(1 for r in fp if r[3] == 'BẮT ĐƯỢC')}/{len(fp)}** ca cài sẵn; "
                  f"**{sum(1 for r in fd if r[3] == 'KHÔNG BÁO NHẦM')}/{len(fd)}** mồi nhử không bị báo nhầm.", ""]
    if unexplained:
        lines += ["Phát hiện ngoài đáp án:", ""] + [f"- `{f['check']}` [{f['severity']}] {f['title']}: {f['why']}" for f in unexplained]
    missing = sorted({c["check"] for c in truth} - run_checks)
    if missing:
        lines += ["", "Phép kiểm tra không chạy: " + ", ".join(missing)]
    text = "\n".join(lines) + "\n"
    if options.markdown:
        open(options.markdown, "w", encoding="utf-8").write(text)
    print(text)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
