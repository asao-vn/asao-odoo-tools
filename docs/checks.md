# Review checks as files

`odoo-cli review run` looks for business issues in an Odoo's books: a bill entered twice, a payment split
under an approval limit, an expense on a day of leave. Each issue is a **check**. A check can be a file,
`odoo-cli.check/v1`, run by one engine:
- **What goes in:** the records of one verified read operation over the period.
- **What comes out:** the same findings every check gives.

Anyone who can write the file can add an issue, without Go and without the SDK.

## A check file

```json
{
  "schema": "odoo-cli.check/v1",
  "id": "X-CLAIMS-PILING-UP",
  "version": 1,
  "name": "Chi phí dồn dập",
  "question": "Có ai kê nhiều khoản chi trong vài ngày không?",
  "kind": "dấu hiệu gian lận",
  "formula": "min_count claims or more of one employee within window_days",
  "params": {"window_days": 7, "min_count": 3},
  "source": {"operation": "people.expense.list", "period": "date", "where": [["state", "not in", ["draft", "refused"]]]},
  "match": {"kind": "groups", "key": ["employee_id.id"], "date": "date", "within": "window_days",
            "aggregate": "count", "threshold": "min_count"},
  "severity": [{"if": "count >= 5", "then": "NEN_XU_LY"}, {"then": "THEO_DOI"}],
  "finding": {"title": "Nhiều khoản chi trong một tuần",
              "subject": {"type": "employee", "id": "{first.employee_id.id}", "name": "{first.employee_id.name}"},
              "why": "{first.employee_id.name}: {count} khoản chi từ {from} đến {to}, tổng {money(total)}.",
              "amount": "total"},
  "cases": [
    {"name": "three claims in a week", "records": {"people.expense.list": ["..."]}, "expect": [{"evidence": [1, 2, 3]}]},
    {"name": "three claims over a month", "records": {"people.expense.list": ["..."]}, "expect": []}
  ]
}
```

- **`source`:** a read operation of the catalog (`odoo-cli catalog list --kind READ`).
  - `domain` is sent to Odoo.
  - `period` names the date field that places a record in the period.
  - `where` filters what comes back: `=`, `!=`, `in`, `not in`.
- **`match`:**
  - `pairs` pairs records of one key that follow each other within `within` days. `flag` names records of
    the pair that must meet a condition, `atLeast` of them. This covers duplicates.
  - `groups` takes records of one key within `within` days of the first, counted (`count`) or summed
    (`sum(field)`) against `threshold`. Each record must be below `eachBelow` when it is set, and there must
    be `minCount` records at least. This covers piling up and splitting.
- **`severity`:** the first rule whose `if` holds, written `name op number`. The names are `gap`,
  `flagged_count`, `count` and `total`.
- **`finding`:** text in braces is filled in from the match: `{first.field}`, `{first.relation.name}`,
  `{second.field}`, `{money(...)}`, `{join(flagged)}`, `{gap}`, `{count}`, `{total}`, `{from}`, `{to}`,
  `{params.name}`.
- **`cases`:** at least one record set the check must flag and one it must leave alone. A file without both
  is refused.

Two built-in checks are files today: O22 (a payment recorded twice) and O23 (an expense claimed twice).
They are in `third_party/packages/odoo/review/checks/`. Before their Go code was removed, both versions
gave byte-identical findings on 786 cases.

## The commands

```sh
odoo-cli review test-check --file X-CLAIMS-PILING-UP.json                 # the format, the operation, the cases
odoo-cli review add-check --profile my-odoo --file X-CLAIMS-PILING-UP.json   # runs in review run from now on
odoo-cli review run --profile my-odoo --from 2026-07-01 --to 2026-09-30
odoo-cli review verdict --profile my-odoo --finding X-CLAIMS-PILING-UP-1a2b3c4d5e6f --false-alarm --note "site trip"
odoo-cli review precision --profile my-odoo                                 # each check's share of real findings
```

- **The key.** Every finding has a `key`, its check and its evidence, the same from one run to the next.
- **Verdicts.** A verdict on a key is shown on that finding in later runs. `review precision` lists the false
  alarms by key: each one is a look-alike case its check's file should gain.

## How a check grows

| Stage | What holds it |
|---|---|
| proposed | a file in a pull request, reviewed by its owners (`.github/CODEOWNERS`) |
| cased | `review test-check` passes; CI runs every file's cases |
| simulated | the `review-sim` workflow scores as before with the check in |
| held-out | a held-out set written after the check passes (`--fresh`) |
| field | people's verdicts on real data |

- **Versions.** A change to a file's formula or parameters raises its `version` and goes through the
  stages again.
- **The weekly run.** The `review-sim` workflow runs every Monday: a 12-month business with everyday
  noise and the held-out set, drawn from a new seed each week. A check that held only on one draw fails
  there.
