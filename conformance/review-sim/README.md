# Odoo review checks: the simulation that proves them

DEC-2026-0165 says a review check is proven from data before anything else. This folder holds that proof for
the Odoo pack. The checks themselves are not here: they live once in ASAO's core, in
`packages/odoo/review`, and run as `odoo-cli review run` (and, governed, as `asao aerp review`). This folder
holds:
- an Odoo 19 lab;
- six months of a simulated Vietnamese trading company written through Odoo's own business flows;
- the errors and frauds a review must find, planted in that data, with a look-alike it must not flag;
- a scorer, and a tracer that holds an agent's report to the numbers.

Every name in the simulation is fictional: the companies, the products, the tax codes (99xxxxxxxx), the
phones, the bank and its accounts, and the e-mail domain (example.com).

| File | What it does |
|---|---|
| `lab-up.sh`, `lab-down.sh` | Odoo 19 (pinned image) without demo data, l10n_vn chart, VND, on 127.0.0.1:58741; an odoo-cli profile `review-sim` |
| `setup.py`, `simulate.py` | run in `odoo shell`; `simulate.py` prints `TRUTH=` (planted cases and decoys, with the records that prove them) |
| `score.py` | caught / missed planted cases, flagged decoys, and every finding the truth does not explain |
| `trace-numbers.py` | every number in an agent's report must trace to the review (or to records the agent read, given with `--reads`) |

```sh
S=/tmp/review-sim
scripts/dev/odoo-review-sim/lab-up.sh --out $S
podman exec -i odoo-review-sim-web sh -c 'odoo shell --db_host="$HOST" --db_user="$USER" --db_password="$PASSWORD" -d reviewsim --no-http' \
  < scripts/dev/odoo-review-sim/simulate.py > $S/sim.out
sed -n 's/^TRUTH=//p' $S/sim.out > $S/truth.json
export XDG_CONFIG_HOME=$S/xdg ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true AERP_CREDENTIAL_REVIEW_SIM="$(cat $S/secret/api-key)"
odoo-cli review run --profile review-sim --from 2026-04-01 --to 2026-09-30 --markdown-file $S/report.md --output json \
  | python3 -c 'import json,sys; json.dump(json.load(sys.stdin)["data"], open(sys.argv[1], "w"))' $S/review.json
scripts/dev/odoo-review-sim/score.py --truth $S/truth.json --review $S/review.json
scripts/dev/odoo-review-sim/lab-down.sh
```

The run of 2026-10-14 is in `evidence-2026-10-14/`. The checks were first written as a Python prototype
here and proven:
- 16 of 16 planted cases caught;
- the decoy (a monthly rent) not flagged;
- one finding outside the truth, which is real in the data.

They were then ported to Go, and on the same data the port gave the same 28 findings and the same figures.
The prototype was removed, so the formulas live in one place.

For the agent's memo to the director, `trace-numbers.py` checked all 55 of its numbers against the Go
review, and it refuses a memo with a made-up number.

## 2026-10-15: six more checks, and a defect the new data found

The scenario gained ten planted cases and nine look-alikes, one or more for each new check (`O02` a customer
invoiced twice, `O07` a payment cut under the approval limit, `O26` documents dated back into an ended month,
`O27` month-end revenue credited early the next month, `O28`/`O29` purchases of 5 million đồng or more paid in
cash). Every document is now created on its own day (an UPDATE of `create_date` at the end of the run), so
only the planted backdated ones look backdated. The run is in `evidence-2026-10-15/`:
- 24 of 24 planted cases caught;
- 11 of 11 look-alikes left alone;
- the review identical on two runs, in about 2 seconds;
- one finding outside the truth, which is real in the data: one of Lan's random office-supply claims falls on
  the last day of her planted leave (O24).

The first run of the day had three more findings outside the truth, all from `O21`. A credit note that only
cancels an unpaid invoice is marked paid by Odoo (the invoice becomes "reversed"), and `O21` read that as money
refunded out. `O21` now requires an invoice the customer had paid; a test holds it.

