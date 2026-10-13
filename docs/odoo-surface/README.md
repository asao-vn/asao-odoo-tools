# Odoo business surface: inventory, surface catalog and live sweep

Date: 2026-10-10. Labs: Odoo 19.0-20260803 (`lab19`) and 20.0-20260926 (`vn20`), each with the same
16-module SME set: contacts, crm, sale_management, purchase, stock, account, project, hr,
hr_holidays, hr_attendance, hr_expense, hr_timesheet, mrp, point_of_sale, calendar, website.

## What Odoo exposes

`scripts/dev/odoo-surface-inventory.py` reads it from the instance: business models, their combined
list and form views, field metadata, and the form's business buttons.

| | 19.0 | 20.0 |
|---|---:|---:|
| Business models | 223 | 238 |
| Form buttons | 419 | 468 |
| Models with a list and a form view | 192 | |
| Distinct object-button methods on those | 233 | |

Before this work the curated catalog covered 36 of the 192 models (18%) and 12 of their 316
object buttons (3%).

## The surface catalog

`scripts/dev/odoo-catalog-surface.py` generates `packages/odoo/odoo19_surface_catalog.gen.go` from
the 19.0 inventory. It covers the uncovered models, except the ones listed in its `SKIP` table:
helpers, SQL views, IAP and PDF tooling, users and roles.

- **Output:** 599 operations over 138 models:
  - 254 list/describe and 254 create/update.
  - 68 business actions.
  - 23 wizards. 13 of them answer the dialog a business method responds with. Families: finance, manufacturing, people, catalog, workforce, inventory, pos,
  work, crm, identity, schedule, structure, sales.
- **Curated models:** for the twelve models the curated catalog owns (`CURATED_ENTITY`), the generator
  adds only operations for their uncovered business buttons and dialogs (47 on 19.0). These are named under the curated entity and use
  the curated write capability, for example:
  - `workforce.leave.approve` / `.refuse` / `.back_to_approval` / `.cancel`
  - `procurement.order.approve` / `.lock` / `.unlock`
  - `finance.payment.post` / `.cancel`
  - `inventory.transfer.confirm`
- **Hand-written inputs:** naming (`FAMILY`, `ENTITY`, `METHOD_VERBS`), exclusions (`SKIP`,
  `SKIP_METHOD*`), reserved names and the wizards a method opens (`MANUAL_WIZARDS`) are tables in
  the generator, reviewed like code.
