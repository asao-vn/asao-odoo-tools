#!/usr/bin/env python3
"""Real spending for the review simulation: a sample of Birmingham City Council's card and supplier payments.

    prepare_birmingham.py --cards cards.csv --payments payments.csv --out bhm-spend.json
                          [--claims 1000] [--bills 800] [--seed 1]

UCI Online Retail II gave the simulation real sales; nothing real reached the checks on purchases and
expenses. Birmingham City Council publishes every purchase card transaction and every payment to a supplier
over 500 pounds, under the Open Government Licence v3.0
(https://www.nationalarchives.gov.uk/doc/open-government-licence/version/3/), on its data portal:

    https://www.cityobservatory.birmingham.gov.uk/explore/dataset/purchase-card-transactions/
    https://www.cityobservatory.birmingham.gov.uk/explore/dataset/payments-to-suppliers-over-gbp500/

Download a year of each as CSV (the portal's export, filtered on the date), October 2024 - September 2025:

    B=https://www.cityobservatory.birmingham.gov.uk/api/explore/v2.1/catalog/datasets
    curl -sL -o cards.csv "$B/purchase-card-transactions/exports/csv?delimiter=%2C&where=trans_date%3E%3Ddate%272024-10-01%27%20and%20trans_date%3Cdate%272025-10-01%27"
    curl -sL -o payments.csv "$B/payments-to-suppliers-over-gbp500/exports/csv?delimiter=%2C&where=date%3E%3Ddate%272024-10-01%27%20and%20date%3Cdate%272025-10-01%27"

The portal is live and corrected over time, so the sha256 of both files is written into the output.

What is kept:
  - card transactions charged in pounds above zero (refunds left out), each card a cardholder, sampled whole
    with a seed until about --claims transactions; they become the cardholder's expense claims, paid with the
    company card;
  - supplier payments, without the beneficiaries the council redacts, each supplier sampled whole until about
    --bills payments; each becomes a supplier bill with no reference (the data has none) paid the same day;
  - dates moved by one year into October 2025 - September 2026 (the simulation's year);
  - pounds turned into VND at 30,000, kept exact: a whole number of pounds is round to 100,000 VND (O25's
    unit) exactly when it is a multiple of 10 pounds.

No name leaves this script: cardholders are numbered, suppliers keep only the council's supplier number.
"""
import argparse
import csv
import datetime
import hashlib
import json
import random

GBP_TO_VND = 30_000
FIRST, LAST = datetime.date(2024, 10, 1), datetime.date(2025, 9, 30)
SHIFT = datetime.date(2025, 10, 1) - FIRST
CITATION = ("Birmingham City Council, Purchase Card Transactions and Payments to Suppliers over £500, "
            "October 2024 - September 2025. Contains public sector information licensed under the Open "
            "Government Licence v3.0.")


def read(path):
    with open(path, "rb") as handle:
        digest = hashlib.sha256(handle.read()).hexdigest()
    with open(path, encoding="utf-8-sig", newline="") as handle:
        return list(csv.DictReader(handle)), digest


def day(text):
    when = datetime.date.fromisoformat(text[:10])
    return when if FIRST <= when <= LAST else None


def vnd(pounds):
    return round(pounds * GBP_TO_VND)


def sample(groups, target, rng):
    """Whole groups in a seeded order until about target rows."""
    keys = sorted(groups)
    rng.shuffle(keys)
    taken, size = [], 0
    for key in keys:
        if size >= target:
            break
        taken.append(key)
        size += len(groups[key])
    return taken


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--cards", required=True)
    parser.add_argument("--payments", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--claims", type=int, default=1000)
    parser.add_argument("--bills", type=int, default=800)
    parser.add_argument("--seed", type=int, default=1)
    options = parser.parse_args()
    rng = random.Random(options.seed)

    card_rows, card_sha = read(options.cards)
    by_card, skipped = {}, {"refunds": 0, "outsideYear": 0}
    for row in card_rows:
        when, pounds = day(row["trans_date"] or ""), float(row["billing_gross_amt"] or 0)
        if when is None:
            skipped["outsideYear"] += 1
        elif pounds <= 0:
            skipped["refunds"] += 1
        else:
            by_card.setdefault(row["card_number"], []).append((when, pounds))
    cards = sample(by_card, options.claims, rng)
    claims = [{"holder": number, "date": str(when + SHIFT), "amount": vnd(pounds)}
              for number, card in enumerate(cards, 1) for when, pounds in sorted(by_card[card])]

    payment_rows, payment_sha = read(options.payments)
    by_supplier, redacted = {}, 0
    for row in payment_rows:
        when, pounds = day(row["date"] or ""), float(row["amount"] or 0)
        if "redact" in (row["beneficiary"] or "").lower():
            redacted += 1
        elif when and pounds > 0:
            by_supplier.setdefault(row["supplier_number"], []).append((when, pounds, row["merchant_category"].strip()))
    suppliers = sample(by_supplier, options.bills, rng)
    bills = [{"supplier": supplier, "date": str(when + SHIFT), "amount": vnd(pounds), "category": category}
             for supplier in suppliers for when, pounds, category in sorted(by_supplier[supplier])]

    out = {"citation": CITATION, "seed": options.seed, "sha256": {"cards": card_sha, "payments": payment_sha},
           "holders": len(cards), "claims": claims, "suppliers": suppliers, "bills": bills,
           "skipped": dict(skipped, redactedPayments=redacted)}
    with open(options.out, "w", encoding="utf-8") as handle:
        json.dump(out, handle, ensure_ascii=False)
    print("cards %d -> %d holders, %d claims; suppliers %d -> %d, %d bills; skipped %s" % (
        len(by_card), len(cards), len(claims), len(by_supplier), len(suppliers), len(bills), out["skipped"]))


if __name__ == "__main__":
    main()
