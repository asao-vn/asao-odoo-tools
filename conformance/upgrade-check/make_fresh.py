"""make_fresh.py <out.json>: a third set, written after the Go check (packages/odoo/upgradecheck) was
finished and never used to change it: the measurement of odoo-cli upgrade check itself. It also plants
what the research sets left out: references to a custom field, an inheriting view whose root locates a
field, a modifier set through <attribute>, and a window action."""
import json
import sys


def inside(field, extra=""):
    return '<xpath expr="//form" position="inside"><field name="%s"%s/></xpath>' % (field, extra)


items = [
    # the company type, which 20.0 removes (on 19.0 it can be exported, not searched)
    dict(key="n_e_company_type", kind="export", model="res.partner", paths=["name", "company_type"], expect="BREAKS"),
    # a custom field made by a person, then used: the upgrade keeps it
    dict(key="n_x_vip", kind="field", model="res.partner", name="x_planted_vip", ttype="boolean", expect="OK"),
    dict(key="n_f_custom_field", kind="filter", model="res.partner", domain="[('x_planted_vip', '=', True)]", expect="OK"),
    dict(key="n_t_custom_field", kind="template", model="res.partner", body='<p><t t-out="object.x_planted_vip"/></p>', expect="OK"),
    dict(key="n_t_email_cc", kind="template", model="project.task", body='<p><t t-out="object.email_cc"/></p>', expect="BREAKS"),
    dict(key="n_v_root_locator", kind="view", model="res.partner", parent="base.view_partner_form",
         arch='<field name="company_type" position="after"><field name="ref"/></field>', expect="BREAKS"),
    dict(key="n_v_attribute", kind="view", model="res.partner", parent="base.view_partner_form",
         arch='<xpath expr="//field[@name=\'ref\']" position="attributes"><attribute name="invisible">company_type == \'person\'</attribute></xpath>',
         expect="BREAKS"),
    dict(key="n_c_employee_type", kind="code", model="hr.employee", code="for e in records:\n    e.write({'employee_type': e.employee_type})", expect="BREAKS"),
    dict(key="n_a_maintenance_user", kind="automation", model="maintenance.request", domain="[('user_id', '!=', False)]", code="pass",
         touch={"description": "<p>planted</p>"}, expect="BREAKS"),
    dict(key="n_e_mailing_name", kind="export", model="mailing.mailing", paths=["name", "subject"], expect="BREAKS"),
    dict(key="n_d_company_registry", kind="default", model="res.company", field="company_registry", value="0101", expect="BREAKS"),
    dict(key="n_f_task_unsearchable", kind="filter", model="project.task", domain="[('allow_task_dependencies', '=', True)]", expect="BREAKS"),
    dict(key="n_f_survey_layout", kind="filter", model="survey.survey", domain="[('certification_report_layout', '=', 'modern_blue')]", expect="CHECK"),
    dict(key="n_c_lot_uom", kind="code", model="stock.lot", code="records.mapped('product_uom_id')", expect="RENAME"),
    dict(key="n_x_related_type", kind="field", model="sale.order.line", name="x_planted_lead", ttype="float", related="customer_lead", expect="BREAKS"),
    dict(key="n_w_model_removed", kind="window_action", model="stock.scrap", domain="[]", expect="BREAKS"),
    dict(key="n_w_rename", kind="window_action", model="stock.move", domain="[('product_uom', '!=', False)]", expect="RENAME"),
    # controls on fields 20.0 keeps
    dict(key="n_ok_task", kind="filter", model="project.task", domain="[('stage_id.fold', '=', False)]", context="{'group_by': ['project_id']}", expect="OK"),
    dict(key="n_ok_template", kind="template", model="hr.employee", body='<p><t t-out="object.name"/> <t t-out="object.work_email"/></p>', expect="OK"),
    dict(key="n_ok_code", kind="code", model="purchase.order", code="records.mapped('partner_id.name')", expect="OK"),
    dict(key="n_ok_view", kind="view", model="res.partner", parent="base.view_partner_form",
         arch='<xpath expr="//field[@name=\'email\']" position="after"><field name="website"/></xpath>', expect="OK"),
    dict(key="n_ok_automation", kind="automation", model="res.partner", domain="[('email', '!=', False)]", code="for r in records:\n    r.mapped('name')",
         touch={"comment": "<p>planted</p>"}, expect="OK"),
    dict(key="n_ok_default", kind="default", model="project.task", field="priority", value="1", expect="OK"),
]
for item in items:
    item.setdefault("name", "planted " + item["key"])
json.dump(items, open(sys.argv[1], "w", encoding="utf-8"), indent=1)
print(len(items), "fresh items")