- **Discipline:** the operations are `SURFACE_GENERATED` (`packages/odoo/surface_catalog.go`).
  - They are standalone-only. They are absent from `Catalog()`/`CatalogByID`, and only an `Adapter`
    with `Surface` set (the standalone executor's) runs them.
  - Their readiness is UNVERIFIED until a conformance record names them with their current
    definition (see "Evidence is bound to the definition it verified").
  - Field names are provider-native.
  - Reads ask unconditionally only for `id` and `display_name`.

`surface-report-19.0.json` lists every model's generated operations and its 20.0 differences.

## Wizards

The dialogs a business form opens (`type="action"`, target `new`) are generated from
`WIZARD_VERBS`. Each confirming button of the wizard becomes one operation:

- `finance.invoice.reverse` and `.reverse_and_replace` (credit note)
- `crm.lead.mark_lost`
- `inventory.transfer.return`, `.return_all` and `.return_for_exchange`
- `manufacturing.order.change_quantity`
- `pos.order.pay`
- `people.employee.load_contract_template`

They are opened the way Odoo's own button opens them:

1. The context names the record: `active_model`, `active_id` and `active_ids`.
2. The context also carries what the action and the button add. These are parsed from Odoo's own
   definitions, but only when the value is a constant or the record itself, as in
   `crm.lead.lost`'s `default_lead_ids: active_ids`.
3. `onchange` supplies the prefilled defaults the web client shows, such as
   `account.move.reversal`'s `journal_id`.
4. The caller may set only the wizard's own editable fields, and anything else is refused before
   contact. No link field is written, because a wizard's field back to the record can be an output
   (`account.move.reversal.new_move_ids`).

## Live read sweep (standalone `odoo` CLI, `--allow-unverified`)

Every surface `list` operation was run, and every `describe` operation whose `list` returned a
record. Results are in `read-sweep-19.0.json` and `read-sweep-20.0.json`.

| | OK | Not OK |
|---|---:|---:|
| 19.0 | 168 / 168 | 0 |
| 20.0 | 155 / 163 | 8 |

The 8 failures on 20.0 are the 8 models 20.0 removed. Each one is now refused before contact with
`OPERATION_UNAVAILABLE_ON_SERIES`, and the refusal says what replaced the model:

| Model | What replaced it |
|---|---|
| `account.group` | parent accounts |
| `res.bank` | fields of `res.partner.bank` |
| `stock.scrap` | `stock.move` with `is_scrap` |
| `hr.leave.type` | `hr.work.entry.type` |
| `hr.contract.type` | contract terms on `hr.version` |
| three `hr.attendance.overtime.*` models | removed |

Writes and actions checked live on 19.0:

- `catalog.category` create, update and describe
- `people.job.create`
- `crm.tag.create`
- a manufacturing order run through `manufacturing.order.create`, then `.confirm`, then `.mark_done`.
  It ended in state `done`.
- `procurement.order.lock` and `.unlock` on a confirmed purchase order. An independent provider read
  saw `locked` go to `true`, then back to `false`.
- `finance.invoice.reverse` on a posted customer invoice. It created an `out_refund` that references
  the invoice, with the caller's reason and the full amount.
- `crm.lead.mark_lost`. The lead became `active: false` with `won_status: lost`.
- `manufacturing.order.change_quantity`. A confirmed order's quantity went to 5.

This work also fixed READ operations ignoring `--order`, which the CLI publishes. The catalog's
default sort is now used only when the caller gives none.

## Read conformance and promotion (2026-10-10)

`scripts/dev/odoo-read-conformance.py` runs every UNVERIFIED read through the real standalone CLI
and compares it with an independent JSON-RPC oracle. Lists compare ids in order; describes compare
each field value the provider has. The evidence is in `evidence/` and holds hashes and statuses
only.

| Series | AGREE | AGREE on an empty model | Describe, no record | Other |
|---|---:|---:|---:|---|
| 19.0 | 100 | 77 | 77 | no disagreement |
| 20.0 | 79 | 78 | 78 | 1 agreement with a dialect-rebuilt field left unchecked; 16 removed-model refusals; 2 for a missing module |
| 19.0, Odoo demo data (`lab19demo`), on the 154 reads still UNVERIFIED | 100 | 27 | 27 | no disagreement |

Only AGREE was promoted, as a reviewed manifest change:

- **19.0:** 100 records from the empty lab, plus 100 from the demo-data lab, for 200 in all. Four are
  the curated Gate 2 reads, which turns the curated release gate green. The other 196 are surface
  reads.
- **20.0:** 79 records, in the separate 20.0 manifest.

A promoted read runs without `--allow-unverified` on 19.0. On 20.0 the series itself still needs
that flag, because it is a candidate.

## Write conformance and promotion (2026-10-10)

`scripts/dev/odoo-write-conformance.py` checks every UNVERIFIED create/update against
`lab19demo`, in four steps:

1. It builds the smallest valid record: each field the provider requires and cannot default. A
   related record is the provider's first of the target model. A code takes a value that fits the
   field's size, and a phone takes a valid number.
2. It creates the record through the CLI.
3. It reads the record back through the independent oracle and compares every written value.
4. It updates one simple field through the CLI and reads that back as well.

| Result | Count |
|---|---:|
| AGREE | 178 |
| DISAGREE (`people.expense.create`: Odoo recomputes `price_unit` from the product) | 1 |
| Provider refusal (21 operations) | 21 |
| Not run: no record to update, nothing simple to write, or a required field the operation cannot supply | 50 |

The 21 refusals are business constraints and environment: duplicates and overlaps, an open POS
session, access groups the demo admin lacks, a journal without a suspense account.

The 178 AGREE are promoted, which makes 374 surface operations CURRENT_VERIFIED on 19.0.

The first run found one generator defect. A required stored field that Odoo declares readonly
(`mrp.workorder.production_id`, `project.update.project_id`) was not offered for writing, so the
create could never succeed. Required stored fields are now always writable.

## Vietnam: SInvoice e-invoicing and VietQR (2026-10-10)

`l10n_vn`, `l10n_vn_edi_viettel` and `l10n_vn_edi_viettel_pos` are installed on both 19.0 labs,
which were then inventoried again (225 business models).

New operations:

| Operation | What it is |
|---|---|
| `finance.einvoice_symbol.list` / `.describe` / `.create` / `.update` | Invoice symbols (ký hiệu), `l10n_vn_edi_viettel.sinvoice.symbol` |
| `finance.einvoice_template.list` / `.describe` / `.create` / `.update` | Invoice templates (mẫu số), `l10n_vn_edi_viettel.sinvoice.template` |
| `finance.invoice.send_einvoice` | Issue the invoice to SInvoice through Odoo's own Send & Print wizard, with the SInvoice EDI selected and no e-mail. Risk HIGH: an issued tax invoice is legally binding |
| `finance.invoice.einvoice_send_payment_status` | The invoice form's "Send Payment Status" button |

A method, not a dialog action, opens the Send & Print wizard. The values that select SInvoice
therefore cannot be read from a view, so `send_einvoice` is a hand-written entry in
`MANUAL_WIZARDS`. Odoo 19 accepts them as written: `extra_edis` is a list of EDI keys.

The modules also changed existing operations:

- The reversal wizards (`finance.invoice.reverse`, `.reverse_and_replace`) accept the Vietnamese
  adjustment fields: `l10n_vn_edi_adjustment_type` and the agreement document's name and date.
- Bank accounts (`identity.bank_account.*`) gain the VietQR fields: `proxy_type` (bank account, ATM
  card, merchant id, payment service), `proxy_value` and `include_reference`.
- POS payment methods (`pos.payment_method.*`) gain `qr_code_method` = `emv_qr`.

Evidence:

| Operations | Result |
|---|---|
| Symbol and template list/describe/create/update (8) | AGREE on `lab19demo`, promoted |
| `send_einvoice` | Wiring check, not promoted (below) |
| `einvoice_send_payment_status` | Not run |

**The `send_einvoice` wiring check:**

- **Setup:** no company held SInvoice credentials (checked before the run). The CLI ran
  `send_einvoice` on `INV/2026/00001`.
- **What happened:** the run reached Odoo's SInvoice path, which refused at its own configuration
  check: "Error when generating SInvoice file. - Sinvoice credentials are missing … - VAT number is
  missing … - The invoice symbol must be provided."
- **Side effects:** none. The invoice's SInvoice state and file were unchanged. Odoo checks the
  configuration before it calls Viettel, so no request left the lab.

**Why the issuing path stays UNVERIFIED:** the SInvoice endpoint is hard-coded to Viettel's
production service (`api-vinvoice.viettel.vn`), with no sandbox switch.

- Issuing with credentials needs a Viettel test account and an explicit decision.
- So does `einvoice_send_payment_status`. On an invoice already sent, it logs in to Viettel even
  with empty credentials.

**Known limit:** a statement line's form is the `account.move` form, so
`finance.bank_statement_line.create` / `.update` also accept the SInvoice fields.

- This mirrors Odoo's UI: its invoice form lets a user edit `l10n_vn_edi_invoice_state`, because
  the field is `readonly=False` and the view does not lock it.
- A statement line's move is an `entry`, never an invoice.
- The generator cannot evaluate a conditional `invisible`, so such fields stay.

## Evidence is bound to the definition it verified

Every surface record in both manifests carries a `definitionHash`
(`odoo.OperationDefinitionHash`).

- **What it covers:** what the operation asks of the provider: model, strategy, read projections,
  writable fields and their typing, relations, method, wizard and modules.
- **The rule:** a record counts only while the operation still has that definition
  (`recordStillApplies`, `packages/odoo/catalog_readiness.go`). When a regenerated operation asks
  something else of the provider, it drops back to UNVERIFIED instead of keeping evidence for
  another definition.
- **Why:** installing the Vietnamese modules changed nine existing definitions, five of them
  promoted:
  - `identity.bank_account.create` / `.describe` / `.update`
  - `pos.payment_method.create` / `.update`

  They were demoted, run again on the Vietnamese lab, and agreed again.
- **Earlier records:** their hashes were backfilled. Each backfill was checked first: the
  operation's generated entry is byte-identical to the one in the commit whose run verified it. That
  is 369 records on 19.0 and 76 on 20.0.
- **Tooling:**
  - `catalog describe` publishes `definitionHash`, and both runners record it.
  - `scripts/dev/odoo-promote-conformance.py` turns AGREE results into records and skips any without
    a hash.
- **Limit:** the hash covers the catalog entry, not the 20.0 dialect. The generated part of the
  dialect is pinned instead (`odoo20RenamesVerified`, next section). Its hand-written derived fields
  and write rules are code, so a change to them goes through review.

**Totals on 19.0** (before the action runs below): 422 of the surface operations were CURRENT_VERIFIED: 240
reads and 182 creates/updates. **On 20.0:** 359 records: 327 surface operations (179 reads, 148 creates/updates)
and 32 curated ones.

## Odoo 20: renames derived from the inventories (2026-10-10)

`scripts/dev/odoo-dialect-renames.py` compares the two inventories and writes
`packages/odoo/odoo20_dialect_renames.gen.go`. It pairs a 19.0 field that 20.0 no longer has with a
field 20.0 added to the same model only when all of these hold:

- exactly one added field has the same type, relation, label, readonly flag and selection values;
- no other vanished field matches that added field.

A `REVIEWED` table adds the pairs the rule cannot see, each checked against the 20.0 source. These are
the leave-type fields, which became work-entry-type fields when `hr.leave.type` became
`hr.work.entry.type`.

| Result | Count |
|---|---:|
| Renames, over 24 models | 39 (35 derived, 4 reviewed) |
| Candidates rejected, each with its reason | 160 |
| Vanished fields with no counterpart | 126 |

The table, the rejections and the dropped fields are in `dialect-renames-20.0.json`. Examples:

- **Applied:** `product_uom_id` → `uom_id` on eleven models (BoM, BoM line, by-product, manufacturing
  order, work order, unbuild, lot, vendor price, …).
- **Applied:** the bank account's `acc_number` → `account_number` and the bank address fields →
  `street`, `city`, `zip`, `state_id`, `country_id`.
- **Applied:** the POS settings `is_*` → `use_*`, and `expense_policy` → `reinvoice_policy`.
- **Rejected:** `timesheet_invoice_type` → `billable_type`, because the values changed completely.
- **Rejected:** `acc_type` → `account_type`, because 20.0 added `clabe`.
- **Rejected:** `number_of_hours_display` → `number_of_hours`, because the field became readonly.

How the table is wired in:

- The dialect merges the table at init and keeps its hand-written derived fields and write rules
  (`addRenames`). A conflict stops the program: a renamed field that is also derived or rewritten,
  two fields sharing one target, or a removed model.
- The five renames that used to be written by hand are now in the generated table.
- `l10n_vn_edi_viettel.sinvoice.template` joined the removed models: in 20.0 a template is a code
  on the symbol.
- The table is part of `ExecutionBuildIdentity`, so a runtime with an older table cannot pass as
  current.

**Conformance under the new dialect.** Both runners take `--renames`: a renamed field is written
under its 19.0 name and compared with the 20.0 field the dialect names. `--readiness ANY` runs every
operation, because the readiness that `catalog list` shows is 19.0's. Results on `vn20`:

- **Reads:** 102 AGREE before the write runs, 206 after them.
- **Creates/updates:** 150 AGREE, plus 3 more on a second pass. The second pass hits the uniqueness
  rules the first one satisfied, such as one currency rate per day.
- **Renamed fields:** 8 describes read `product_uom_id` through `uom_id`, and 4 creates wrote it. All
  agreed.
- **Earlier records:** all 79 agreed again. The 20.0 manifest was rebuilt from these runs: 79 → 359
  records.
- **The pin:** `odoo20RenamesVerified` (`provider_dialect_test.go`) pins the table these runs used.
  A regenerated table fails that test until the 20.0 conformance is re-run.

Observed and left alone:

- **Curated describes DISAGREE on both series:** seven of them. Curated operations report Odoo's
  `False` as `null` and translate selections (`01_in_progress` → `in-progress`); the runner compares
  raw values. Their evidence comes from the curated gate, not from these runs.
- **A 20.0 provider defect:** reading `account.bank.statement.is_valid` fails inside Odoo
  (20.0-20260926) with an SQL syntax error (`st.journal_id IN ()`). The oracle reproduces it without
  the CLI. `finance.bank_statement.list` therefore fails on 20.0.
- **New required fields on 20.0:** 20.0 requires fields that 19.0 did not, so some creates cannot
  be satisfied there. Examples are `document_tax_mode` on sale and purchase orders, and an employee
  on HR lines when the user has none.

## Dialogs: a question is not a result (2026-10-10)

Some Odoo buttons answer with a dialog instead of acting: an `ir.actions.act_window` whose target
is `new`. Examples:
- A transfer done only partly asks whether to create a backorder.
- Cancelling time off asks for a reason.
- Scrapping more than the location holds asks for confirmation.

Until the dialog is confirmed, the button has not done what it says.

**The defect.** The executor reported any answer from a METHOD_CALL as success, so it reported each
of those dialogs as a completed operation. This was reproduced on `lab19demo`:
`workforce.leave.cancel` answered `SUCCESS`, and the leave stayed `validate`. The defect also
reached the governed product: the curated `inventory.transfer.validate` reported a partial transfer
as validated.

**Which buttons do this.** `scripts/dev/odoo-dialog-scan.py` reads the source of the installed
modules inside the Odoo container. For every object button of the inventory, it follows the
method's calls into the model's other methods, three levels deep. Calls are followed because a
dialog is often returned by a helper such as `_pre_action_done_hook`. The scan then resolves window
actions on the live instance.

On 19.0, 31 of the 317 object buttons can answer with a dialog. The evidence is in
`dialogs-19.0.json`.

**The fix.** It lives in `packages/odoo/provider_dialog.go` and is part of the execution identity.
- A METHOD_CALL answered with a dialog now fails with `BUSINESS_PRECONDITION_FAILED`, and the error
  names the dialog.
- `performed` states what is known:
  - `NO` for a dialog Odoo shows before acting.
  - `UNCONFIRMED` for a dialog not reviewed into the tables.
- `resolve_with` lists the operations that answer the dialog.
- A dialog Odoo offers after acting keeps the success and is reported next to it. The only such
  dialog on 19.0 is `account.autopost.bills.wizard`, which follows a vendor bill post.
- Whether a dialog comes before or after the action is a reviewed table, read from the source.
- The generator refuses a catalog in which a METHOD_CALL can reach a dialog that is not in either
  table.

**Operations that answer dialogs.** These are WIZARD operations with a `WizardOpenerMethod`. Each
one calls the method the way the web client's button does, then answers the dialog Odoo returns:
- The dialog is opened in the context Odoo gave it. A context given as source text is evaluated for
  literals and the active record only; anything else is refused.
- Where Odoo asks in a chain, the operation answers each dialog in turn (`WizardAlsoAnswers`).
- A method that acts when it has nothing to ask (`WizardOpenerActs`) has done the operation in that
  case. For example, a transfer with nothing left over needs no backorder decision.

The operations are generated from `OPENED_WIZARDS` in the generator:

| Operation | Dialog it answers | Checked live on `lab19demo` |
|---|---|---|
| `workforce.leave.cancel` (was a METHOD_CALL) | `hr.holidays.cancel.leave`, with `reason` | `validate` → `cancel` |
| `people.expense.refuse` (was a METHOD_CALL) | `hr.expense.refuse.wizard`, with `reason` | → `refused` |
| `people.expense.post` (was a METHOD_CALL) | `hr.expense.post.wizard` (employee-paid expenses) | → `posted`, bill created |
| `people.expense.approve_despite_duplicate` | `hr.expense.approve.duplicate` | plain `approve` reports the dialog; not exercised further (the lab approves on submit) |
| `inventory.transfer.validate_with_backorder` | `stock.backorder.confirmation` → Create Backorder | partial receipt → `done`, 1 backorder |
| `inventory.transfer.validate_without_backorder` | `stock.backorder.confirmation` → No Backorder | partial receipt → `done`, no backorder |
| `inventory.scrap.validate_despite_shortage` | `stock.warn.insufficient.qty.scrap` | `draft` → `done` |
| `manufacturing.order.mark_done_with_backorder` / `_without_backorder` | `mrp.production.backorder` | not reached in the lab: the consumption question comes first |
| `manufacturing.order.mark_done_confirm_consumption` | `mrp.consumption.warning` | answered, then the backorder question is reported |
| `manufacturing.order.mark_done_confirm_consumption_with_backorder` / `_without_backorder` | both, in Odoo's order | 2 of 5 produced → `done`; the backorder is split off, or the order is closed |
| `finance.invoice.request_cancel` (was a METHOD_CALL) | `l10n_vn_edi_viettel.cancellation` | not run: it cancels at Viettel production |

The plain operations now report their dialogs, and the lab state shows nothing was done:
- `inventory.transfer.validate` on a partial transfer stays `assigned`.
- `inventory.scrap.validate` with no stock stays `draft`.
- `manufacturing.order.mark_done` on a partly produced order stays `progress`.

**Dropped from the catalog.** Three buttons only open a form or a dialog with no decision a view
states:
- `manufacturing.order.unbuild` and `workforce.accrual_plan.create_accrual_plan_level`. Use the
  record's create operation instead.
- `manufacturing.order.generate_serial`.

With this change the surface catalog had 620 operations. The action conformance run then brought
it to 599 (below).

**Register Payment needs no new operation.** It is the curated `finance.payment.register`.

## Action and wizard conformance (2026-10-10)

`scripts/dev/odoo-action-conformance.py` gives actions and wizards an oracle-backed run, the way
reads and writes have one. An action operation claims to do what its Odoo button does. For each
UNVERIFIED METHOD_CALL and WIZARD operation, the runner works in four steps.

1. **Find twins.** The runner looks for two records on which Odoo's form shows the button: the
   button's `invisible` expression, read from the inventory and evaluated in Python, is false.
   - The two records must agree on what that expression reads.
   - They must also agree, if possible, on the state, partner, company and kind of record; failing
     that, on the state alone.
   - Archived records are included, because some buttons are for them (restore).
   - Where the lab's data holds no twins for an operation that matters, `FIXTURES` makes two
     identical records through the oracle and drives them to the right state. Fixtures exist for
     draft and posted payments, confirmed and locked orders, partial receipts, partly produced
     manufacturing orders, unstocked scraps and future leaves.
2. **Run the CLI** on the first record.
3. **Run an independent web-client oracle** on the second record. It shares no code with the CLI:
   - It calls the method with the button's context.
   - It opens the wizard with the defaults the web client loads, an `onchange` over every field of
     the dialog.
   - It answers the dialogs the operation's catalog entry says the operation answers.

   `catalog describe` now publishes that entry as `providerCall` for surface actions.
4. **Compare** the two records' stored fields before and after. For relations, it compares the
   number of records they hold.

The outcomes:

| Outcome | Meaning | Promoted |
|---|---|---|
| AGREE | Both succeeded, the same fields changed, selections and booleans reached the same values, and relations gained or lost the same number of records | Yes |
| AGREE_NO_CHANGE | Nothing changed on either record | No |
| AGREE_REFUSED | Both paths refused | No |
| AGREE_DIALOG | Both answered with the same dialog | No |
| INCONCLUSIVE_TWINS_DIFFER | The twins differed beforehand in the fields that then differed | No |
| DISAGREE | The two paths acted differently | No |

Operations whose modules reach a third party are never run here: Viettel SInvoice is
production-only.

The runner was run three times on `lab19demo`:

| Run | Operations | AGREE | Promoted |
|---|---:|---:|---|
| `action-conformance-19.0-demo-20261010.json` | 112 | 21 | the 17 METHOD_CALLs. The wizards ran again after the defaults fix below. |
| `…-demo-rerun-20261010.json` | 74 | 6 | all 6 |
| `…-demo-fixtures-20261010.json` | 68 | 23 | all 23 |

That makes 46 promoted (29 actions, 17 wizards). They include:
- every dialog-answering operation that could be run: transfer validation with and without
  backorder, the five manufacturing completions, scrap despite shortage, leave cancellation, and
  expense posting and refusal;
- payment post, cancel and reset;
- invoice reversal: the credit note appears in `reversal_move_ids` on both twins;
- order unlock;
- manufacturing plan and unplan;
- lead lost and restore.

`packages/odoo` allows a promoted action or wizard only on `action-conformance-` evidence.

**Totals on 19.0:** 468 of the 599 surface operations are CURRENT_VERIFIED:

| Kind | Verified |
|---|---:|
| Reads | 240 |
| Creates/updates | 182 |
| Actions | 29 |
| Wizards | 17 |

**Defects the runner found**

- **A wizard's defaults were asked field by field.** The executor asked `onchange` only for the
  fields a caller may set. A default that depends on another field, such as
  `change.production.qty`'s `product_qty` (the order's quantity, found through `mo_id`), came back
  0. It was then written over the value Odoo itself would have filled in, so
  `manufacturing.order.change_quantity` without `--set product_qty` failed with "The quantity to
  produce must be positive!". The executor now asks for every plain field of the dialog, as the
  web client does, and keeps only the caller-settable ones.
