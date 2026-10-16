# odoo-cli

[![conformance](https://github.com/asao-vn/asao-odoo-tools/actions/workflows/conformance.yml/badge.svg)](https://github.com/asao-vn/asao-odoo-tools/actions/workflows/conformance.yml)

A command-line client for **Odoo Community**. It is built on a catalog of business operations, each
checked against what Odoo itself does on the stock image. Every command answers in one JSON envelope:
the data, or an error with a code, the reason and what to do next. Scripts, notebooks and AI agents
can drive Odoo through it as dependably as a person does through the web client.

> Odoo is a registered trademark of Odoo S.A. This project is not affiliated with or endorsed by
> Odoo S.A.

## What it covers

1368 operations over the business apps of Odoo Community 19.0. 1301 of them are verified on the stock
image. On Odoo Community 20.0, a candidate series (run with `--allow-unverified` until it is
supported), 1187 of the 1306 operations 20.0 can run are verified; the other 62 need models, dialogs
or buttons 20.0 removed, and odoo-cli refuses them with the reason.

| Kind | Operations | Verified |
|---|---:|---:|
| Read, list, group and paginate | 591 | 589 |
| Create and update | 557 | 543 |
| Business actions (form and list buttons) | 177 | 132 |
| Dialogs (wizards) | 27 | 21 |
| Print a document's PDF | 7 | 7 |
| Guarded deletes | 8 | 8 |
| Install a module | 1 | 1 |

- **Apps:**
  - Contacts, CRM, Sales, Invoicing and accounting, Purchase, Inventory, Manufacturing and Project
    (recurring tasks included).
  - Employees, Skills, Time Off, Attendances, Expenses and Recruitment.
  - Point of Sale with Restaurant, Calendar, Website, Discuss and To-Do.
  - Fleet, Maintenance, Repairs, Events and Surveys.
  - Purchase agreements, batch transfers, loyalty, expiry dates, shipping, dropshipping,
    subcontracting, landed costs and margins.
  - Email and SMS Marketing with marketing cards, Live Chat with its chatbots, Lunch, eLearning,
    eCommerce (public categories, ribbons, wishlists, product feeds, checkout steps) and Data
    Cleaning.
- **Vietnam:** e-invoices through Viettel SInvoice (`l10n_vn_edi_viettel`).
- **Business as code:** `odoo-cli spec plan|apply` brings an Odoo to a YAML description of a company:
  settings, products, website pages and more. The same spec rebuilds the same company on a blank
  database. A team shares a backend on S3, R2, MinIO or a folder: one apply at a time, plans saved for
  review, every apply recorded, the spec last applied to pull back.
- **Routines:** `odoo-cli routines` runs the checks a business makes every day, week or month (sales
  yesterday, overdue receivables, late transfers, last month's books review), keeps what Odoo answered
  and hands it to the agent you chose, on a timer.
- **Change history:** who changed which field of a record, when, and from what to what
  (`structure.field_change.list`, `structure.change_note.list`), for the fields a review needs: payee
  bank accounts, partners' tax numbers, document states and dates, lock dates, prices. Odoo shows it
  to administrators only; on 20.0 it needs the `mail_tracking` module.
- **Prints:** the PDFs equal the browser's own download, page for page and word for word.

**How "verified" is earned.** Each operation runs on a lab with the stock Odoo image. Its result is
then compared with what Odoo itself reads, writes or prints. The tooling is in `conformance/` and
the evidence in `docs/odoo-surface/evidence/`.

**Odoo Online and Enterprise.** odoo-cli connects to them through the JSON-2 API, and runs their reads
with a note that they are unverified. [docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md)
says what was tested and what it does not promise.

**Check it yourself.** The [conformance workflow](.github/workflows/conformance.yml) does it in the
open, every week and on every release:
- It builds Odoo 19.0 and 20.0 from the public images pinned by digest in
  `conformance/odoo-conformance-labs.json`, with demo data and every module an operation needs.
- It runs every operation through odoo-cli.
- It checks each result against Odoo through an independent JSON-RPC reader.

The run fails when an operation the manifest calls verified now disagrees with Odoo. Its evidence is
attached to the run and to the release. **[docs/conformance.md](docs/conformance.md)** explains:
- what "agree" means for a read, a write, a button, a dialog and a print;
- how to read the report;
- what the check does not prove (your own Odoo's customisations, Enterprise, Odoo Online).

The same script runs on any machine with Docker or Podman, in about an hour per series:

```sh
conformance/odoo-conformance-lab.sh --series 20.0 --out /tmp/conformance-20
cat /tmp/conformance-20/summary.md
```

Every operation carries its readiness: `CURRENT_VERIFIED`, `UNVERIFIED` or `HISTORICAL_ONLY`. An
`UNVERIFIED` operation runs only with `--allow-unverified`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/asao-vn/asao-odoo-tools/main/scripts/install.sh | sh
```

The installer downloads the release binary for your machine and checks it against the release's
checksums. You can also download `odoo-cli_<os>_<arch>` from
[Releases](https://github.com/asao-vn/asao-odoo-tools/releases), for Linux, macOS or Windows on
amd64 or arm64. To build from source, run `make build` (Go 1.26.6 or newer).

## Quick start

```sh
# A profile is configuration only: endpoint, database, login and a credential reference.
odoo-cli profile set --name my-odoo --endpoint https://erp.example.com --database erp --login admin@example.com

# The password (or a JSON-2 API key) never goes into the profile: give it through the environment,
# the OS keyring (`odoo-cli credential set --ref my-odoo --login ... --password-stdin`) or a file.
export AERP_CREDENTIAL_MY_ODOO='...'

# What this Odoo is, and which operations it can run.
odoo-cli doctor --profile my-odoo --probe --output json

odoo-cli catalog list --domain sales --output json
odoo-cli execute --profile my-odoo --operation sales.quotation.list --limit 5 --output json
odoo-cli execute --profile my-odoo --operation finance.journal_entry.list \
  --group-by journal_id --aggregate amount_total:sum --output json
odoo-cli execute --profile my-odoo --operation identity.contact.create --set name="An Phat Co." --output json
odoo-cli execute --profile my-odoo --operation finance.invoice.print --provider-target-id 42 --save-file invoice.pdf
```

- **An Odoo on your own network:** `odoo-cli` refuses loopback, private and link-local addresses by
  default. For an Odoo at `localhost` or `192.168.x.x`, set `ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS=true`.
- **Odoo's JSON-2 API (19.0):** use `--protocol json2` and an API key as the credential. To print
  PDFs over JSON-2, also give the web password in `AERP_CREDENTIAL_<REF>_WEB`, because Odoo serves
  reports only to a logged-in web session.
- **Interactive help:** `odoo-cli --help`, and `odoo-cli <command> --help --output json` for one
  command's contract.

## AI agents (MCP)

`odoo-cli mcp serve` offers the catalog to an AI agent over the Model Context Protocol, on Community
as on Enterprise. Each tool is one odoo-cli command, run against the profile you start the server
with, with the same checks as a typed command.

```sh
claude mcp add odoo -- odoo-cli mcp serve --profile my-odoo                 # reads only
claude mcp add odoo -- odoo-cli mcp serve --profile my-odoo --allow-writes  # and writes
odoo-cli mcp tools --allow-writes --output json                             # what an agent would get
```

- **Reads by default:** 615 tools:
  - the 598 verified reads;
  - catalog discovery, the pre-flight check, the review (`review run`, `review checks`), the
    controls (`controls list`, `status`, `plan`), the routines (`routines templates`, `list`,
    `status`) and `report draft`;
  - four generic reads (`explore models`, `fields`, `search`, `group`) that reach any model, an
    Enterprise app's or a customisation's included, and never a credential.
- **With `--allow-writes`:** 1384 tools. The 58 high-risk and destructive operations run only after
  you confirm each call in the agent host.
- **Hosts with a short tool list:** above 60 tools the server offers four catalog tools instead:
  `odoo_find`, `odoo_describe`, `odoo_read` and `odoo_write`. Use `--mode tools` to get one tool per
  operation.
- **What the agent cannot choose:** the profile, accepting unverified results, writing a file on your
  machine, or the provider context. Installing a module is never offered.

The server acts with the full authority of the profile's Odoo credential. It bounds what is
*offered*; it is not a sandbox. An agent that can run commands can run odoo-cli directly. See
[docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md) for how it compares with
Odoo's own MCP server.

### When odoo-cli gets it wrong: `odoo-cli report draft`

An agent is told, in the server's instructions and in every internal error, to offer you a report
when odoo-cli itself fails it: an internal error, a result that contradicts the command's
description, misleading help, or a gap it had to work around. It runs `odoo-cli report draft`, which
builds the report **on your machine and sends nothing**. Endpoints, databases, logins and profile
names from your profile file, URLs, e-mail addresses, IP addresses, phone-like numbers, long tokens
and `password=`-style values are scrubbed first, and command flags and values are dropped. The agent
shows you the draft and asks. If you agree, it gives you a link that opens a prefilled issue in this
repository, and GitHub shows you the report again before you press Submit. The fingerprint in the
title finds earlier reports of the same fault, so you can add to one instead.

```sh
odoo-cli report draft --kind WORKAROUND_USED --command "inventory transfer list" \
  --summary "no scheduled_date in the list" --workaround "read stock.picking with explore" --output json
```

## Review the books: `odoo-cli review`

Twenty fixed checks find mistakes, duplicates and signs of fraud in a period of your Odoo's books,
and the period's figures month by month:

```sh
odoo-cli review run --profile my-odoo --from 2026-07-01 --to 2026-09-30 --markdown-file q3.md --output json
odoo-cli review checks --output json      # each check: question, formula, thresholds, how to read it
```

- **What the checks look for:**
  - bills, customer invoices or payments entered twice, duplicate partners;
  - a supplier paid into an employee's account, a new supplier paid fast;
  - orders split under the approval threshold, a payment cut into pieces under it;
  - sales below cost, a tax rate unlike the product's;
  - refunds after payment;
  - documents dated back into a month that had ended, revenue booked at a month's end and credited
    early in the next;
  - in Vietnam, purchases of 5 million đồng or more paid in cash, as one bill or as several bills of
    one seller on one day: they lose the input VAT deduction (Nghị định 181/2025/NĐ-CP, Điều 26);
  - expenses claimed twice, on leave or in round amounts;
  - valuable scrap;
  - overdue receivables.
- **What each finding carries:** its formula, its thresholds (`--param O01.window_days=10`) and the
  records that are its evidence.
- **How to read a finding:** it says *cần xem lại*, "look at again", and is never a verdict.
- **A check that cannot read** (a module not installed, a model the series removed, a field the read
  does not return) is listed as not run, with the reason, never as clean.
- **Proof:** the checks were proven on a simulated business with 24 planted cases, all found, and 11
  look-alikes, none flagged. Agents get them as MCP tools, and the reading notes come with them.

## Turn on Odoo's own controls: `odoo-cli controls`

The review finds problems after they happened. Most of them have a switch in Odoo that would have
stopped them. `odoo-cli controls` says which, reads whether each is on, and turns the reversible ones
on through verified operations. Nothing is added to Odoo.

```sh
odoo-cli controls list --output json                       # what each does, where Odoo enforces it, whether it can be undone
odoo-cli controls status --profile my-odoo --output json   # ON, OFF, PARTIAL or NOT_AVAILABLE
odoo-cli controls plan --profile my-odoo --enable K01,K04 \
    --param K01.amount=20000000 --param K04.date=2026-09-30 --output json
odoo-cli controls apply --profile my-odoo --enable K01,K04 \
    --param K01.amount=20000000 --param K04.date=2026-09-30 --plan-hash sha256:... --output json
```

| Control | What Odoo does once it is on | Where Odoo enforces it |
|---|---|---|
| K01 Purchase approval from an amount | an order at or over the amount waits for a Purchase Administrator | server |
| K02 Lock approved purchase orders | read-only in the web client; cannot be cancelled while locked | web client |
| K03 Lock confirmed sales orders | the same, for sales orders | web client |
| K04 Lock the books up to a date | entries of the locked period cannot change; a late document is posted after the lock date. Community has no screen for lock dates | server |
| K05 Restrictive audit trail | posted entries, their attachments and their change log cannot be deleted | server |
| K06 Hash posted entries | posted entries can only be reversed. **odoo-cli never turns it on**: Odoo never lets it be undone, so `controls plan` says how to do it in Odoo | server |
| K07 Customer credit limit | a warning on quotations and draft invoices | warning only |
| K08 Two-step sign-in for employees | a code by email at sign-in; Odoo then refuses employees' passwords over the API, so integrations need API keys | server |
| K09 Alert: a bank account added or changed | an activity for the person you name, on that account | notify |
| K10 Alert: a posted entry reset to draft | an activity on that entry; posting it again adds none | notify |
| K11 Alert: a trading partner's tax number or name changed | an activity on that partner; a partner that never traded is not reported | notify |

- **Odoo's limits are said plainly:** "web client" means a caller of the API is not stopped, and K07
  only warns.
- **Alerts (K09-K11)** are Odoo automation rules from the `base_automation` module, which you install.
  - Their only action schedules an activity for the person you name: `--param K09.user="Ngọc Lan"`, or
    their id.
  - odoo-cli's alert operations reach only the rules odoo-cli made.
  - They never offer an action that runs code, writes records, sends a message or calls a webhook.
  - There is no alert for a changed lock date: Odoo cannot schedule an activity on a company. The
    change history shows it.
- **Safe by construction:**
  - `apply` only turns controls on, and refuses a plan that changed since you reviewed it
    (`--plan-hash`);
  - it never moves a lock date back, never sets the hard lock date, and never weakens a stronger
    setting already on;
  - an agent can list, read and plan; applying stays with you.
- **The review points at them:** a finding of `O16` or `O26` names the control that would have
  prevented it (`review checks` lists them as `preventedBy`).
- **Proof:** on Odoo 19.0 and 20.0 labs, each control was turned on through odoo-cli, and what Odoo then
  does was exercised: an order over the amount waited for approval, an entry of a locked period could
  not be reset to draft, a posted bill could not be deleted, a password was refused over RPC. Then
  everything was put back (`conformance/odoo-controls-conformance.py`). Saving the Settings page is now
  verified setting by setting on both series.

## Routines: `odoo-cli routines`

The questions a business asks its Odoo every morning, every Monday or on the first of the month, run
on a timer and land in a folder an agent reads.

```sh
odoo-cli routines templates --output json                       # ten starter routines: what each asks, what Odoo returns
odoo-cli routines init --profile my-odoo --agent claude         # writes routines.toml, yours to change
odoo-cli routines run --routine cong-no-qua-han                 # one now
odoo-cli routines install                                       # "routines due" every 15 minutes (systemd, launchd, Task Scheduler or cron)
odoo-cli routines status --output json                          # each routine's latest run
```

- **What a run leaves:** each run is kept in `~/asao-routines/<routine>/<day>/`:
  - `result.json`: exactly what Odoo answered;
  - `summary.md`: counts and totals, computed and never written by a model;
  - `analysis.md`: what the agent made of it. The agent can be Claude Code, Codex, Gemini CLI, agy, any
    command, or none.
- **For an interactive agent:** `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` in that folder tell it how to
  read the runs.
- **Reads stay reads:**
  - a read or group step can only run a read operation;
  - a routine changes Odoo only through steps that say so (`writes = true`), and never installs a
    module;
  - the agent only reads.
- **A missed slot:**
  - a laptop that was off at 07:30 runs the daily routine when it wakes;
  - a run that could not reach Odoo leaves the slot open for the next check.
- **Same file as the SDK:** the format is the one the `asao-erp` SDK's routines use (TOML, or YAML and
  JSON). Credentials stay in the keyring or the credential file; the timer keeps only paths.

## A team on one Odoo: `odoo-cli spec --backend`

```sh
odoo-cli spec plan  --profile company --file company.yaml --backend s3://my-bucket/odoo/company
odoo-cli spec apply --profile company --file company.yaml --backend s3://my-bucket/odoo/company --plan-hash sha256:...
odoo-cli spec state --backend s3://my-bucket/odoo/company
```

- **The backend holds** the lock (one apply at a time), the plans saved for review, a record of every
  apply, and the spec last applied in full, which a teammate pulls back with `spec pull`.
- **Where it lives:** an S3 bucket, Cloudflare R2, MinIO, Backblaze B2 or a shared folder. odoo-cli
  signs S3 requests itself.
- **No state file:** every plan reads Odoo itself. With a backend, apply needs the reviewed plan's hash.
- **Who approves:** the backend records who as a label. For an approval by a checked identity, use your
  git host's reviews or ASAO; [docs/team-spec.md](docs/team-spec.md) has both, with GitHub Actions
  workflows.

## Any model: `odoo-cli explore`

The catalog covers the business apps of Odoo Community. To read anything else, such as an Enterprise
app, a third-party module or a field added in Studio, use the generic reads. They are what Odoo 20
Enterprise's own MCP server offers, and they also run on Community:

```sh
odoo-cli explore models --profile my-odoo --search planning --output json
odoo-cli explore fields --profile my-odoo --model planning.slot --output json
odoo-cli explore search --profile my-odoo --model planning.slot --filter '[["state","=","published"]]' --limit 5 --output json
odoo-cli explore group  --profile my-odoo --model helpdesk.ticket --group-by stage_id --output json
```

They read and never write. Their answers are Odoo's own and are not checked by conformance, so each
one says `"generic": true`. They never show:
- models that hold credentials (system parameters, mail servers, payment providers, API keys,
  certificates, bank links);
- fields whose names mark a secret, whether as a value, a filter or a group;
- binary fields.

## Before it acts

Before running an operation, `odoo-cli` checks the Odoo it reaches.

| It finds | It does |
|---|---|
| The endpoint does not answer as Odoo | Refuses (`AERP_PROVIDER_NOT_ODOO`). Nothing is sent, not even the credential |
| A series it does not support | Refuses, and says why: older than 19.0, newer than it knows, or an intermediate Odoo Online release (`saas~`) |
| 20.0 | Runs only with `--allow-unverified`. A translation layer maps 19.0's names onto 20.0's; 1170 of the 1291 operations 20.0 can run are verified (62 cannot exist there: their models, dialogs or buttons are gone) |
| The operation's modules are not installed | Refuses before sending (`AERP_MODULE_NOT_INSTALLED`) |
| A write where this Odoo is customised: a model that Enterprise or third-party modules extend, an automation watching it, or a required field added by hand | Holds it back (`AERP_ENVIRONMENT_UNVERIFIED`) until you pass `--allow-unverified` |
| Reads on a customised Odoo | Runs them, with a note |

**Customised Odoo is not guaranteed.** The operations are verified on the stock Odoo image of each
series only. Some Odoo instances are changed beyond that image, by Enterprise, third-party or
in-house modules, Studio, automations or fields added by hand. On such an Odoo, operations cannot be
verified, and nothing guarantees that they work as described or without error. An operation you
accept there runs at your own risk. `odoo-cli doctor --probe` shows what it found. See
[docs/provider-fit.md](docs/provider-fit.md).

Other guarantees:
- **Credentials:** no command prints credential material, and it is redacted from errors and data.
- **Deletes:** an operation that would affect dependent records needs `--confirm-impact`.
- **Errors:** the envelope carries a stable code, the reason, a remediation, and the structured
  context a caller needs to correct itself. Exit codes separate configuration, denial, conflict and
  provider failures.
- **Trust boundary:** `odoo-cli` acts with the full authority of the Odoo credential in the profile.
  It is for a single trusted operator: a developer, a local automation, or an agent running under
  your own identity. Run `odoo-cli trust --output json` for the full statement.

## Python

The [`asao-erp`](https://github.com/asao-vn/asao-erp-sdk) package drives `odoo-cli` from Python. It
covers reads, grouping, paging, prints and pandas frames, which suits notebooks and staff statistics.

```sh
pip install asao-erp
```

## Not yet

- **Odoo Enterprise and Odoo Online** are not verified. On 2026-10-14 odoo-cli reached an Enterprise
  20.0 trial on Odoo Online: none of its 580 reads disagreed with Odoo, 415 of 553 writes and 75 of 192
  buttons and dialogs agreed, and one button behaved differently on Enterprise. Odoo Online allows the external API on its Custom plan only. See
  [docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md), which also compares
  odoo-cli with Odoo's own MCP server.
- **Business actions left out on purpose:**
  - Sending email or SMS: a quotation, an RFQ or an invoice (odoo-cli prints the PDF instead), a
    mailing (launching, scheduling, testing it), invitations to a course or an event, coupons,
    abandoned-cart reminders, survey reminders, lunch notifications.
  - Creating a user or a company from another record.
  - Paid IAP services (lead generation, enrichment, SMS credits).
- **Not verified yet:** 67 operations, most of them writes and actions whose records the lab's data
  cannot supply, or whose business rules refuse generic values.
- **Self-update:** `odoo-cli update` needs a signed release channel that is not published yet. To
  update, run the installer again.

## This repository

This repository is exported from the ASAO monorepo; `SOURCE.md` names the commit.
- The root is the part of the command-line module that `odoo-cli` links.
- `third_party/` holds the Odoo core (`packages/odoo`, with its tests) and the endpoint policy.
- `conformance/` holds the tools that verify the catalog against a live Odoo.
- `docs/` holds the catalog's research and evidence.

Issues and pull requests are welcome. An accepted change is applied in the monorepo and arrives with
the next export.

```sh
make build   # bin/odoo-cli
make test    # the command line and the Odoo core
```

## Support the project

odoo-cli is free and built by one person. If it saves you time and you would like to chip in, any
amount helps:

- **From anywhere, by card or PayPal:** [ko-fi.com/nguyenhuuhoang](https://ko-fi.com/nguyenhuuhoang)

| PayPal | Vietcombank (VietQR, any Vietnamese banking app) |
|:---:|:---:|
| <img src="docs/support/paypal-qr.svg" alt="PayPal QR code for paypal.me/AldenNguyen2345" width="160" height="160"> | <img src="docs/support/vcb-qr.svg" alt="VietQR code for Vietcombank account 1031712729" width="160" height="160"> |
| [paypal.me/AldenNguyen2345](https://paypal.me/AldenNguyen2345) | NGUYEN HUU HOANG · 1031712729 |

A star, an issue or a note about what you use it for helps just as much. Support buys no priority and
no warranty; the license and its terms stay the same.

## License, owner, privacy and terms

- **License:** Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE). The software is
  provided "as is", without warranty (sections 7 and 8 of the license).
- **Owner:** Copyright 2026 Nguyễn Hữu Hoàng, who publishes it under the brand ASAO.
  Contact: nguyenhuuhoang711@gmail.com.
- **Privacy:** odoo-cli collects nothing. It has no telemetry and no account, and it connects only to
  the Odoo you configure; `odoo-cli update`, when you run it, also reaches the release channel you
  name. Your Odoo credential stays on your machine: in the OS keyring, a file you name, or an
  environment variable. What it reads from your Odoo goes to your terminal, your files or the agent
  you connect, never to ASAO.
- **Terms:** using this software on its own is governed by its license only. ASAO's hosted service
  (asao.vn) has its own [Terms of Service](https://asao.vn/terms) and [Privacy Policy](https://asao.vn/privacy), which apply
  when you use that service, including through the `asao` CLI.

## Tiếng Việt

`odoo-cli` là công cụ dòng lệnh cho **Odoo Community**. Nó có 1368 thao tác nghiệp vụ, trong đó 1301 thao
tác đã được kiểm chứng trên bản Odoo 19.0 chuẩn: đọc, ghi, bấm nút nghiệp vụ, hộp thoại, in PDF và xoá
có kiểm soát. Mọi lệnh trả về JSON, dùng được cho script, notebook và AI agent. Trên Odoo 20.0, 1187 trong
1306 thao tác áp dụng được đã được kiểm chứng.

**Tự kiểm lại được.** Workflow `conformance` chạy công khai mỗi tuần và mỗi lần phát hành:
- dựng Odoo 19.0 và 20.0 từ image chính hãng (ghim theo digest);
- chạy mọi thao tác qua odoo-cli;
- đối chiếu từng kết quả với chính Odoo qua một bộ đọc JSON-RPC độc lập.

Workflow báo lỗi khi một thao tác đã kiểm chứng nay lệch với Odoo. Trên máy có Docker hoặc Podman, bạn
chạy `conformance/odoo-conformance-lab.sh --series 20.0 --out /tmp/conformance-20`.

[docs/conformance.md](docs/conformance.md) (có phần tiếng Việt) giải thích:
- "khớp" nghĩa là gì;
- cách đọc báo cáo;
- những gì việc kiểm chứng này **không** chứng minh.

Trước mỗi thao tác, CLI kiểm tra Odoo mà nó kết nối:
- có phải Odoo không, và phiên bản nào;
- thiếu module gì;
- có bị tuỳ biến không.

**AI agent qua MCP:** `odoo-cli mcp serve --profile my-odoo` đưa catalog cho agent qua MCP, chạy được cả trên
Community lẫn Enterprise.
- **Mặc định chỉ đọc:** 615 công cụ, gồm 4 công cụ `explore` đọc được mọi model (cả app Enterprise và trường tự
  thêm), không bao giờ đọc mật khẩu hay khoá bí mật.
- **Thêm `--allow-writes`:** agent được ghi. 58 thao tác rủi ro cao hoặc xoá chỉ chạy sau khi bạn xác nhận
  từng lần.
- **Agent không tự chọn được:** profile, việc chấp nhận kết quả chưa kiểm chứng, hay việc ghi file lên máy
  bạn.

**Chốt kiểm soát của chính Odoo:** `odoo-cli controls` cho biết công tắc nào trong Odoo chặn trước được
việc phép kiểm tìm ra: duyệt đơn mua từ một mức tiền, khoá sổ đến một ngày, không cho xoá dấu vết kế toán,
hạn mức nợ của khách, xác thực hai bước cho nhân viên... Nó đọc xem công tắc nào đang bật, và bật những cái
tắt lại được qua thao tác đã kiểm chứng, có kế hoạch để bạn xem trước.
- Odoo Community không có màn hình đặt ngày khoá sổ; odoo-cli đặt được, và không bao giờ lùi ngày khoá.
- Niêm phong bút toán bằng mã băm thì odoo-cli không bật, vì Odoo không cho tắt lại.

**Việc định kỳ:** `odoo-cli routines` chạy những câu hỏi doanh nghiệp hỏi Odoo mỗi sáng, mỗi thứ Hai hay
đầu tháng (bán hàng hôm qua, công nợ quá hạn, phiếu kho trễ, rà soát sổ sách tháng trước), theo lịch.
- Mỗi lần chạy được lưu thành thư mục: số liệu Odoo trả về, bản tóm tắt do máy tính, và phần đọc của
  agent bạn chọn (Claude Code, Codex, Gemini CLI, agy hoặc không dùng agent).
- Không cần Python. Lệnh `routines install` đặt lịch chạy tự động trên máy.

**Làm theo nhóm trên một Odoo:** `odoo-cli spec --backend s3://...`. Bucket S3, R2, MinIO hay một thư mục
dùng chung giữ khoá (mỗi lúc chỉ một lần apply), các plan đã lưu để xem lại, lịch sử mọi lần apply, và spec
đã apply gần nhất để kéo về. Không có tệp state: mỗi lần plan đều đọc thẳng Odoo. Xem
[docs/team-spec.md](docs/team-spec.md).

**Odoo Online và Enterprise:** odoo-cli kết nối được qua API JSON-2 (cần gói Custom của Odoo). Ngày 14/10/2026,
trên một bản dùng thử Enterprise 20.0: 580 thao tác đọc không lệch thao tác nào; 415/553 thao tác ghi và
75/192 nút nghiệp vụ khớp với Odoo; một nút chạy khác trên Enterprise. Chi tiết: [docs/odoo-online-and-enterprise.md](docs/odoo-online-and-enterprise.md).

**ASAO chỉ kiểm chứng trên bản Odoo chuẩn.** Với Odoo đã tuỳ biến, ASAO không thể kiểm chứng và không
bảo đảm thao tác chạy đúng; chạy trên Odoo đó là do người dùng tự chịu rủi ro. Chi tiết:
[docs/vi/pham-vi-kiem-chung-va-bao-dam.md](docs/vi/pham-vi-kiem-chung-va-bao-dam.md).

**Giấy phép, chủ sở hữu, quyền riêng tư.** odoo-cli phát hành theo giấy phép Apache 2.0, thuộc sở hữu của
cá nhân Nguyễn Hữu Hoàng (thương hiệu ASAO), và được cung cấp "nguyên trạng", không kèm bảo đảm.
odoo-cli không thu thập dữ liệu gì: không có telemetry, không cần tài khoản, chỉ kết nối tới Odoo bạn
cấu hình. Thông tin đăng nhập Odoo nằm trên máy của bạn, và dữ liệu đọc được không gửi về ASAO. Dịch vụ
ASAO (asao.vn) có [Điều khoản sử dụng](https://asao.vn/terms) và [Chính sách quyền riêng tư](https://asao.vn/privacy) riêng; chúng
chỉ áp dụng khi bạn dùng dịch vụ đó.

**Ủng hộ dự án.** odoo-cli miễn phí và do một người làm. Nếu nó giúp bạn tiết kiệm thời gian và bạn muốn
ủng hộ một chút: chuyển khoản Vietcombank **1031712729** (NGUYEN HUU HOANG), quét VietQR bằng app ngân hàng
nào cũng được, hoặc PayPal [paypal.me/AldenNguyen2345](https://paypal.me/AldenNguyen2345), hoặc thẻ quốc tế qua
[ko-fi.com/nguyenhuuhoang](https://ko-fi.com/nguyenhuuhoang). Mã QR ở mục
[Support the project](#support-the-project). Một sao trên GitHub, một issue hay một lời kể bạn dùng nó
vào việc gì cũng quý như vậy.
