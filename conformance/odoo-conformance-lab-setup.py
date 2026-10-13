# Runs inside `odoo shell` on a fresh conformance lab (scripts/dev/odoo-conformance-lab.sh), never on a
# business database. It gives the lab what the runners need and demo data does not:
#   - the administrator in the accounting group (journal entries, payments);
#   - the Demo payment provider capturing manually (payment capture and void need authorized payments);
#   - one record in the models demo data leaves empty, so their reads have something to compare;
#   - an API key for the administrator, printed once as ASAO_KEY=... for the caller to store.
# Every step is independent: a step a series does not support is skipped and reported.
import datetime

done, skipped = [], []


def step(name, action):
    try:
        with env.cr.savepoint():
            if action() is not False:
                done.append(name)
    except Exception as error:  # noqa: BLE001 - report and carry on; the caller reads the list
        skipped.append(f"{name}: {str(error).splitlines()[0][:120]}")


admin = env.ref("base.user_admin")


def accounting_group():
    group = env.ref("account.group_account_user")
    field = "group_ids" if "group_ids" in admin._fields else "groups_id"
    admin.write({field: [(4, group.id)]})


def demo_provider():
    provider = env["payment.provider"].search([("code", "=", "demo")], limit=1)
    if not provider:
        return False
    provider.write({"capture_manually": True})


def distribution_model():
    account = env["account.analytic.account"].search([], limit=1)
    if not account or env["account.analytic.distribution.model"].search([], limit=1):
        return False
    env["account.analytic.distribution.model"].create({"analytic_distribution": {str(account.id): 100}})


def sinvoice_symbol():
    model = env.get("l10n_vn_edi_viettel.sinvoice.symbol")
    if model is None or model.search([], limit=1):
        return False
    values = {"name": "C26TCF"}
    if "invoice_template_code" in model._fields:
        values["invoice_template_code"] = "1/001"
    elif "invoice_template_id" in model._fields:
        template = env["l10n_vn_edi_viettel.sinvoice.template"].search([], limit=1) or env["l10n_vn_edi_viettel.sinvoice.template"].create(
            {"name": "1/001", "template_invoice_type": "1"})
        values["invoice_template_id"] = template.id
    model.create(values)


def weekly_recurrence():
    if env["calendar.recurrence"].search([], limit=1):
        return False
    start = datetime.datetime.now().replace(hour=9, minute=0, second=0, microsecond=0) + datetime.timedelta(days=3)
    env["calendar.event"].create({"name": "Conformance weekly", "start": start, "stop": start + datetime.timedelta(hours=1),
                                  "recurrency": True, "rrule_type": "weekly", "mon": True, "count": 3, "end_type": "count"})


def counted_quant():
    quant = env["stock.quant"].search([("location_id.usage", "=", "internal"), ("product_id.is_storable", "=", True)], limit=1)
    if not quant:
        return False
    quant.inventory_quantity = quant.quantity + 1


for name, action in [("accounting group", accounting_group), ("demo provider", demo_provider),
                     ("distribution model", distribution_model), ("sinvoice symbol", sinvoice_symbol),
                     ("weekly recurrence", weekly_recurrence), ("counted quant", counted_quant)]:
    step(name, action)

# 20.0 scopes a key to "rpc" for the JSON-2 API; 19.0 keys are unscoped.
from odoo import release  # noqa: E402

scope = "rpc" if release.version_info[0] >= 20 else None
key = env["res.users.apikeys"].with_user(admin)._generate(scope, "odoo-conformance-lab", datetime.datetime.now() + datetime.timedelta(days=2))
env.cr.commit()
print("SETUP done=" + ",".join(done) + " skipped=" + " | ".join(skipped))
print("ASAO_KEY=" + key)