## 2026-10-17: a bigger, messier business, in the open

`run.sh` does the whole run (lab, business, `odoo-cli review run`, score) on any machine with Docker or
Podman. The public repository runs it as the `review-sim` workflow. `--months`, `--scale` and `--mess`
size the business:
- `--months` sets how many months, ending with September 2026;
- `--scale` multiplies the regular sales;
- `--mess` adds a real shop's everyday noise:
  - partial returns credited;
  - customers paying in two parts;
  - supplier bills paid in instalments;
  - discounts above cost;
  - a clearance week where every seller sells the old primer below cost;
  - cancelled orders.

  Each kind of noise is a look-alike in the truth, and it draws from its own generator, so the business
  underneath is the same with or without it.

| Business | Planted caught | Look-alikes left alone | Findings outside the truth | Run |
|---|---|---|---|---|
| 6 months, the 2026-10-15 business | 24/24 | 11/11 | 1 (real) | [38985512771](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38985512771) |
| 12 months, before the fixes | **23/24** | 11/11 | 1 | [38986245449](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38986245449) |
| 12 months with noise, before the fixes | **23/24** | **14/16** | 3 | [38986251024](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38986251024) |
| 12 months, after the fixes | 24/24 | 11/11 | 0 | [38991185860](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38991185860) |
| 12 months with noise, after the fixes | 24/24 | 16/16 | 0 | [38991191468](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38991191468) |

The year found four formulas that held only on a small, clean business. Each was fixed in
packages/odoo/review, with a test of its own:
- **O25:** a share taken over a year drowned a burst of round claims. It now also looks at every
  92-day window.
- **O19:** a clearance week counted against every seller. A product sold below cost by two sellers or
  more within 14 days is now a markdown.
- **O21:** a partial return looked like a refund paid out. Paid out now means an outbound payment
  settles the credit note.
- **O02:** a customer reordering the same goods looked like one sale invoiced twice. Two invoices
  each from its own sale order are two sales. The invoice read gained `invoice_origin`, which was
  re-verified on 19.0 and 20.0.

**The caveat.** The fixes were made on these look-alikes, so the clean runs after them show the fixes
work. They do not show that the formulas hold on look-alikes nobody has seen. That needs a fresh set,
as the upgrade check had.

## 2026-10-17: the held-out set, on three kinds of business

**What was written.** After the fixes above, a held-out set was written: 18 planted cases (19 answers,
since one supplier is both O17 and O18) and 13 look-alikes, in shapes the earlier cases did not take.
Examples:
- a supplier reference retyped in lower case;
- an invoice copied two days later;
- a bill paid in four parts;
- the accountant's own supplier;
- 8% VAT on tiles that carry 10%;
- half of a paid invoice credited and paid out;
- a team lunch claimed by two people;
- a claim on a refused leave;
- two vendors paid in cash on one day.

The set sits in November 2025 - March 2026, where nothing else is planted (`--fresh`, 12 months or
more). It was not used to change a check. `--profile` shapes the everyday sales:
- trader: the business above;
- retail: a building-materials store selling every product to walk-in customers, paid at the counter;
- service: the trade plus many small installation tickets paid on the spot.