- **The inventory read buttons from embedded sub-views.** A default form, one the model does not
  declare, embeds the list view of every x2many field, header buttons of other models included.
  `account.bank.statement.line` showed `action_post` of its payments and the reschedule buttons of
  its activities. `calendar.event` showed the attendee buttons `do_accept`, `do_decline` and
  `do_tentative`. 21 generated operations therefore called other models' methods. The inventory
  now skips sub-views; the 19.0 inventory was taken again from `lab19`, with fields and
  projections unchanged, and the catalog went from 620 to 599 operations. The committed 20.0
  inventory predates the fix: it is used for the renames (fields) and the report only, and taking
  it again would also pick up view changes that the conformance runs caused on `vn20`.

**What the twin method cannot settle**
- **Shared resources.** `manufacturing.order.assign` reserves stock, and the first twin takes what
  the second would have reserved, so the run reports DISAGREE.
- **Records the lab makes impossible.** `inventory.transfer.return` fails on both paths: 19.0
  return lines start at quantity 0, and the CLI cannot set lines. `return_all` is the usable one.

## JSON-2: the protocol that replaces JSON-RPC (2026-10-10)

Odoo 19.0 deprecated its external JSON-RPC and XML-RPC API (`/jsonrpc`, `/xmlrpc`). The `common`
and `object` services go away in Odoo Online 21.1 (winter 2027) and on-premise Odoo 22 (fall 2028).
The replacement is JSON-2, which Odoo 19.0 and 20.0 already serve:
- each call is `POST /json/2/<model>/<method>`;
- the credential is an API key sent as a bearer token;
- the database is named in the `X-Odoo-Database` header;
- the body holds `ids`, `context` and every argument by its name in the Python signature.

