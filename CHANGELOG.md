# Changelog

## 0.15.0 (unreleased)

- **New: `odoo-cli upgrade check --to 20.0`.** Before an upgrade, it says which customizations people made
  in the database will stop working on 20.0, and why:
  - what it reads: saved filters, automation rules, server actions, custom fields, email templates,
    views, record rules, access rights, export templates, default values and window actions;
  - how: it resolves every model and field they reference against a map made from the 19.0 and 20.0
    conformance labs;
  - what it says: the probable new name of a renamed field, and UNKNOWN for a module the map does not
    cover (Enterprise or third-party).
  It reads only, and never prints the code or the templates it reads. `--markdown-file` writes a report
  in Vietnamese; `upgrade maps` lists the pairs this build can check. On 20.0, record rules and access
  rights move to `ir.access`, so every custom one is reported. Customizations planted on 19.0 were
  replayed on 20.0. Before the check was tuned on them, it flagged 8 of 10 that broke, with one false
  alarm among 8 that still worked.
- MCP: `upgrade_check` and `upgrade_maps` are read tools: 617 by default, 1386 with `--allow-writes`.
- **The check is measured on real Odoo, in the open.** The `upgrade-check` workflow
  (`conformance/upgrade-check/measure.sh`, any machine with Docker or Podman) makes a fresh set of
  customizations on a throwaway 19.0, as a person would, and uses each once. It runs the check there,
  makes the same customizations again on a throwaway 20.0, and sets each prediction against what 20.0 did.

## 0.14.0 (2026-10-16)

- **Every write an agent asks for needs your confirmation.** `mcp serve --allow-writes` now asks you in
  the agent host before every write, not only before the 61 high-risk, destructive and bank-account
  operations: almost any text an agent reads in Odoo can be written outside the company, and can
  carry instructions. `--confirm-writes=false` gives back what 0.13 did, for an agent that reads no
  such text. `--confirm-writes`, added in 0.13.1, is now the default and is still accepted.
  `mcp tools --allow-writes` shows 769 CONFIRM by default.
- **Fixed: `explore` refuses `ir.access` on Odoo 20.** Odoo 20 merges `ir.model.access` and `ir.rule`
  into one model, `ir.access`, which `explore` read although it refuses the two models it replaces.
- **Conformance:** a lab no longer fails to start one time in 64. Its random database password could
  begin with `-`; the Odoo image passes the password to `wait-for-psql.py` as an argument of its own,
  which took it for an option, and the lab stopped before Odoo made the database. The password is now hex.
- **Conformance:** the read and write runners record the renames file by its name, not by its path on
  the machine that ran them. One published 20.0 evidence file carried such a path; it now carries the name.

## 0.13.1 (2026-10-16)

1368 operations, unchanged: 1301 verified on 19.0, and 1187 of the 1306 that 20.0 can run.

- **Changes to a bank account ask you first.** On `mcp serve --allow-writes`, creating, changing or
  archiving a bank account (`identity.bank_account.*`, risk LOW) ran without asking, and could also
  set `allow_out_payment`, the flag Odoo checks before it pays an account. A bank account says who the
  company pays, so an agent now changes one only after you confirm the call in the agent host: 61
  operations ask, up from 58.
- **`mcp serve --confirm-writes`:** every write asks you first, not only the high-risk ones. Use it
  when the agent reads text written outside the company (a website form, an email that became a lead
  or a task, a sign-up): such text can carry instructions, and an agent can follow them.
- The server's instructions tell the agent that text inside records is data, never an instruction,
  and to bring requests found there about bank details, contacts, prices or access to you.
- **Fixed: what a confirmation tells you.** It showed an `asao ...` command line and said ASAO would
  still check your permissions, which odoo-cli does not. It now shows the `odoo-cli ...` command line,
  and says the call runs with the full authority of the Odoo login in the server's profile.
- **Fixed: the flags that shape a read are refused on a write.** `execute` accepted `--limit`,
  `--filter`, `--order`, `--offset`, `--group-by` and `--aggregate` with an operation that changes
  Odoo, and ran the write. A caller paging through what it took for a read, such as the asao-erp SDK
  0.3.0's `read()`, could run a write by mistake. It is now refused with `AERP_READ_FLAG_ON_WRITE`
  before any profile is read.
