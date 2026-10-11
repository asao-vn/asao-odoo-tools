# What odoo-cli checks before it acts

The catalog's evidence proves each operation on one environment: the stock Community image of an
Odoo series. An Odoo you reach may not be that environment, even one you host yourself. So before an
operation runs, `odoo-cli` looks at the Odoo it reaches instead of assuming it.

## What it reads

| Question | How it is answered | Who can read it |
|---|---|---|
| Is it Odoo? | The version handshake answers with an Odoo version | anyone, before login |
| Which series? | `server_serie` | anyone, before login |
| Community or Enterprise? | `web_enterprise` is installed, a module is licensed OEEL-1, or the build is `+e` | any login |
| Which modules? | The installed modules, compared with the module list of the stock image (`odoo_standard_modules.json`) | any login |
| Fields added by hand or by Studio | `fields_get` marks them `manual` | any login |
| Modules that extend the model | `ir.model.modules` | an administrator's login |
| Automations on the model | `base.automation`, when that module is installed | an administrator's login |
| Where it is hosted | An endpoint under odoo.com is Odoo's cloud: Odoo Online or Odoo.sh | reported only |

## What it decides

- **Refused, with nothing executed:**
  - The endpoint is not Odoo (`PROVIDER_NOT_ODOO`).
  - The series is unsupported (`PROVIDER_VERSION_UNSUPPORTED`). The reason is one of
    `OLDER_THAN_SUPPORTED`, `NEWER_THAN_SUPPORTED`, `INTERMEDIATE_RELEASE` (an Odoo Online `saas~`
    release) or `CANDIDATE_NOT_ADMITTED` (20.0 without `--allow-unverified`).
  - A required module is not installed (`MODULE_NOT_INSTALLED`).
- **Held back (`ENVIRONMENT_UNVERIFIED`)** until the caller passes `--allow-unverified`. This applies
  to a write, business action, dialog or delete on a model that:
  - an Enterprise or third-party module extends;
  - an active automation watches;
  - for a create, has a required field added by hand;
  - or whose extensions or automations this login cannot read, while such modules or automations
    exist.
- **Runs:** every read and print whose modules are installed. If the database runs non-stock
  modules, the result carries a note.

When an operation runs on such an Odoo, its result carries `providerFit`: the verdict, the reasons
and the notice below.

`odoo-cli doctor --profile <name> --probe --output json` reports the whole picture. It shows:
- the series, edition and hosting;
- the standard, Enterprise and third-party modules;
- the models that were changed;
- how many operations are OK, held back or refused, and why.

## Customised Odoo: no verification, no guarantee

> Operations are verified on the stock Odoo image of their series only. On an Odoo changed beyond
> that image — Enterprise, third-party or in-house modules, Studio, automations, fields added by
> hand — they cannot be verified, and nothing guarantees that they work as described or without
> error. An operation you accept there runs at your own risk.

This text appears in four places:
- every verdict on such an Odoo;
- the `ENVIRONMENT_UNVERIFIED` refusal;
- the result of an operation accepted with `--allow-unverified`;
- the doctor's report.

## What it cannot see

- Odoo's source edited in place on the server.
- Access rights and record rules. These show as `ACCESS_DENIED` when an operation runs.
- View changes. Operations act through the API, not through the views.
- Where an Odoo is hosted. Only the image digest of an Odoo you run yourself proves the build.

Quiet output from the check therefore does not prove an Odoo is stock.