**How the standalone CLI speaks it.** A profile chooses its protocol: `odoo profile set …
--protocol json2`. Its credential is then an API key, supplied exactly as a password would be.
Everything above the RPC client is unchanged: the transport, the 20.0 dialect, the executor and
the evidence. `packages/odoo/rpc_json2.go`, part of the execution identity:
- **Version.** It asks `/web/webclient/version_info`, the web client's own route. That route needs
  no credential and is not part of what Odoo removes. The series gate therefore still runs before
  the API key is sent.
- **Arguments.** It turns `execute_kw`'s positional arguments into named ones, using Odoo's
  signatures (`json2Signatures`). JSON-2 refuses positional arguments and unknown names, so a
  method called with an unnamed positional argument is refused before anything is sent. `odoo
  call` then says to pass the arguments by name with `--kwargs-json`.
- **Errors.** Odoo answers with an HTTP status and the same serialized exception as JSON-RPC, so
  errors classify the same. A refused API key (401) is `ACCESS_DENIED`, as a wrong password is.
- **Redaction.** The API key is redacted from errors and results like a password.

**API keys differ between 19.0 and 20.0.**
- On 19.0, a key made under Preferences > Account Security has no scope, and JSON-2 accepts such a
  global key.
- On 20.0, a key must have a scope, and the JSON-2 route requires the scope `rpc`. The UI offers
  it as "RPC".

A 19.0-style global key is refused as "Invalid apikey". `odoo profile set --help` says so.

**Parity.** The read, write and action runners ran again with the CLI on JSON-2 and `--readiness
ANY`. The oracle stayed on its own JSON-RPC connection. The evidence files are
`*-json2-20261010.json`.

| Series | Kind | Previously promoted operations that agree over JSON-2 | The others |
|---|---|---|---|
| 19.0 | Surface reads | all 240 | none |
| 19.0 | Writes | 183 | data the earlier runs left behind |
| 19.0 | Actions and wizards | 32 of 46 | twins that differ or no longer exist (below) |
| 20.0 | Reads in the 20.0 manifest | all | none |
| 20.0 | Writes | 147 | the same uniqueness rules |

The writes that did not agree hit uniqueness rules on records the JSON-RPC runs had created: one
currency rate per day, a collaborator added twice, an attendance still open, overlapping holidays.

Where the actions differ, the twins explain it:
- One expense was company-paid (posted straight to `paid`) and the other employee-paid
  (`posted`). The runner now also matches twins on `payment_mode`.
- The first manufacturing order took the stock the second would have reserved.

No difference came from the protocol.

**Not yet done**
- The governed product (organization-service `odoo19exec`) still speaks JSON-RPC.
- No Odoo Online database has been tried. Online needs the Custom plan and runs `saas~` series,
  which the series gate refuses (see the conversation of 2026-10-10 on Online).

## Standard operations for models the curated catalog owns (2026-10-10)

The generator leaves models the curated catalog owns to it. Several of those models had no write
operation at all: the company, journals, taxes, payment terms, working schedules, leaves,
payments, transfers, warehouses and locations. Setting up ASAO's own company through the catalog
(`contracts/business/`) showed each of these as an `UNSUPPORTED` step.

`CURATED_GAPS` now generates, under the curated name, the standard operations the curated catalog
lacks. The list is reviewed per model; 35 operations in all, for example:
- `structure.company.describe` and `.update`;
- `finance.journal`, `finance.tax`, `finance.payment_term` and `finance.account.chart`: describe,
  create and update. Journals, taxes and accounts are HIGH risk.
- `workforce.calendar.*`, `workforce.leave.*`, `people.department.update`,
  `finance.payment.create/update` and `inventory.transfer.create/update`.

Journal items, stock moves and quants get no writes: writing them directly bypasses the documents
that keep the books and the stock consistent. A company is updated, not created, because a new
company is a provisioning step.

The 35 operations are new; no existing definition changed. The surface catalog had 634 operations, 644 once the website ones below were added, 648 with `website.media`, 654 with the guarded deletes, 656 with the settings, 716 with bookkeeping, counts, notes and files, and 727 with printing and the deployment system parameters.

**Evidence.** The write runner now also runs an update that has no create beside it: it updates
one simple field of the newest existing record, reads it back and restores it. On `lab19demo`
(`*-curated-gaps-20261010.json`):
- All 9 describes agree.
- 17 writes agree: uom, journal, payment term, location, transfer and calendar create/update;
  warehouse update; and the update-only company, department, variant and milestone updates.
- The rest are refused by Odoo's own rules on the runner's sample data and stay UNVERIFIED:
  - an account needs a code per company;
  - a payment needs a payment method line;
  - a tax group must share the tax's country;
  - a leave needs a valid allocation and period.
- `structure.warehouse.create` DISAGREES. Odoo makes a new warehouse's stock locations itself, so
  the `lot_stock_id`/`view_location_id` it is given do not hold.

