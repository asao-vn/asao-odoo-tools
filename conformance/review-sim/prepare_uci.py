#!/usr/bin/env python3
"""Real sales for the review simulation: a sample of UCI Online Retail II, made ready to replay in Odoo.

    prepare_uci.py --xlsx online_retail_II.xlsx --out uci-sales.json [--invoices 500] [--seed 1]

The dataset is the real transactions of a UK online retailer, 2009-2011, by Daqing Chen, UCI Machine
Learning Repository, https://doi.org/10.24432/C5CG6D, licensed CC BY 4.0. Nothing in it was planted: its
returns, cancellations, repeat orders, manual entries, postage and price changes are what a real shop does.
That is the point of it here. The planted cases measure what a check catches; this measures what a check
says about a business nobody arranged.

What is kept, from the sheet Year 2010-2011:
  - invoices of customers with an id, from 2010-12-01 to 2011-11-30, moved by a fixed number of days into
    October 2025 - September 2026 (the simulation's year);
  - a seeded random sample of customers, taken whole, until about --invoices invoices;
  - every line of their invoices, cancellations ('C' invoices, negative quantities) included;
  - prices in GBP turned into VND at 30,000 and rounded to the thousand.

A product's cost is not in the data. It is set to 70% of the product's median price in the sample, a
stated assumption, so that a line sold at a deep discount is below cost the way it would be in a shop.

Read with the standard library only: the workbook is a zip of XML, streamed sheet row by row.
"""
import argparse
import datetime
import json
import random
import re
import statistics
import xml.etree.ElementTree as ET
import zipfile

NS = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
GBP_TO_VND = 30_000
SHEET = "xl/worksheets/sheet2.xml"  # Year 2010-2011
FIRST, LAST = datetime.date(2010, 12, 1), datetime.date(2011, 11, 30)
SHIFT = datetime.date(2025, 10, 1) - FIRST
EXCEL_EPOCH = datetime.datetime(1899, 12, 30)


def shared_strings(z):
    out = []
    for _, node in ET.iterparse(z.open("xl/sharedStrings.xml")):
        if node.tag == NS + "si":
            out.append("".join(t.text or "" for t in node.iter(NS + "t")))
            node.clear()
    return out


def rows(z, strings):
    """Each data row as a dict of its column letters."""
    for _, node in ET.iterparse(z.open(SHEET)):
        if node.tag != NS + "row":
            continue
        row = {}
        for cell in node.iter(NS + "c"):
            column = re.match(r"[A-Z]+", cell.get("r")).group(0)
            kind, value = cell.get("t"), cell.find(NS + "v")
            if kind == "inlineStr":
                row[column] = "".join(t.text or "" for t in cell.iter(NS + "t"))
            elif value is None:
                continue
            elif kind == "s":
                row[column] = strings[int(value.text)]
            else:
                row[column] = value.text
        node.clear()
        yield row


def number(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--xlsx", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--invoices", type=int, default=500)
    parser.add_argument("--seed", type=int, default=1)
    options = parser.parse_args()
    z = zipfile.ZipFile(options.xlsx)
    strings = shared_strings(z)
    invoices, names = {}, {}
    for i, row in enumerate(rows(z, strings)):
        if i == 0:
            continue  # the header
        invoice, code, customer = row.get("A"), row.get("B"), number(row.get("G"))
        quantity, price, serial = number(row.get("D")), number(row.get("F")), number(row.get("E"))
        if not invoice or not code or customer is None or quantity is None or price is None or serial is None:
            continue
        day = (EXCEL_EPOCH + datetime.timedelta(days=serial)).date()
        if not FIRST <= day <= LAST:
            continue
        code = str(code).strip().upper()
        names.setdefault(code, (row.get("C") or code).strip()[:60])
        entry = invoices.setdefault(invoice, {"no": invoice, "date": (day + SHIFT).isoformat(), "customer": int(customer),
                                              "country": row.get("H") or "", "cancel": invoice.startswith("C"), "lines": []})
        entry["lines"].append([code, quantity, round(price * GBP_TO_VND, -3)])
    by_customer = {}
    for entry in invoices.values():
        by_customer.setdefault(entry["customer"], []).append(entry)
    customers = sorted(by_customer)
    random.Random(options.seed).shuffle(customers)
    chosen, kept = [], []
    for customer in customers:
        if len(kept) >= options.invoices:
            break
        chosen.append(customer)
        kept.extend(by_customer[customer])
    kept.sort(key=lambda e: (e["date"], e["no"]))
    prices = {}
    for entry in kept:
        for code, quantity, price in entry["lines"]:
            if price > 0 and quantity > 0:
                prices.setdefault(code, []).append(price)
    used = sorted({code for entry in kept for code, _, _ in entry["lines"]})
    products = [{"code": code, "name": names.get(code, code),
                 "price": statistics.median(prices[code]) if code in prices else 1000,
                 "cost": round(0.7 * statistics.median(prices[code]), -3) if code in prices else 0}
                for code in used]
    document = {
        "source": "UCI Online Retail II, sheet Year 2010-2011",
        "citation": "Chen, D. (2012). Online Retail II [Dataset]. UCI Machine Learning Repository. https://doi.org/10.24432/C5CG6D",
        "license": "CC BY 4.0",
        "changes": "sampled by customer (seed %d), dates moved by %d days, GBP to VND at %d, cost assumed 70%% of median price"
                   % (options.seed, SHIFT.days, GBP_TO_VND),
        "customers": chosen, "products": products, "invoices": kept,
    }
    json.dump(document, open(options.out, "w", encoding="utf-8"), ensure_ascii=False)
    cancels = sum(1 for e in kept if e["cancel"])
    print("customers %d, invoices %d (%d cancellations), lines %d, products %d" % (
        len(chosen), len(kept), cancels, sum(len(e["lines"]) for e in kept), len(products)))


if __name__ == "__main__":
    main()