| Business, 12 months | Sales | All planted | All look-alikes | **Held-out planted** | **Held-out look-alikes** | Run |
|---|---|---|---|---|---|---|
| trader, with noise | 692 | 42/43 | 29/29 | 18/19 | 13/13 | [38993568020](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38993568020) |
| retail | 1901 | 42/43 | 24/24 | 18/19 | 13/13 | [38993572677](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38993572677) |
| service | 2063 | 42/43 | 24/24 | 18/19 | 13/13 | [38993577194](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38993577194) |
| trader, with noise, answer corrected | 692 | **43/43** | **29/29** | **19/19** | **13/13** | [38995070451](https://github.com/asao-vn/asao-odoo-tools/actions/runs/38995070451) |

**The one miss in the first three runs was the answer key, not a check.** O07 found the 36-million
bill paid in four 9-million parts in every run, and it names the payments. The held-out answer named
only the bill, so the scorer missed the match; the original split case names the payments. The answer
was corrected (5c3b07a6). No check changed between these runs.

**Findings outside the truth.** The findings outside the truth were O24: a random claim that fell on a
planted approved leave, which is real in the data.

**What this shows, and what it does not.**
- On cases written after the formulas were fixed, every check found its case, and no look-alike was
  flagged.
- The result held for three shapes of business, from about 700 to 2,000 sales a year.
- The set was still written by the person who wrote the checks. A set written by someone else, real
  Vietnamese cases, and real data are the next tests.

## 2026-10-17: real sales, nothing planted in them

Every case above was written by the person who wrote the checks, the look-alikes and the everyday noise
included. `--real-sales FILE` adds what nobody arranged: the real sales of a UK online retailer, **UCI
Online Retail II** (Chen, 2012, [doi:10.24432/C5CG6D](https://doi.org/10.24432/C5CG6D), CC BY 4.0).

`prepare_uci.py` (standard library only) does the following:
- takes the sheet of December 2010 - November 2011;
- samples customers, whole, until about 500 invoices (`--seed`);
- moves the dates into the simulation's year;
- turns GBP into VND at 30,000.

The real noise is kept: returns and cancellations, manual lines, postage, free items, changing prices.
The simulator then replays the sample on top of the business:
- invoices, each with its real number as origin, paid on the day;
- each cancellation as a credit note against the customer's latest earlier invoice with that product,
  refunded within three days.

The scorer counts the findings on these records apart, for a person to judge. The public workflow takes
`real_sales: true`: it downloads the archive, checks its sha256 and samples it.

**Two assumptions, and what they do:**
- **Cost.** The data has no cost, so a product's cost is 70% of its median price. This drives O19.
- **Sellers.** The data has no sellers, so each customer is given one of the three sellers. This shapes
  O19's per-seller grouping.

**The first run.** 12 months, the trader business, plus 123 real customers: 421 invoices, 79 credit notes
(67 linked to an invoice), 7,992 lines and 1,929 products. The full score is in
[results-real-2026-10-17](results-real-2026-10-17/score.md).

| What | Result |
|---|---|
| The planted cases, among the real sales | 24/24 caught, 11/11 look-alikes left alone, nothing else on the simulated records |
| Findings on the real sales | **38**: O21 36, O19 2 |
| O02 (an invoice twice), O22 (a payment twice) | none, although the real customers often order the same again on one day |

**What it says:**
1. **The checks are not blinded by real noise.** Every planted case was still found among 500 real
   invoices.
2. **O21 does not carry over to a business where refunds are routine.** It flagged 36 of 79 real
   returns. A refund after payment is rare in the simulated trader, and an everyday thing for an online
   shop.
   - A fixed rule cannot hold for both. O21 needs a base rate: a seller's or a customer's refunds
     against the business's own norm, a refund above the invoice, or one invoice refunded twice.
   - Two of the 36 had a refund above the invoice. That comes from the replay's linking (one return may
     cover several invoices), not from the shop.
3. **One real finding is worth a person's look.** INV/2026/00568 was credited twice on the same day for
   the same amount (two credit notes of 327,800 đ, from two identical cancellation lines in the source).
4. **O19's two findings are about the assumptions,** not evidence: the sellers are assigned, the costs
   assumed, and some lines are free items.
5. **O02 and O22 held.** The origin rule of 2026-10-17 keeps real repeat orders from being called
   duplicates.

**Next, done as a measurement and not as a fit:** O21 gains a base rate, tuned on this sample (`--seed 1`)
and measured on customers it has not seen (`--seed 2`), so that the fix is not fitted to the 36.