**Website (for ASAO's landing page, `contracts/business/README.md`).**
- **Extra fields.** A `CURATED_GAPS` entry may name fields the form does not show but a business
  sets as code:
  - `website.page.arch`, the page's QWeb;
  - the website's `logo`, `favicon` and `custom_code_head`;
  - the company `logo`.
- **New operations.** `website.site.update`, `website.page.describe/create/update` and
  `website.menu.describe/create/update` all agree with the oracle.
- **`website.layout.list/describe/update`.** These reach `ir.ui.view`, but their provider domain
  holds them to seven layout views: header call to action, header text, header search, sign-in
  (`portal.user_sign_in`), footer, copyright and brand promotion. The domain names `active`, so a
  view that was switched off is still found.
  - On a view outside the seven, the CLI refuses the update as "not eligible for this operation",
    which is the scope working.
  - The list and describe are CURRENT_VERIFIED (2026-10-11): the read runner now selects within the
    provider domain the catalog publishes (`providerDomain` in `catalog describe`). The update stays
    UNVERIFIED: its only text field is the view architecture, which the write runner never writes.
- **`website.media.list/describe/create/update`.** These reach `ir.attachment`, held by their
  provider domain to binary files whose url starts with `/media/`. Create and update always write
  `public`, `type: binary` and `res_model: ir.ui.view` (forced values), so whatever they publish is
  a public file Odoo serves at its own url (`ir.http._serve_fallback`, with range requests). The
  landing page loads its hero video, posters and fonts from there.
  - The domain only scopes the records an operation reaches, not the values it writes. A new
    operation field, `InputPrefixes`, holds the url they write to `/media/` as well, as a clean path
    (no `.`/`..` segments, no doubled slashes). The value is refused as `VALIDATION_FAILED` before
    Odoo is contacted, so no write can put a public file at another path of the site
    (`website_media_scope_test.go`). `InputPrefixes` enters an operation's definition hash only
    when it is set.
  - `datas` cannot be read back. The spec planner compares it through the attachment's
    `checksum` (SHA-1 of the content), so an unchanged file is not sent again.
  - A create must name its url. Without one the file would land outside `/media/`, where these
    operations could neither list, update nor delete it. The adapter refuses such a create
    (`MissingFieldError`), and `catalog describe` publishes the url as required on create, with its
    `prefix`. A conformance run found this: its media, created without a url, then appeared in no
    list. All four operations are CURRENT_VERIFIED since 2026-10-11.
  - These operations stay UNVERIFIED, like `website.layout.*`.
- **Company logo.** Adding `logo` to `structure.company.update` voided its record. `--drop-stale`
  removed it, and the operation agreed again under the new definition.
- **Guarded deletes** (`GUARDED_DELETES`), for the records a business spec marks `absent`:
  `crm.stage.delete`, `crm.lost_reason.delete`, `work.task_stage.delete`, `people.job.delete`,
  `website.menu.delete` and `website.media.delete`. They are DESTRUCTIVE_GUARDED, like the curated
  `people.department.delete` and `work.project.delete`: each names the records that point at its
  target (leads, tasks, employees, child menus), and the delete is refused while any exists.
  `website.media.delete` has none to name (a page refers to a file by its url), and its provider
  domain is its whole guard: only binary files under `/media/`.
  - Every guarded delete now counts archived dependents too. Odoo's searches skip them, yet a delete
    strands or cascades them all the same: deleting a project removes its archived tasks, and a lost
    lead (which Odoo archives) keeps pointing at its lost reason. On the replay database the guard
    refused to delete a lost reason that only an archived lost lead named; Odoo itself would have
    cleared the lead's reason without a word.
  - These operations stay UNVERIFIED: the conformance runners leave destructive operations out.
    Their behaviour was shown on the replay database instead (`contracts/business/README.md`).

- **`structure.settings.describe/update`**, Odoo's Settings page (`res.config.settings`), held to the
  reviewed fields of `SETTINGS_FIELDS`: feature switches, numbers and closed choices for the modules a
  business spec installs. No `module_*` field, no chart of accounts, no field naming a record by id and
  no credential (`settings_scope_test.go`).
  - Two new operation flags. `ReadNewRecord` makes a read return a new record's values as the web
    client gets them when the page opens (`onchange` with nothing set). `default_get` alone leaves out
    the related fields (`auth_signup_uninvited`, `portal_confirmation_sign`, ...), and the stored
    records of a TransientModel are only past saves. `WizardStandalone` is a wizard no record opens:
    the caller's values make it, every other field keeps what the page shows, and `execute` applies
    it. Both enter an operation's definition hash only when set.
  - Shown on the replay database: switching `group_use_lead` on gave employees the leads group, and
    `quotation_validity_days` reached the company; switching back left both as they were.
  - These operations stay UNVERIFIED.

## Reads that page, group and export (2026-10-10)

Every READ operation, curated or surface, in both products, takes three more inputs (`--offset`,
`--group-by`, `--aggregate`), and every CLI read prints CSV with `--output csv`:
- **Paging.** `--offset N` skips N records, with `--limit` and `--order` as before.
- **Grouping.** `--group-by field` or `--group-by date_field:month` (day, week, quarter, year and the
  `*_number` parts), with `--aggregate field:sum` (avg, min, max, count, count_distinct, bool_and,
  bool_or). Odoo groups and aggregates, through `formatted_read_group`, the call its own grouped lists
  and pivots make. Each group carries its record count. Without `--group-by`, the aggregates cover
  every record the filter matches.
- **Scope.** A grouped read names only fields the read already shows, refused as `VALIDATION_FAILED`
  before Odoo is contacted. Grouping never reveals more than reading does. The provider domain still
  applies.
- **Shape.** A relation group is `{id, display_name}` and a date bucket `{value, display_name}`. A
  group without a value for one of them (tasks without a project) is null, as a read reports it.
- **Series.** Grouping runs on 19.0. On the candidate 20.0 series it is refused
  (`OPERATION_UNAVAILABLE_ON_SERIES`) until renames are translated into grouping specifications.
- **Governed path.** The governed path carries the same inputs: `aerpbridge` puts them in the input
  document its plan signs.

So that statistics can be grouped by date, person and amount, five curated reads gained optional
fields:
- `sales.quotation.list`: `date_order`, `validity_date`, `user_id`, `team_id`, `amount_untaxed`,
  `currency_id`.
- `finance.invoice.list`: `invoice_date`, `invoice_date_due`, `date`, `amount_untaxed`,
  `amount_residual`, `currency_id`, `invoice_user_id`, `journal_id`.
- `crm.lead.list`: `create_date`, `date_deadline`, `date_closed`, `user_id`, `team_id`,
  `lost_reason_id`.
- `work.task.list`: `create_date`, `date_deadline`, `date_last_stage_update`, `allocated_hours`,
  `effective_hours`, `partner_id`, `milestone_id`.
- `procurement.rfq.list`: `date_order`, `date_approve`, `date_planned`, `user_id`, `amount_untaxed`,
  `currency_id`.

That changed the definitions of the three that hold 20.0 evidence. Their records were voided, and a
read run on 20.0 agreed again (`read-conformance-20.0-widened-reads-20261010.json`); no operation that
agreed in the earlier 20.0 runs disagrees now. On the 19.0 demo lab (`lab19demo`, JSON-2), the
statistics answer what Odoo's own pivots would:
- confirmed sales by month;
- posted customer invoices by payment state, with the amount still due;
- the pipeline by stage, with expected revenue;
- tasks by project.

## The money flows, verified on 19.0 (2026-10-10)

The curated money flows held only historical evidence on 19.0. That was 28 operations, among them
confirming a sale, invoicing it, posting the invoice, registering its payment, confirming a purchase,
receiving it and billing it. They now hold current evidence from the same runners as the surface:
- **The action runner runs curated operations named by `--only`.** `catalog describe` now publishes
  `providerCall` for curated actions and wizards too, with a wizard's link field: that is what the
  oracle redoes.
- **Fixtures.** The lab's own twins differed in what these buttons branch on: an alternative RFQ
  pending, a loyalty reward, a journal entry picked next to invoices. Three flows "disagreed" for that
  reason alone. Each flow now runs on fresh identical twins:
  - a quotation, and an order with an invoiceable line;
  - an RFQ;
  - a draft customer invoice, and a posted one;
  - a fully picked receipt;
  - an opportunity;
  - an order billed on ordered quantities.
- **The web client's own path.** Where the web client reaches the same business action another way,
  the oracle takes that way (`CURATED_OPENERS`, `CURATED_BUTTONS`):
  - Register Payment opens the payment dialog through `action_register_payment`;
  - Won calls `action_set_won_rainbowman`, which wraps `action_set_won`;
  - Lost answers the Mark Lost dialog;
  - Create Bills is a header button of the purchase list.

**Result.** 9 AGREE, each with the same fields changed on both paths, and promoted:
- `sales.order.confirm`, draft to sale;
- `sales.invoice.create`, to invoice to invoiced;
- `finance.invoice.post`, draft to posted;
- `finance.payment.register`, not paid to paid;
- `procurement.order.confirm`, draft to purchase, with its receipt;
- `inventory.transfer.validate`, assigned to done;
- `procurement.bill.create`, to invoice to invoiced, with its bill;
- `crm.opportunity.win`;
- `crm.opportunity.lose`.

Ten historical lists also agreed with the read oracle and were promoted. The curated catalog now
holds 96 CURRENT_VERIFIED operations and 9 HISTORICAL_ONLY. Those 9 are:
- four curated describes, whose semantic field names the read oracle does not map yet;
- `crm.opportunity.convert` and `finance.invoice.update`, curated writes outside the write runner's
  create/update pairs;
- `inventory.transfer.reserve`, for want of twins with something to reserve;
- the two guarded deletes, since the runners never run a destructive operation.

## Bookkeeping, counts, notes and files (2026-10-10)

60 surface operations cover what the coverage review found missing. Each was first tried against
Odoo 19 directly, and then run through the CLI on `lab19demo`:
- **Manual journal entries** (`finance.journal_entry.list/describe/create/update/post/reset_to_draft`,
  held to `move_type = entry`).
  - Odoo refuses a line that leaves its entry unbalanced, even in draft ("The entry is not
    balanced"), so an entry is made with all its lines in one call.
  - `line_ids` is a list of line objects (`LineInputs`), each checked and resolved on its own and
    written as a `(0, 0, {...})` command; an error names the line (`line_ids[0]`).
  - A line names its account by code. A code looks like an id (6427), and the resolver took a
    numeric string for one, so it would have posted to whichever account had that id. A relation
    can now say a string is always a name (`ByName`).
- **Inventory counts** (`inventory.count.set/list/apply/clear`).
  - `set` writes in inventory mode (`WriteContext`), so it lands on the quant it counts.
  - `apply` and `clear` reach only counted quants. Odoo's `action_apply_inventory` applies a quant's
    inventory quantity whether it was counted or not, and an uncounted quant's is 0: applying one
    wiped a product's stock in the lab during the trial, which was then restored.
- **Notes and activities** on ten records a business talks about: leads, sales and purchase orders,
  invoices, journal entries, projects, tasks, contacts, transfers and expenses. They are
  `<entity>.log_note` (`message_post`) and `<entity>.schedule_activity` (`activity_schedule`).
  - A METHOD_CALL can take keyword arguments now: `MethodKwargs` are fixed and always win, so a note
    stays an internal note (`mail.mt_note`) whatever the caller sends; `MethodInputs` and
    `MethodRequiredInputs` are what the caller gives.
  - An activity's user is resolved by name.
  - An activity names its due date (`date_deadline` is required). Odoo's `activity_schedule` makes an
    activity due today when none is given, where the web client's dialog proposes the activity type's
    delay (five days for a To-Do). The first conformance run found the two paths a week apart, and
    the catalog guesses neither.
  - All twenty are CURRENT_VERIFIED since 2026-10-11: each path left one note or activity on its
    twin, the same in type, subtype, author, summary, user and due date.
- **Files** on the same ten records (`<entity>.attachment.list/describe/create`).
  - `describe` returns the content (`datas`); a download matched the uploaded bytes.
  - `create` can attach only to a record the entity reaches: a relation may carry a domain now
    (`RelationSpec.Domain`). Attaching to a journal entry through the invoice operation was
    refused, through the journal entry operation it was not.
- **PDF reports**: see the next section.

These operations are UNVERIFIED until conformance runs cover them (the trials above are not runner
evidence). The fields they add (`ByName`, `Domain`, `WriteContext`, `LineInputs`, the method
keywords) enter an operation's definition hash only when set, so no existing evidence changed.

## Printing, the way the web client does it (2026-10-11)

Odoo renders a record's PDF report only for a logged-in web session (`/report/pdf/<report>/<ids>`,
`auth="user"`). An API key, as a bearer token or in basic auth, gets the login page, and the render
method (`ir.actions.report._render_qweb_pdf`) is private, so no RPC reaches it.

The CLI therefore does what the web client's Print menu does (`packages/odoo/rpc_report.go`):
1. logs a web session in with the user's password;
2. asks for the report;
3. checks that the answer is a PDF;
4. logs the session out.

It is driven by:
- **The REPORT strategy.** Seven operations, each naming its report:
  - `sales.order.print` (PDF Quote);
  - `procurement.rfq.print` and `procurement.order.print`;
  - `finance.invoice.print` (Invoice PDF, held to invoice documents);
  - `finance.payment.print` (Payment Receipt);
  - `inventory.transfer.print` (Delivery Slip);
  - `manufacturing.order.print` (Production Order).

  The result is the PDF in base64, with its size and SHA-256, named after the record
  (`INV_2026_00017.pdf`). `--save-file` writes it to a path; it also saves one attachment's content
  from `<entity>.attachment.describe`.
- **A web password.** The profile's own reference with `.web`: `AERP_CREDENTIAL_<REF>_WEB`, keyring
  account `<ref>.web`, or that key in `AERP_CREDENTIAL_FILE`. A JSON-RPC profile's password serves
  without one. A JSON-2 profile holds an API key, so without a web password a print is refused before
  anything is sent (`WEB_CREDENTIAL_REQUIRED`, naming the variable).
- **Refusals.** A refused login, the login page (a redirect from the report route), and an answer
  that is not a PDF are refused (`ACCESS_DENIED`). A report name is checked against
  `module.report_name` before anything is sent.
- **`report.url`.** In a container, Odoo's PDF renderer fetches the report's style and images
  through `web.base.url`, the public address, which the container cannot reach. The report then
  prints without its header, logo and fonts. Setting the system parameter `report.url` to the address
  Odoo reaches itself on (its own port, `http://127.0.0.1:8069`) fixes it: the demo lab's invoice
  printed with its company header after that. The CLI sets it through
  `structure.system_parameter.*`, held to `report.url`, `web.base.url` and `web.base.url.freeze`.
  - Odoo accepts any key, so the key's closed values are the only scope. Until 2026-10-11 the
    surface's closed values (`InputEnums`) were published but enforced only for curated operations.
    A conformance run then created a parameter with an arbitrary key. The adapter now refuses a value
    outside an operation's closed values before Odoo is contacted (`EnumValueError`,
    `surface_enum_scope_test.go`); clearing a field (`false`) is still the provider's to judge.

