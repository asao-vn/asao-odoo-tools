# odoo-cli

A command-line client for **Odoo Community**. It is built on a catalog of business operations, each
checked against what Odoo itself does on the stock image. Every command answers in one JSON envelope:
the data, or an error with a code, the reason and what to do next. Scripts, notebooks and AI agents
can drive Odoo through it as dependably as a person does through the web client.

> Odoo is a registered trademark of Odoo S.A. This project is not affiliated with or endorsed by
> Odoo S.A.

## What it covers

1139 operations over the business apps of Odoo Community 19.0. 981 of them are verified on the stock
image.

| Kind | Operations | Verified |
|---|---:|---:|
| Read, list, group and paginate | 480 | 476 |
| Create and update | 458 | 371 |
| Business actions (form and list buttons) | 159 | 99 |
| Dialogs (wizards) | 26 | 19 |
| Print a document's PDF | 7 | 7 |
| Guarded deletes | 8 | 8 |
| Install a module | 1 | 1 |

- **Apps:**
  - Contacts, CRM, Sales, Invoicing and accounting, Purchase, Inventory, Manufacturing and Project.
  - Employees, Skills, Time Off, Attendances, Expenses and Recruitment.
  - Point of Sale with Restaurant, Calendar, Website, Discuss and To-Do.
  - Fleet, Maintenance, Repairs, Events and Surveys.
  - Purchase agreements, batch transfers, loyalty, expiry dates, shipping, dropshipping,
    subcontracting, landed costs and margins.
- **Vietnam:** e-invoices through Viettel SInvoice (`l10n_vn_edi_viettel`).
- **Business as code:** `odoo-cli spec plan|apply` brings an Odoo to a YAML description of a company:
  settings, products, website pages and more. The same spec rebuilds the same company on a blank
  database.
- **Prints:** the PDFs equal the browser's own download, page for page and word for word.

**How "verified" is earned.** Each operation runs on a lab with the stock Odoo image. Its result is
then compared with what Odoo itself reads, writes or prints. The tooling is in `conformance/` and
the evidence in `docs/odoo-surface/evidence/`.

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

## Before it acts

Before running an operation, `odoo-cli` checks the Odoo it reaches.

| It finds | It does |
|---|---|
| The endpoint does not answer as Odoo | Refuses (`AERP_PROVIDER_NOT_ODOO`). Nothing is sent, not even the credential |
| A series it does not support | Refuses, and says why: older than 19.0, newer than it knows, or an intermediate Odoo Online release (`saas~`) |
| 20.0 | Runs only with `--allow-unverified`. A translation layer maps 19.0's names onto 20.0's; 759 of the 1139 operations are verified on 20.0 so far |
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

- **Odoo Enterprise and Odoo Online** are not verified. Odoo Online allows the external API on its
  Custom plan only.
- **Business actions left out on purpose:**
  - Sending a document by email (a quotation, an RFQ, an invoice): odoo-cli prints the PDF instead.
  - Creating a user or a company from another record.
  - Paid IAP services (lead generation, enrichment).
- **Apps not covered yet:** Email and SMS Marketing, eLearning, eCommerce, Live Chat and Lunch.
- **Not verified yet:** 156 operations, most of them writes and actions whose records the lab's data
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

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Tiếng Việt

`odoo-cli` là công cụ dòng lệnh cho **Odoo Community**. Nó có 1139 thao tác nghiệp vụ, trong đó 981 thao
tác đã được kiểm chứng trên bản Odoo 19.0 chuẩn: đọc, ghi, bấm nút nghiệp vụ, hộp thoại, in PDF và xoá
có kiểm soát. Mọi lệnh trả về JSON, dùng được cho script, notebook và AI agent.

Trước mỗi thao tác, CLI kiểm tra Odoo mà nó kết nối:
- có phải Odoo không, và phiên bản nào;
- thiếu module gì;
- có bị tuỳ biến không.

**ASAO chỉ kiểm chứng trên bản Odoo chuẩn.** Với Odoo đã tuỳ biến, ASAO không thể kiểm chứng và không
bảo đảm thao tác chạy đúng; chạy trên Odoo đó là do người dùng tự chịu rủi ro. Chi tiết:
[docs/vi/pham-vi-kiem-chung-va-bao-dam.md](docs/vi/pham-vi-kiem-chung-va-bao-dam.md).
