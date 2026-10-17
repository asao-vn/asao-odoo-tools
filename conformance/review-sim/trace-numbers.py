#!/usr/bin/env python3
"""Check an agent's report against the review it was written from (DEC-2026-0165 §5.4): every number in
the report must appear in the review (a finding's amount, facts or words, or the period summary), or be
the sum of numbers that do. A number that traces to nothing is one the agent made up.

    trace-numbers.py --review review.json --report memo.md

Numbers are read as Vietnamese writes them: 12.500.000 đ, 82,7 triệu, 30%, 18,7%. Dates and record
names (BILL/2026/07/0016, 2026-07-06) are matched as text, not as numbers. Exit 1 when one does not trace."""
import argparse
import itertools
import json
import re
import sys

MONEY = re.compile(r"(\d{1,3}(?:\.\d{3})+|\d+(?:,\d+)?)\s*(triệu|tỷ|đ|nghìn)?", re.I)
SKIP = re.compile(r"\b\d{4}-\d{2}-\d{2}\b|\b\d{1,2}–\d{1,2}/\d{1,2}\b|[A-Z]+/\d{4}/\d+(?:/\d+)?|\b[A-Z]{1,5}\d{2,}\b|\bO\d{2}\b|\d{1,2}/\d{1,2}(?:/\d{4})?")


def numbers_in(value, out):
    if isinstance(value, bool):
        return
    if isinstance(value, (int, float)):
        out.add(round(float(value), 6))
    elif isinstance(value, str):
        for number, _ in parse(value):
            out.add(number)
    elif isinstance(value, dict):
        for item in value.values():
            numbers_in(item, out)
    elif isinstance(value, list):
        for item in value:
            numbers_in(item, out)


def parse(text):
    text = SKIP.sub(" ", text)
    for match in MONEY.finditer(text):
        raw, unit = match.group(1), (match.group(2) or "").lower()
        value = float(raw.replace(".", "")) if "." in raw and "," not in raw else float(raw.replace(",", "."))
        value *= {"triệu": 1e6, "tỷ": 1e9, "nghìn": 1e3}.get(unit, 1)
        after = text[match.end():match.end() + 1]
        decimals = len(raw.split(",")[1]) if "," in raw else 0
        if after == "%":
            yield round(value / 100, 6), decimals + 2
        yield round(value, 2), decimals


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--review", required=True)
    parser.add_argument("--report", required=True)
    parser.add_argument("--reads", help="records the agent read through the CLI to look closer, saved as JSON: "
                                        "their numbers trace too, and are reported as such")
    options = parser.parse_args()
    review = json.load(open(options.review, encoding="utf-8"))
    known = set()
    numbers_in(review, known)
    # Sums are looked for among the findings' own amounts only: a handful, so every pair and triple is cheap.
    stated = set()
    for finding in review["findings"]:
        numbers_in([finding.get("amountVnd"), finding.get("facts")], stated)
    amounts = sorted({n for n in stated if n >= 1000})
    report = open(options.report, encoding="utf-8").read()
    read = set()
    if options.reads:
        numbers_in(json.load(open(options.reads, encoding="utf-8")), read)

    def traces(value, decimals):
        if value in known:
            return "có trong rà soát"
        # Rounded the way a person writes it: no further than half the last digit written (18,4% from
        # 0,1838; 30% from 0,296), and 82,7 triệu within 0,6% of 82.700.000.
        half = 0.5 * 10 ** -decimals + 1e-9
        for n in sorted(known, key=lambda k: abs(k - value)):
            if (value >= 1000 and abs(n - value) / max(abs(n), 1) < 0.006) or abs(n - value) <= half:
                return f"làm tròn từ {n}"
        for n in known:
            if 0 < n < 1 and abs(n * 100 - value) <= half:
                return f"làm tròn từ {n}"
        if value in read:
            return "đọc thêm qua CLI"
        for k in (2, 3):  # a sum of a few amounts the review states
            for combo in itertools.combinations([a for a in amounts if a < value], k):
                if abs(sum(combo) - value) < 1:
                    return "tổng " + " + ".join(f"{c:,.0f}" for c in combo)
        return None

    rows, failed, seen = [], False, 0
    for line in report.splitlines():
        for value, decimals in sorted(set(parse(line))):
            if value < 10 and decimals == 0:
                continue
            seen += 1
            why = traces(value, decimals)
            failed |= why is None
            rows.append(f"{'KHÔNG TRUY ĐƯỢC' if why is None else 'ok':16} {value:>18,.2f}  {why or ''}  ← {line.strip()[:90]}")
    print("\n".join(rows))
    print(f"\n{seen} con số; " + ("có số KHÔNG truy được." if failed else "mọi con số đều truy được về kết quả rà soát."))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