All seven printed through the CLI on `lab19demo`: a one-page PDF each, named after its document.
All seven are CURRENT_VERIFIED since 2026-10-11 (`scripts/dev/odoo-report-conformance.py`,
`evidence/report-conformance-19.0-demo-*20261011.json`). The runner prints the newest record inside
the operation's provider domain through the CLI. It then downloads the same report the way the
browser does: a web session, then `/report/pdf/<report>/<id>`. Both PDFs had the same pages and the
same text, over JSON-RPC and over JSON-2 with a web credential. Without one, a JSON-2 profile is
refused (`WEB_CREDENTIAL_REQUIRED`).

**Totals on 19.0:** 593 of the 727 surface operations are CURRENT_VERIFIED: 291 reads, 219
creates/updates, 53 actions, 17 wizards, 7 prints and 6 guarded deletes. With the curated catalog,
696 of 832 operations are current, 134 unverified and 2 historical only
(`crm.opportunity.convert`, `finance.invoice.update`). The 2026-10-11 runs
(`evidence/*-conformance-19.0-demo-new-ops-20261011.json`) added 40 reads and 15 writes: files on
ten kinds of record, journal entries, counts, the Settings page, system parameters, website media
and layouts, pricelist items, analytic distribution models and payments. The read runner now honors
each operation's provider domain, and checks a new-record describe (the Settings page) against a
transient record the oracle creates. The write runner now writes a scope-held input inside its
scope.
- Before that run, the 19.0 demo lab was seeded with records the empty models lacked: a pricelist
  item, an analytic distribution model, a counted quant (counted quantity equal to the quantity on
  hand), a weekly event, and the Wire Transfer provider in test mode with one transaction and one
  token.
- Payment token reads agree but cannot be recorded: the manifest's credential guard refuses the word
  `token` anywhere in it (`GUARDED_WORDS`), so an operation named for one never gets a record.
- `inventory.package_history.*` found no record to compare.
- The action run (`evidence/action-conformance-19.0-demo-new-ops-20261011.json`) verified 24 actions.
  - Notes and activities on the ten records are compared by what each path left (see above).
  - Posting and resetting journal entries used fresh entries. Twins found on the account.move form
    were invoices, which the journal entry operations refuse, rightly; the runner now seeks twins
    inside the operation's provider domain.
  - Posted twins go to two general journals. Resetting an entry that is not the last of its
    sequence flags a gap, so each twin must be the last of a sequence of its own.
  - Applying and clearing counts used two quants counted at exactly what is on hand.
- Curated operations with only historical evidence went from 9 to 2 the same day, over JSON-2
  (`evidence/*-19.0-demo-json2-*20261011.json`):
  - The four curated describes (product, invoice, contact, employee) agree with the oracle. A curated
    answer speaks the contract's names (`stock_managed` for `is_storable`), its enum spellings
    (`goods` for `consu`, now published as `providerValues`), and shapes a relation as
    `{id, display_name}`. The read runner reads it back through what `catalog describe` publishes.
    The same sweep re-ran every read: 350 of 352 agree, the other two have no data.
  - `inventory.transfer.reserve` agrees on deliveries confirmed and then unreserved (confirmed to
    assigned on both twins).
  - The guarded deletes, two curated and six surface, agree. The runner deletes one of two fresh
    records inside the domain through the CLI and unlinks the other, as the web client's Delete
    does. Both are gone, and nothing else in the domain is.
- **A weak lab password shows through the CLI's redaction.** The CLI replaces its credential
  wherever it appears in an answer. With the lab password `admin`, `admin@example.com` came back as
  `[REDACTED]@example.com`, and Odoo's "administrator" as `[REDACTED]istrator`. Over JSON-RPC two
  describes therefore disagreed; over JSON-2 (an API key) they agree. A real password that is also an
  ordinary word would mask that word in business data the same way.

## Business buttons of list views (2026-10-11)

