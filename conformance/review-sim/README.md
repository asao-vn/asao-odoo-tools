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