- **Fixed: a partial apply is recorded as PARTIAL.** Before, `spec apply --partial` with a backend
  recorded the run as APPLIED, and kept the spec as the one last applied in full, although the plan's
  UNSUPPORTED or AMBIGUOUS steps were left out. The run is now PARTIAL, with how many steps were left
  out, and `spec pull` still gives the last spec applied in full.
- **Fixed: an S3 bucket whose name holds a dot** is addressed by path. AWS's certificate does not
  cover a dotted bucket name used as a host name.
- **Said plainly:** the routines timer was tried on systemd. The launchd, Task Scheduler and cron
  installers are written but not tried yet.

## 0.13.0 (2026-10-16)

1368 operations, unchanged: 1301 verified on 19.0, and 1187 of the 1306 that 20.0 can run.

- **`odoo-cli routines`: a business's daily, weekly and monthly checks, with no Python.** The routines
  the `asao-erp` SDK introduced now run in the command line itself:
  - `templates`: ten starter routines (sales yesterday, overdue receivables, money in and out, late
    transfers, late tasks, last week's and last month's books review, stalled opportunities, monthly
    revenue, recurring tasks);
  - `init`, `list`, `run`, `due` and `status`;
  - `install`: a timer on systemd, launchd, Task Scheduler or cron.

  Each run is kept in a folder: what Odoo answered, a computed summary, and the reading of the agent
  you chose (Claude Code, Codex, Gemini CLI, agy, any command, or none). The same file format as the
  SDK (TOML, and now YAML or JSON).

  What changed on the way:
  - a read or group step can only run a read operation;
  - a routine changes Odoo only through steps that say so, never by installing a module;
  - a run that reached Odoo at no step leaves the slot open for the next timer tick;
  - one `due` runs at a time.

  An agent over MCP may list the starter routines and read a file's routines and their latest runs
  (three read tools, from the file the server was started with); running routines, writing a file
  and installing a timer stay with you.
- **A team on one Odoo: `odoo-cli spec --backend`.** `spec plan` and `apply` can share a backend:
  - where: an S3 bucket (or Cloudflare R2, MinIO, Backblaze B2), signed by odoo-cli itself with no
    SDK, or a shared folder;
  - the lock: one apply at a time, and `spec unlock` for an apply that died;
  - the plans: each one is saved under its hash with the spec as written, so a colleague can review
    it (`spec pull --plan`);
  - the record: every apply is recorded as REFUSED, FAILED or APPLIED, with plan and report
    (`spec runs`);
  - the spec last applied in full, which anyone can pull back (`spec pull`);
  - `spec state` shows it all.

  With a backend, apply needs the reviewed plan's hash (or `--auto-approve`). A backend belongs to
  one Odoo, and another one is refused before it is read. There is no state file: every plan reads
  Odoo itself. "Who" is a label (user and host); the bucket's permissions decide who writes, and
  `odoo-cli trust` says so. For approval by a colleague whose identity is checked, see
  [docs/team-spec.md](docs/team-spec.md).
- **Proof:**
  - **Routines** on an Odoo 19 lab: all ten templates ran; the recurring-task routine created its
    tasks once and found them on the next run; a timer ran the due routines.
  - **The backend** against MinIO: the signature matches AWS's published example; plan, review, apply
    and pull round-trip the spec byte for byte; five concurrent applies gave one APPLIED and four
    SPEC_LOCKED.

## 0.12.0 (2026-10-16)

1368 operations: 1301 verified on 19.0, and 1187 of the 1306 that 20.0 can run.