The generator used to take a model's form buttons only. Some business actions sit only on the
model's list: a work order's Start, Pause and Finish, a transfer's Unreserve, and a reorder rule's
Order Once and Automate. `business_buttons` now takes the form's buttons, then the list's. A list
button acts on the selected records as a form button acts on the open one.

**Seven operations were added and verified** on fresh twins, through JSON-2:

| Operations | Fixture |
|---|---|
| `manufacturing.workorder.start` / `.pause` / `.finish` | A fresh product and a bill of materials on a work center the demo does not block |
| `inventory.transfer.unreserve` | Reserved deliveries |
| `inventory.reorder_rule.replenish` / `.automate` / `.clear_manual_quantity` | Manual rules short of their minimum |

Evidence: `action-conformance-19.0-demo-json2-list-buttons*.json`.

**Left out, with the reason recorded in `SKIP_METHODS`:**
- `copy_to_bom` copies into the bill of materials that only the list's context names.
- The POS list's Create Invoices opens `pos.make.invoice`. The form's `pos.order.pos_order_invoice`
  already does that.
- The IAP lead generation is a paid service.
- The replenishment information popup.
- The website page's debug view.
- The quant relocation dialog needs a destination that no list states.

The action runner finds a list button as it finds a form button.

**Coverage after this.** Of the business buttons on the screens the 16 SME apps show, every one is
in one of these states:
- covered;
- left out on purpose: sending documents by email, creating users or companies from other records,
  paid IAP, and navigation, print and share buttons;
- covered under another name, such as `crm.opportunity.win` for Won and
  `manufacturing.unbuild.create` for Unbuild.

## The parked apps join (2026-10-11)

The 19 Community apps parked on 2026-10-10 are in the catalog now. A fresh `lab19` inventory found
307 business models, 82 of them new. The catalog grew to 1139 operations, of which 981 are verified.

**Re-verified.** The apps changed the forms of 51 verified operations. Their records were dropped
(`--drop-stale`), along with 36 more from the 20.0 manifest, and those operations ran again.

**What made the rest verifiable:**
- **Lab data.** `scripts/dev/odoo-lab-seed.py` fills the models the demo leaves empty. It goes through
  the oracle, using the model's defaults and generic required values, plus a few hand-written seeds
  (event slots, answers and tickets, repair tags).
- **Updates without a create.** The write runner updates the newest existing record when an update's
  create made none. It also tries a create whose missing required fields the catalog does not offer.
- **Inputs Odoo overrides.** The generator no longer offers fields Odoo sets itself on create
  (`OVERRIDDEN_INPUTS`): a warehouse's locations, a repair's locations, an agreement's reference, a
  recruitment source's name and an expense's unit price. The warehouse and expense creates, which had
  disagreed since 2026-10-10, now agree.
- **Navigation.** Sixteen buttons that only open another list or a URL are left out (`SKIP_METHODS`).
  Each one was called on the lab, and each returns an act_window or act_url.
- **Fixtures** for blanket orders, repairs, batch transfers, event registrations, vehicles changing
  driver and live-session surveys.

**Still unverified: 156 operations.** They are mostly writes that Odoo's business rules refuse with
generic values, models the demo cannot hold twice in a button's state, and payments that need a
provider which authorises.

## Odoo 20.0 with the same apps (2026-10-11)

The 20.0 lab (`vn20`) now runs the 19 modules the 19.0 labs gained. `stock_picking_batch` and
`hr_homeworking` are no longer on 20.0's addons path, so their operations stay unsupported there.

A fresh 20.0 inventory added one rename to `dialect-renames-20.0.json`. Because the table changed,
the 20.0 manifest was rebuilt from this day's runs only, all of them under the new table. The runs
were read, write and action, over JSON-2, with `--readiness ANY`. The empty models were seeded with
`odoo-lab-seed.py`.

The result is 759 records, against 578 before. `odoo20RenamesVerified` records the table they cover.

20.0 stays a candidate series: making it supported is a decision for the owner.

## Six more apps (2026-10-12)

The labs gained mass_mailing (with SMS), marketing_card, im_livechat, lunch, website_slides,
website_sale, website_event and data_recycle: 51 new business models.

