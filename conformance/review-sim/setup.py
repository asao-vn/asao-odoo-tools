# Runs inside `odoo shell` (env is given): Vietnam, VND, the l10n_vn chart, and an API key for admin.
import datetime

company = env.company
company.write({"name": "Công ty TNHH Vật tư Minh An", "country_id": env.ref("base.vn").id,
               "currency_id": env.ref("base.VND").id, "vat": "9900000100", "city": "Đà Nẵng"})
env.ref("base.VND").active = True
if not env["account.account"].search_count([("company_ids", "in", company.id)]):
    env["account.chart.template"].try_loading("vn", company, install_demo=False)
admin = env.ref("base.user_admin")
admin.write({"tz": "Asia/Ho_Chi_Minh", "lang": "en_US",
             "group_ids": [(4, env.ref("account.group_account_manager").id)]})
env.cr.commit()
key = env["res.users.apikeys"].with_user(admin)._generate(None, "review-sim", datetime.datetime.now() + datetime.timedelta(days=7))
env.cr.commit()
print("SETUP chart=%s currency=%s accounts=%d" % (company.chart_template, company.currency_id.name,
      env["account.account"].search_count([])))
print("ASAO_KEY=" + key)
