# Operation packs: your module's own operations in odoo-cli

odoo-cli's catalog covers Odoo Community. A custom module has models, buttons and wizards of its own.
A **pack** declares them, so that odoo-cli and the agents that use it can run them. A pack is a JSON
file, not code: odoo-cli runs every operation through its own executor, behind the guards every
catalog operation has.

## For a module's author

1. **Write the pack.** On a lab with the module installed:

   ```sh
   odoo-cli pack generate --profile lab --module my_module --file pack.json
   ```

   It reads the models your module defines, their fields and the object buttons of their views. For
   each model it writes:
   - a list read of the stored plain fields;
   - a create and an update of the fields a person can edit;
   - one call per object button.

   Edit the ids, summaries and risks. Declare wizards and reports by hand (see the format below).
2. **Ship it** as `my_module/static/odoo-cli/pack.json`. Odoo serves a module's static files without
   a login, which is where odoo-cli looks.
3. **Prove it** on your lab, as `conformance/packs/prove.sh` proves the demo module
   `conformance/packs/salon_demo`: every operation through odoo-cli, each checked against Odoo.
4. **Ship the evidence** (optional) as `my_module/static/odoo-cli/evidence.json`: the result of your
   run, in the format below. `prove.py` writes it as `pack-evidence.json`; take it as the pattern for
   your own runner.

## For a person who uses the module

```sh
odoo-cli pack sync --profile my-odoo --dry-run    # what would be added
odoo-cli pack sync --profile my-odoo              # keep the packs the custom modules ship
odoo-cli pack show --profile my-odoo --module my_module
odoo-cli my_module booking confirm --profile my-odoo --provider-target-id 12 --allow-unverified
```

- **Where packs come from.** `sync` reads the pack of every installed module that is neither standard
  nor Enterprise, from your own Odoo only, and checks it.
- **Packs that are not shipped.** For a module that ships none, a Studio customization or Odoo Online,
  write one and run `pack add --file pack.json`. Module `custom` covers what was made in the database
  itself.
- **Nothing changes by itself.** Packs are kept per profile and change only when you run `sync`, `add`
  or `remove`.

## What odoo-cli checks

**Without Odoo, when it reads a pack:**
- the format, with no unknown key;
- the ids, which are `<module>.<entity>.<verb>`, begin with the module, and are no catalog operation;
- the module name, which may not be a catalog family or one of odoo-cli's own commands.

A pack is refused when it names any of these:
- a model that holds credentials or system configuration (`ir.*`), users, groups, company settings,
  automation rules, mail or mail templates;
- a field whose name marks a secret;
- a method that begins with `_`, or one of the ORM's own (`write`, `unlink`, `create`...);
- a delete or a module install.

**Against your Odoo, when it syncs or adds a pack:**
- the module is installed;
- every model and field exists;
- every method is an object button (`<button type="object">`) of the model's views, that is,
  something a person can click;
- a wizard is a transient model, and its method is a button of its form;
- a report exists for the model.

It also fills in the type of each written field (string, integer, number, boolean, reference) from
Odoo.

## What an agent may do

- **Reads.** A pack's reads are READ tools on `mcp serve`.
- **Writes.** Its writes are offered only with `--allow-writes`, and they **always** ask you first,
  also under `--confirm-writes=false`. A pack is its author's declaration, not one ASAO verified.
- **Pack management.** `pack list` and `pack show` are open to an agent. `sync`, `add` and `remove`
  are yours alone.

## Risk

The pack declares a risk, and odoo-cli raises it to these floors:
- a button or a wizard is MEDIUM at least;
- a write to a model that moves money or says who is paid (`res.partner.bank`, `account.payment`,
  `account.move`) is HIGH.

## Trust

A pack's operations are **unverified**. `execute` runs one only with `--allow-unverified`, and
`pack show` names the pack's module, author, source and sha256. The proof run
(`conformance/packs/prove.sh`) is how a module's author shows the pack does what it says. ASAO
verifies the catalog, not your pack.

### The author's evidence (`odoo-cli.pack-evidence/v1`)

A module can ship the result of its author's run next to the pack, as
`static/odoo-cli/evidence.json` (or `pack add --evidence FILE`). `pack sync` reads it with the pack, and
`pack show` then says, for each operation:

| status | when |
|---|---|
| `AUTHOR_VERIFIED` | the run covered it, agreed with Odoo, on this Odoo's series, and the operation is the one it ran |
| `AUTHOR_VERIFIED_OTHER_SERIES` | the same, on another series than this Odoo's |
| `CHANGED_SINCE_EVIDENCE` | the operation as kept differs from the one the run covered: unverified again |
| `AUTHOR_RUN_DISAGREED` | the author's own run found it disagreeing with Odoo |
| `NOT_IN_EVIDENCE` | the run did not cover it |

**What it does not change.** An author-verified operation is still unverified to odoo-cli: `execute`
needs `--allow-unverified`, and an agent's write still asks you. The evidence is the author's claim,
shown as "verified by <author>", never as verified by ASAO. odoo-cli checks only that each operation is
the one the run covered, by its definition hash. It cannot prove who ran the lab or what the run found.
Evidence that does not pass its checks is left out with a warning; the pack is kept, unverified.

```json
{
  "schema": "odoo-cli.pack-evidence/v1",
  "module": "my_module",
  "author": "the pack's author, exactly as the pack names it",
  "odoo": {"series": "19.0", "serverVersion": "19.0-20251010", "image": "docker.io/library/odoo@sha256:..."},
  "runner": "what ran the operations and what read Odoo back",
  "odooCli": "0.18.0",
  "ranAt": "2026-10-20T08:00:00Z",
  "run": "https://github.com/you/my_module/actions/runs/1",
  "operations": [
    {"id": "my_module.booking.confirm", "definitionHash": "sha256:...", "outcome": "AGREE",
     "check": "the state read back is confirmed"}
  ]
}
```

- `definitionHash` is the operation's hash as odoo-cli gave it on your lab after `pack sync`: the
  `definitionHash` of `pack show` (or `catalog describe`). A sync completes the pack with the field types
  of the Odoo it checks, so a database whose fields differ from your lab's gives another hash.
- `outcome` is `AGREE` or `DISAGREE`. `image` is optional, and by digest. `run` is optional, an https
  address.

## Format (`odoo-cli.pack/v1`)

```json
{
  "schema": "odoo-cli.pack/v1",
  "module": "salon_demo",
  "title": "Salon demo",
  "author": "Who wrote the pack",
  "series": ["19.0"],
  "operations": [
    {"id": "salon_demo.booking.list", "kind": "READ", "strategy": "READ", "model": "salon_demo.booking",
     "summary": "Bookings", "readFields": ["name", "partner_id", "start", "state"], "defaultSort": "start desc"},
    {"id": "salon_demo.booking.create", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "salon_demo.booking",
     "writeMode": "CREATE", "fields": ["name", "partner_id", "start", "seats"],
     "relations": {"partner_id": {"model": "res.partner", "searchField": "name"}}},
    {"id": "salon_demo.booking.confirm", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "salon_demo.booking",
     "method": "action_confirm"},
    {"id": "salon_demo.booking.reschedule", "kind": "ACTION", "strategy": "WIZARD", "model": "salon_demo.booking",
     "wizard": {"model": "salon_demo.reschedule", "method": "action_apply", "fields": ["start"]}},
    {"id": "salon_demo.booking.print", "kind": "READ", "strategy": "REPORT", "model": "salon_demo.booking",
     "report": "salon_demo.report_booking"}
  ]
}
```

| strategy | kind | keys | runs |
|---|---|---|---|
| `READ` | `READ` | `readFields`, `defaultSort` | a list, or one record with `--provider-target-id` |
| `FIELD_WRITE` | `COMMAND` | `writeMode` (`CREATE` or `UPDATE`), `fields`, `relations`, `types` | a create, or an update of the target |
| `METHOD_CALL` | `ACTION` | `method` | an object button on the target |
| `WIZARD` | `ACTION` | `wizard` (`model`, `method`, `fields`), `relations`, `types` | the wizard, opened on the target, then its button |
| `REPORT` | `READ` | `report` | the target's PDF (`--save-file`) |

Every operation can also carry a `summary` and a `risk` (`LOW`, `MEDIUM`, `HIGH` or `CRITICAL`).

A `relation` lets a many2one take a name as well as an id. With `searchField`, a value that is not a
number is looked up by that field.

**Not in v1:** deletes, binary fields, one2many and many2many writes, and running a pack through the
governed ASAO product.