**Families.** The new models form four families: `marketing`, `livechat`, `lunch` and `elearning`.
`data_recycle` goes under `structure`, and the eCommerce models under `catalog`, `website` and
`sales`. The governed vocabulary gains a read key for each new family, and every one of them reaches
personal data. Only `livechat` is in a preset (the manager's, which already reads contacts). `lunch`
holds each employee's orders and wallet, so it is in no preset: the statistics role excludes staff
data.

**Left out on purpose** (`SKIP_METHODS`), as for quotations and invoices:
- every button that sends email or SMS (mailings, invitations, coupons, reminders, notifications);
- buying SMS credits;
- the buttons that only open another window.

**Generator fixes.**
- A required rich-text field is offered: a lunch alert's message could not be created without it.
- Inputs Odoo sets itself are not offered: lunch jobs, and a mailing's UTM source, which replaces its
  name.

**Runner fixes.**
- **Write runner:**
  - it retries a create refused as a duplicate with other related records, one many2one at a time;
  - it samples reference fields;
  - it gives the values a generic sample cannot (`GIVEN_VALUES`, each with its reason);
  - it compares zip prefixes in upper case (`NORMALISED`);
  - it keeps update values within a field's size.
- **Action runner:**
  - a click the oracle cannot evaluate is `NOT_RUN_UNEVALUABLE`, not a disagreement;
  - lunch orders are verified on fixtures with distinct notes, because Odoo merges identical orders.
- **Seeder:** it numbers generic names, and seeds event slots, marketing cards and mailing filters.

**Lab change.** The admin of `lab19demo` has *Show Full Accounting Features*, which bank statements,
account tags and reconciliation models require.

**Result:** 1361 operations, 1201 verified on 19.0 (937 after the stale drop, before the runs). No
operation disagrees. On 20.0, 42 records were dropped as stale, leaving 717. The new apps are not yet
installed on `vn20`.

## Closing the gaps (2026-10-12, later)

**The write runner, with fields of every simple type.** An update of a model without a text field
changes a stored number, boolean, date, selection or many2one, never `state`. The new value always
differs from the current one. A create with nothing required writes one field of its own. Computed
fields that are not stored are never chosen: a write there can be ignored and still read back the
same value.

**The action runner, on the lines too.**
- *Line states.* A button may act on a record's lines rather than on the record: reserving a
  repair's parts changes its stock moves. Each one2many whose lines have a stored state is now
  compared by those states (`move_ids.state`).
- *Deletions.* A button that deletes its record, such as validating a data-cleaning proposal, agrees
  when both twins are gone.

**Fixtures:**
- purchase orders waiting for approval;
- unpublished, non-public events;
- archived maintenance requests;
- data-cleaning rules and proposals, on fresh contacts;
- landed costs on receipts of an average-cost product;
- productions with their components released.

Fixtures that need stock now make a fresh product and stock it with an applied inventory count: the
demo stock had run out. A repair's parts are stocked where Odoo's default repair type takes them
from, because the lab holds several warehouses.

**Lab fixes.** The seeder had built data-cleaning rules on `base`, an abstract model with no table.
It now uses `res.partner`, and the broken rules were removed.

**Left out:** the debug and production switches of a shipping method. They show only for a carrier
connector, and Community has none.

**Result:** 1359 operations, 1263 verified on 19.0, and none disagrees.

**20.0.**
- **Lab set-up:** `vn20` gained the same nine modules, and its admin got full accounting features.
- **Renames:** a fresh 20.0 inventory left the renames table unchanged, so `odoo20RenamesVerified`
  still holds.
- **Runs:** read, seed, write and action passes ran with `--readiness ANY`. The 20.0 manifest went
  from 717 to 1012 records.
- **Two 20.0 problems, met along the way:**
  - Odoo 20 drops the JSON-RPC connection when it cannot encode an answer that holds a binary
    default (a lunch product's image). The write runner no longer asks for binary defaults, and a
    dropped connection now fails only that operation.
  - A work order's running time differs by the seconds between the two clicks. The action runner
    ignores it (`MODEL_VOLATILE`).
- **Disagreement:** one write disagrees on 20.0, `catalog.pricelist_item.create` on `applied_on`. It
  stays unverified there.

The 96 unverified operations are of these kinds:
- **The CLI cannot see the effect.** BoM cost writes `standard_price`, which is computed and not
  stored.
- **The effect lands outside the record.** Duplicating a mailing or generating leads creates other
  records.
- **Odoo's rules refuse a generic value.**
- **A payment provider or a carrier is needed** to authorise or ship.

## Payments, the 20.0 fixtures and governed writes (2026-10-13)

**Payments.** `payment_demo`, the Demo payment provider shipped with Community, is installed on the
three labs and set to capture manually.
- **Fixture:** it authorizes payments through the module's own simulation route, the one a browser
  posts to. The transaction may stand alone or sit on a quotation or a posted invoice.
- **Capture:** a transaction's Capture always opens the capture dialog, so it is now that dialog's
  operation (`OPENED_WIZARDS`, high risk). A quotation's or invoice's Capture reaches the dialog through
  the transaction, which the dialog scan does not follow; capture those payments on the transaction.
- **Void:** voids are verified on all three. The action runner now compares record states across
  many2many relations as well (a quotation's `transaction_ids`).
- **Left out:**
  - a transaction's Post Process, whose `invisible` is the constant 1: the generator now leaves out
    every button that can never show;
  - the Demo provider's outcome-forcing buttons.

**20.0.**
- **Fixtures:** the 20.0 database has no demo data, so a fixture that finds no customer, vendor,
  product or bill of materials now makes one (`Fixtures.make_missing`).
- **Absent model:** live chat conversation tags no longer exist in 20.0 (`absentModels`).
- **Result:** the 20.0 manifest holds 1081 records, 83% of the 1297 operations that 20.0 can
  run.

## Odoo 20.0 past 90% (2026-10-13)

**A 20.0 lab with demo data.** `lab20demo` is a second 20.0 database on the same image digest
(`sha256:cdd83e83…`).
- It has the 271 modules vn20 has, created `--with-demo`, plus sale_stock and sale_timesheet.
- It is served by `odoo20demo-research-web` on 127.0.0.1:58717.
- The lab seeder and four records made in `odoo shell` filled the models it left empty: an analytic
  distribution model, an SInvoice symbol, a weekly recurrence, a counted quant.

**What 20.0 removed, now in the dialect** (`packages/odoo/provider_dialect.go`):
- **Four more models.** `stock.return.picking` (a transfer's own `action_return`/`action_return_all`
  replace it), `hr.expense.post.wizard`, `hr.version.wizard` and `l10n_vn_edi_viettel.cancellation`.
- **Fourteen business methods** that are defined nowhere in the 20.0 source:
  - mailing favorites;
  - `sale.order.action_update_taxes`;
  - POS session closing control;
  - `repair.order.action_repair_start`;
  - attendance overtime approval and refusal;
  - calendar type switches;
  - and others.

  `CallMethod` refuses each before the provider is contacted.
- **`UnavailableOnSeries`.** It says why an operation cannot run on a series. 62 of the 1358
  operations cannot run on 20.0, so **1296 apply**. Not applicable is different from unverified: no
  run can ever verify those.
- **Four merged modules.** 20.0 ships `stock_picking_batch` inside `stock`,
  `website_sale_wishlist` and `website_sale_comparison` inside `website_sale`, and `hr_homeworking`
  inside `hr`. The provider-fit check counts the old name as installed when the new one is
  (`SeriesModule`), and so do the runners.

**Runner fixtures on 20.0.** The action fixtures ask the series (`Fixtures.v20`). On 20.0 they use:
- a stock move's `uom_id`;
- an untracked product with tracking `False`, not `'none'`;
- an active payment provider, not one by `state`;
- `active` instead of a maintenance request's `archive`;
- `hr.work.entry.type` for a leave;
- no repair start step.

Twin vehicles now get distinct plates.

**Curated money flows on 20.0.** With `--only`, 16 of 18 agree:
- sales and purchase confirm, cancel and draft;
- invoice post and cancel;
- payment register;
- transfer reserve, validate and cancel;
- the bill and invoice creation;
- opportunity win and lose.

**Result:** the 20.0 manifest holds **1170 records, 90.3% of the 1296 applicable operations**.
- Promoted from `*-20.0-lab20demo-*-20261013.json`: 89 new records.
- Still unverified on 20.0:
  - business rules a generic record does not meet, as on 19.0;
  - eight buttons the 20.0 forms do not show;
  - payment token operations, which the promotion's guarded-word rule holds back on both series;
  - four describes whose rebuilt or unchecked fields the oracle cannot compare (`company_type`,
    `rounding`, `tz`).

**Governed writes (DEC-2026-0160).** The governed product now executes the surface's verified low-
and medium-risk creates, updates, actions and dialogs, under one write key per domain. That is 593
operations. `REVIEWED_HIGH_RISK` lists 17 more high-risk operations: posting and reopening the
books, money, rates, company settings and deletes.

## Not yet done

- **Actions and wizards still UNVERIFIED on 19.0:** 39 actions and 7 wizards.
  - Some have no twins in the lab and no fixture yet. Examples: payment capture and void, which
    need a provider's authorized transactions; POS session closing; overtime approval.
  - Some were refused on both paths, or changed nothing, on the data the lab has.
- **Writes still UNVERIFIED on 19.0:** 84 creates/updates. Most failed on
  business rules a generic smallest record does not meet: duplicates of an existing membership or
  rule, a tax group of another country, a journal without a suspense account, an open POS session.
  Some need accounting rights the demo admin does not hold ("Show Full Accounting Features").
- **Reads still UNVERIFIED on 19.0:** 4: the package history the lab never wrote, and the token reads
  the manifest cannot hold.
- **SInvoice issuing and payment status:** UNVERIFIED until a Viettel test account exists and the
  owner decides to run them.
- **20.0 beyond renames:**
  - Renames whose values changed are not translated (`billable_type`, `account_type`).
  - Neither are fields 20.0 made required (`document_tax_mode`).
  - 20.0-only models and buttons are not in the catalog, for example SInvoice symbol sync and
    `l10n_vn_edi_viettel_stock`.
- **Dialogs not answered yet** are reported, not answered:
  - `confirm.stock.sms` and `pos.close.session.wizard`.
  - The partial capture `payment.capture.wizard`.
  - `purchase.requisition.alternative.warning`. Its module is not installed in the labs.
  - Dialogs of modules not installed in the labs, which the scan has not read.

## Regenerate

```sh
ODOO_INVENTORY_PASSWORD=... scripts/dev/odoo-surface-inventory.py --endpoint http://127.0.0.1:58719 \
    --database lab19 --login admin --output inventory-19.0.json
ODOO_ORACLE_PASSWORD=... scripts/dev/odoo-dialog-scan.py --container odoo19-research-web \
    --endpoint http://127.0.0.1:58718 --database lab19demo --login admin \
    --inventory inventory-19.0.json --evidence docs/architecture/provider-research/odoo-surface/dialogs-19.0.json
scripts/dev/odoo-catalog-surface.py --inventory inventory-19.0.json \
    --inventory-next inventory-20.0.json --dialogs docs/architecture/provider-research/odoo-surface/dialogs-19.0.json \
    --report surface-report-19.0.json
gofmt -w packages/odoo/odoo19_surface_catalog.gen.go
```

A dialog the scan finds behind a METHOD_CALL stops the generator until it is reviewed into
`packages/odoo/provider_dialog.go`.

Then update `AcceptedSurfaceSize` in `packages/cli/internal/standalone/standalone_test.go` on purpose.

The 20.0 renames come from the same two inventories:

```sh
scripts/dev/odoo-dialect-renames.py --inventory inventory-19.0.json --inventory-next inventory-20.0.json \
    --evidence docs/architecture/provider-research/odoo-surface/dialect-renames-20.0.json
gofmt -w packages/odoo/odoo20_dialect_renames.gen.go
```

A changed table fails `TestOdoo20ManifestWasVerifiedUnderTheCurrentRenames`. To clear it, run the
20.0 conformance with `--readiness ANY --renames …/dialect-renames-20.0.json`, rebuild the 20.0
manifest, then update `odoo20RenamesVerified`.

Collect evidence and promote it as a reviewed change. The action runner takes the same flags plus
the inventory (`--inventory inventory-19.0.json`); `--only` limits a run, or a promotion, to some
operations:

```sh
ODOO_ORACLE_PASSWORD=... scripts/dev/odoo-read-conformance.py --odoo-bin ./odoo --profile lab19demo \
    --endpoint http://127.0.0.1:58718 --database lab19demo --login admin --series 19.0 \
    --image-digest sha256:... --evidence read-run.json      # likewise odoo-write-conformance.py
scripts/dev/odoo-promote-conformance.py --evidence docs/.../evidence/read-run.json \
    --manifest packages/odoo/odoo19_conformance_manifest.json
```

After a regeneration, an operation whose definition changed is UNVERIFIED at runtime, and
`TestSurfaceEvidenceIsBoundToTheDefinitionItVerified` fails until its record is dropped. Drop it, run
the operation again, and promote the new result. Then update `AcceptedSurfaceCurrent` on purpose.