- **Alerts: K09-K11 in `odoo-cli controls`.** Notify-only Odoo automation rules (module
  `base_automation`, which you install) whose only action schedules an activity for the person you name:
  - K09: a bank account is added, or its number, holder or trusted flag changes;
  - K10: a posted entry is reset to draft (posting it again adds nothing);
  - K11: a partner that has invoices changes its tax number or name (one that never traded is not
    reported).

  Plan and apply them like any control: `--param K09.user="Ngọc Lan"` (or the user's id) and
  `K09.lang=vi|en` for the activity's summary. A plan of an alert already on is NO_CHANGE. There is no
  alert for a changed lock date: Odoo cannot schedule an activity on a company, and the change history
  shows it.
- **Eleven operations under them, all verified:**
  - rules (`structure.alert_rule.list|describe|create|update`) held to names that start with `odoo-cli: `;
  - their actions (`structure.alert_action.*`), whose type is forced to an activity;
  - three reads that resolve what a rule names (`structure.registry_model.list`,
    `structure.registry_field.list`, `structure.activity_type.list`).

  A rule or action you made is out of their reach. An action that runs code, writes records, sends a
  message or calls a webhook is refused before Odoo is reached; so is an action without its rule, and a
  rule or action on a model that cannot carry activities (never `base`, which every model inherits).
  Proven by
  `conformance/odoo-alerts-conformance.py`, now part of the lab run; the labs install `base_automation`.
- **Fixed: an archived record of an operation's own subset can be updated.** The check that a target
  belongs to an operation's subset ran with Odoo's default active test, so an archived record (an alert
  rule switched off) could never be brought back. It now sees archived records; the subset still decides.
- **Fixed: a plan compares lists of ids as sets.** A list of two ids was read as a many2one's
  `[id, name]`, so a record with a two-record many2many was planned as a change on every run.
- **Conformance:** the action runner leaves a payment transaction's state-change stamp out of the twin
  comparison. It is a clock stamp to the second, and it failed 0.11.0's release run on 19.0.
  - The lab seeder no longer makes automation rules or server actions. Its generic rules on the model
    `base` made every model run them inside its own create, and creating a bank account with K09 on then
    failed in the rehearsal before this release.

## 0.11.0 (2026-10-16)

1357 operations: 1290 verified on 19.0, and 1176 of the 1295 that 20.0 can run.

- **`odoo-cli controls`: Odoo's own switches that stop what the review finds.** Eight controls:
  purchase approval from an amount, locked purchase and sales orders, lock dates, the restrictive
  audit trail, hashed entries, customer credit limits and two-step sign-in for employees. Each says
  what Odoo does once it is on (read in Odoo's source), where Odoo enforces it (server, web client
  only, or a warning) and whether it can be undone.
  - `controls status` reads them; `controls plan` and `controls apply --plan-hash` turn the reversible
    ones on through the spec engine and the catalog's verified operations.
  - It never moves a lock date back, never sets the hard lock date, never hashes a journal (Odoo never
    lets that be undone) and never weakens a stronger setting already on.
  - Odoo Community has no screen for lock dates: odoo-cli sets them.
  - Agents may list, read and plan; applying stays with the person. `review checks` and the markdown
    report name the control that would have prevented a finding.
  - Proven on 19.0 and 20.0 by `conformance/odoo-controls-conformance.py`: each control turned on
    through the CLI, then what Odoo does exercised through an independent oracle (an order over the
    amount waits for approval, an entry of a locked period cannot be reset to draft, a posted bill
    cannot be deleted, an employee's password is refused over RPC), then everything restored.
  - Two things the run found in Odoo itself are now in the controls' text. A document dated in a
    locked period is posted with its accounting date moved after the lock, not refused. Once two-step
    sign-in is required of employees, their passwords no longer work over RPC.
- **Saving the Settings page is verified.** `structure.settings.update` had no runner: it has no
  record and no button. `conformance/odoo-settings-conformance.py` sets every setting through the
  CLI, reads it back as the page opens it, and restores it. Verified on 19.0 and 20.0; the two
  settings 20.0 removed are left out of the read and refused on write.
  - New settings: the purchase approval amount, the restrictive audit trail, the customer credit
    limit and its default.
  - The timesheet reminders are gone: Community declares them but keeps neither.
  - Company reads and writes take the four soft lock dates. Reads also show the hard lock date and a
    journal's hash switch, which the catalog never writes.
  - The labs install `hr_timesheet`, whose settings the page offers, and run both new workflows.

- **Agents can report a fault in odoo-cli, and you decide whether it is filed.** `odoo-cli report
  draft` builds a scrubbed report on your machine and sends nothing; you file it through a prefilled
  GitHub link. Internal errors and the MCP server's instructions point agents at it.

- **Six more review checks, 20 in all.** Each was planted, with a look-alike it must leave alone, in the
  simulated business the review is proven on:
  - `O02` a customer invoiced twice for one sale (the same lines are XEM_NGAY);
  - `O07` a payment cut into pieces under the approval limit: parts of one bill, or no bill at all;
  - `O26` documents dated back into a month that had ended: customer invoices, credit notes and manual
    journal entries, with a closing allowance;
  - `O27` revenue booked in the last days of a month and credited early in the next;
  - `O28` and `O29`, Vietnam: purchases of 5 million đồng or more paid in cash, alone or as several bills of
    one seller on one day, lose their input VAT deduction (Nghị định 181/2025/NĐ-CP, Điều 26, read in the
    Official Gazette).

  A check whose read does not return a field it needs is reported as not run, with the reason, never as
  clean.
- **The change history, read.** `structure.field_change.list|describe` (old and new values) and
  `structure.change_note.list|describe` (who changed which record, when), held to the fields a review
  needs: payee bank accounts, partners' tax numbers, document states and dates, lock dates, prices.
  Odoo shows them to administrators only. On 20.0 they need the `mail_tracking` module (Discuss
  Tracking), which 20.0 split out of `mail`; the check before a call says so.
- **Payments and journal entries say more.** `finance.payment.list` returns the bank account paid into, the
  bills and invoices a payment settles, and who made it when; `finance.journal_entry.list|describe`, who made
  an entry when and whether a payment or a bank statement line made it. All verified again on 19.0 and 20.0.
- `conformance/odoo-read-conformance.py --only` runs a few reads; `odoo-conformance-lab.sh --setup-only` builds
  a lab and keeps it, without a pass.

- **Recurring tasks.** `work.task.create` and `work.task.update` take `recurring`, `repeat_every`,
  `repeat_unit` (day, week, month, year), `repeat_type` (forever, until) and `repeat_until`, with
  `due_on`, `priority` (low, medium, high, urgent), `tags` (by name or id) and `description`;
  `work.task.list` and `work.task.describe` return them. Odoo makes the next occurrence when the
  current one is closed (done or cancelled), not on a calendar date.
- **Verified as a workflow, on 19.0 and 20.0.** A new conformance pass
  (`conformance/odoo-task-recurrence-conformance.py`, part of the weekly lab run) creates a recurring
  task with every new field, reads it back through an independent JSON-RPC oracle and through the
  CLI, changes its recurrence, closes it, and checks that Odoo opened exactly one next occurrence a
  month later.

## 0.10.0 (2026-10-15)

- **Every release is checked against Odoo, automatically.** A release made by the release workflow
  raised no event for the conformance workflow, so "every release" depended on someone starting it by
  hand (0.9.0's was). The release workflow now calls it, and the evidence is attached to the release.
- **1353 operations, every one an Odoo allows.** Five writes Odoo grants to no group, administrators
  included, are no longer offered:
  - creating a payment method;
  - creating or updating live chat member history;
  - creating or updating work-center productivity loss types.

  Creating a loyalty card now takes its program, without which Odoo cannot create one, and it is
  verified: 1285 on 19.0.
- **`odoo-cli review`: review the books of a period.**
  - 14 checks over the catalog's own reads, for mistakes, duplicates and signs of fraud, plus the
    period's figures month by month.
  - Each finding carries its formula, its thresholds and its evidence.
  - Proven on a simulated business: 16 of 16 planted cases found, no decoy flagged.
  - Offered to agents as MCP tools, with each check's reading note.
- **Odoo 19.0: 1284 verified.** Ten more writes agree with Odoo:
  - pricelist items, CRM team members, accounts, payment terms, reconciliation lines, quotation
    template lines, payments, taxes and calendar recurrences;
  - the conformance runner now gives the values those models check: a recurrence rule, a term's days,
    a percentage, a bank journal, the tax group's country.
- **Odoo 19.0: 1274 verified.** Creating and updating payment tokens agreed with Odoo but could not be
  recorded: the manifest refused the word "token" anywhere, its own operation ids included. An
  operation id now passes, since it can only name a catalog operation, and the word is still refused
  everywhere else.
- **`odoo-cli explore`: read any model.**
  - Four commands: models, fields, search, group.
  - They also run as MCP tools.
  - They reach what the catalog does not: Enterprise apps, third-party modules, custom fields.
  - This is what Odoo 20 Enterprise's MCP server offers, and it runs on Community too.
  - Credentials, secret fields and binaries are never shown.
  - Answers say they are generic and unverified.
- **`spec plan`: records whose module the plan itself installs are planned after the modules.**
  Before, such a record was reported as a gap, so a spec that brought its own apps could not plan their
  records.
- **Correction to 0.9.0's README:** with `--allow-writes` the MCP server offered 1361 tools, 54 of them
  waiting for confirmation (not 1362 and 55). Installing a module was never offered.
- **Odoo Online and Enterprise: writes and buttons checked too**, on the same Enterprise 20.0 trial:
  - creates and updates: 415 of 553 agreed with Odoo;
  - buttons and dialogs: 75 of 192 agreed;
  - `finance.reconcile_model.set_manual` behaves differently on Enterprise;
  - see [docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md).

## 0.9.0 (2026-10-14)

- **Check it yourself.** A public conformance workflow runs weekly and on every release:
  - it builds Odoo 19.0 and 20.0 from the official images pinned by digest, with demo data;
  - it runs every operation through odoo-cli and checks each result against Odoo through an
    independent JSON-RPC reader;
  - it fails when a verified operation disagrees with Odoo.

  `conformance/odoo-conformance-lab.sh` does the same on any machine with Docker or Podman. See
  [docs/conformance.md](docs/conformance.md).
- **Odoo 20.0.** 1170 of the 1296 operations 20.0 can run are verified, up from 1081. The other 62
  need models, dialogs or buttons 20.0 removed, and odoo-cli now refuses them before contacting Odoo,
  with the reason. Four modules 20.0 ships inside others (batch transfers in Inventory, wishlists and
  product comparison in eCommerce, remote work in Employees) no longer make an operation look
  uninstalled.
- **`odoo-cli mcp serve`: the catalog for AI agents.** It serves the catalog over the Model Context
  Protocol, on Community as on Enterprise.
  - Reads by default.
  - Writes with `--allow-writes`. High-risk and destructive operations then wait for your
    confirmation in the agent host.
  - Above 60 tools it offers four catalog tools: `odoo_find`, `odoo_describe`, `odoo_read` and
    `odoo_write`.
  - The profile, unverified results, local files and the provider context stay yours.

  `odoo-cli mcp tools` shows what an agent would get.
- **Odoo 19.0: 1272 verified**, up from 1267: creating and updating POS sessions, updating a job's
  skills, invoicing a POS order and validating a repair order. The weekly run of 2026-10-14 found no
  regression on 19.0 or 20.0.
- **Grouped reads on Odoo 20.0.** `--group-by` and `--aggregate` (the SDK's `group()`) were refused on
  20.0 and therefore on Odoo Online. They now run there:
  - the grouping, the sums and the sort are translated like a read's;
  - the groups come back under the 19.0 field names.
- **Odoo Online and Enterprise.** Tested for the first time, on an Enterprise 20.0 trial on Odoo
  Online (2026-10-14): odoo-cli connects through the JSON-2 API with an RPC key and ran 269 of its 291
  list reads without an error. Compared one by one with Odoo, none of the 580 reads disagreed (366
  agreed; the rest found no record, a missing module or a model 20.0 removed). Writes were not run
  there. See [docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md),
  which also compares odoo-cli with Odoo's own MCP server.
- **Fields for review and analysis.** Six reads return more, when the Odoo has them:
  - invoices: the supplier's reference, the invoice a credit note reverses, who created it, when;
  - invoice lines: their taxes and document type;
  - contacts: tax code, street, city, who created it, when;
  - products: the cost;
  - employees: their own contact (which holds their bank accounts);
  - payments: whether they are reconciled, and their memo.

  They agree with Odoo on 19.0 again (conformance run of 2026-10-14).
- **Rights holder.** The copyright holder is Nguyễn Hữu Hoàng, who publishes ASAO (see NOTICE). The
  licence is unchanged: Apache-2.0.

## 0.8.0 (2026-10-13)

- Payments: with Odoo's Demo provider (`payment_demo`), capturing and voiding authorized payments are
  verified. A transaction's Capture is now its capture dialog (`finance.payment_transaction.capture`,
  with `amount_to_capture`).
- Risk reviewed: 17 operations whose name hid their risk are now high risk, so a plan asks for
  approval:
  - posting, reopening or cancelling a payment;
  - resetting a journal entry to draft;
  - validating landed costs and scraps;
  - posting expenses;
  - POS payments;
  - payment providers, transactions and currency rates;
  - company settings;
  - data-cleaning deletes.
- Buttons that can never show are left out (a transaction's Post Process), as are the Demo
  provider's outcome buttons.
- Odoo 20.0: 1081 operations verified, up from 1012; live chat conversation tags no longer exist
  there.
- 1267 of 1358 operations verified on 19.0.

## 0.7.0 (2026-10-12)

- 1263 of 1359 operations verified on 19.0, up from 1201.
- The conformance runners see more:
  - updates of numbers, booleans, dates, selections and relations;
  - creates of models with nothing required;
  - the states of a record's lines (a repair's parts, a production's components);
  - buttons that delete their record.
- New fixtures:
  - purchase approval;
  - event publishing;
  - maintenance restore;
  - data cleaning;
  - landed costs (compute, validate, cancel);
  - reservations of productions, repairs and batches.
- A shipping method's debug and production switches are left out: they need a carrier connector.
- Odoo 20.0 runs the same apps as 19.0: 1012 operations verified there, up from 717.

## 0.6.0 (2026-10-12)

- Six more Community apps:
  - Email and SMS Marketing, with marketing cards;
  - Live Chat, with its chatbots;
  - Lunch;
  - eLearning;
  - eCommerce;
  - Data Cleaning.
- Buttons that send email or SMS, buy IAP credits or only open another window stay out.
- Required rich-text fields are accepted (a lunch alert's message). A lunch alert's and a lunch
  supplier's scheduled job, and a mailing's UTM source, are no longer offered: Odoo sets them itself.
- Conformance:
  - the lab admin has full accounting features;
  - the write runner retries a create Odoo refuses as a duplicate with other related records, samples
    reference fields, and gives the values Odoo needs that a generic sample cannot;
  - zip prefixes are compared in upper case, and short codes respect a field's size;
  - lunch orders are verified on fixtures with distinct notes, so that Odoo does not merge them.
- 1361 operations, 1201 verified on 19.0. On 20.0, 717 are verified; the new apps are not verified
  there yet.

## 0.5.0 (2026-10-11)

- Odoo 20.0 conformance:
  - the 20.0 lab runs the same apps as the 19.0 one;
  - the renames table is rebuilt from both inventories;
  - every operation ran again against 20.0.
- 759 of 1139 operations are verified on 20.0. 20.0 stays a candidate series: it runs only with
  `--allow-unverified`, and each result reports 20.0's own readiness.

## 0.4.0 (2026-10-11)

- The 19 Community apps that were parked join the catalog:
  - Fleet, Maintenance, Recruitment, Surveys, Repairs, Events and Restaurant;
  - purchase agreements, batch transfers, loyalty, expiry, shipping, dropshipping, subcontracting,
    landed costs and margins.
- The operations whose forms those apps change ran their conformance again.
- Fields that Odoo sets itself on create are no longer offered as inputs: a warehouse's locations, a
  repair's locations, an agreement's reference, an expense's unit price.
- Sixteen navigation buttons, which only open another list or a URL, are left out.
- 1139 operations, 981 verified.

## 0.3.0 (2026-10-11)

- The business buttons of list views join the catalog, each verified against the web client's own
  path on fresh twins:
  - work orders: start, pause, finish;
  - transfers: unreserve;
  - reorder rules: Order Once, Automate, and clearing a quantity set by hand.
- 839 operations, 703 verified.

## 0.2.1 (2026-10-11)

- `odoo-cli version` reports the release (`releaseVersion`) next to the command contract
  (`cliVersion`).
- The release and CI workflows run on the current GitHub Actions (Node 24).

## 0.2.0 (2026-10-11)

The first release of `odoo-cli` from this repository. 0.1.0 was an earlier conformance toolkit.

- 832 operations over the business apps of Odoo Community 19.0, 696 verified on the stock image:
  reads and grouping, creates and updates, business actions, dialogs, PDF prints and guarded deletes.
- A check before every operation: whether the endpoint is Odoo, which series it runs, the installed
  modules, and the customisations where a write acts.
- `spec plan|apply`: a company's Odoo described as YAML, and replayed onto a blank database.
- An employee's own Odoo login can run every read.
- JSON-2 (API keys) as well as JSON-RPC.
