# Changelog

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
