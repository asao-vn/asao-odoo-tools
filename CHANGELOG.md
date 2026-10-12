# Changelog

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
