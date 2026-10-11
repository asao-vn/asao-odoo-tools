# Changelog

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
