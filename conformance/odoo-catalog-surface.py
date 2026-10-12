#!/usr/bin/env python3
"""Generate the Odoo surface catalog from a surface inventory (scripts/dev/odoo-surface-inventory.py).

The curated catalog (packages/odoo/odoo19_catalog.go) covers 36 of the 192 business models the core
SME module set shows in its UI. This generator covers the rest from what Odoo itself declares:

  * <family>.<entity>.list / .describe  READ, projecting the model's list / form view fields
  * <family>.<entity>.create / .update  FIELD_WRITE, allowing the form's writable scalar fields
  * <family>.<entity>.<verb>            METHOD_CALL, one per business button of the form view

Every generated operation is SURFACE_GENERATED: standalone-only (the governed product never sees it),
UNVERIFIED until a conformance run promotes it, and never placed in the curated catalog. Naming is
the one judgment the generator cannot make, so FAMILY, ENTITY and SKIP below are written by hand
and reviewed like code. Models the curated catalog already covers are left to it.

    scripts/dev/odoo-catalog-surface.py --inventory inventory-19.0.json \
        [--inventory-next inventory-20.0.json] --report coverage.json
"""

import argparse
import ast
import json
import re
import sys

CURATED_CATALOG = "packages/odoo/odoo19_catalog.go"
OUTPUT = "packages/odoo/odoo19_surface_catalog.gen.go"

# Semantic family of a model, by longest matching prefix.
FAMILY = {
    "account.": "finance", "payment.": "finance", "res.bank": "finance", "res.currency": "finance",
    "stock.": "inventory",
    "product.": "catalog", "uom.": "catalog",
    "hr.": "people", "hr.leave": "workforce", "hr.attendance": "workforce", "resource.": "workforce",
    "crm.": "crm", "project.": "work", "sale.": "sales", "purchase.": "procurement",
    "mrp.": "manufacturing", "pos.": "pos", "calendar.": "schedule",
    "res.partner": "identity", "phone.": "identity",
    "res.country": "structure", "res.company": "structure",
    "website.": "website",
    # Vietnam: Viettel SInvoice e-invoicing (l10n_vn_edi_viettel)
    "l10n_vn_edi_viettel.": "finance",
    # Community apps added on 2026-10-10
    "delivery.": "inventory", "event.": "event", "fleet.": "fleet", "loyalty.": "sales",
    "maintenance.": "maintenance", "repair.": "repair", "restaurant.": "pos", "survey.": "survey",
    "hr.applicant": "recruitment", "hr.recruitment": "recruitment", "hr.job.platform": "recruitment",
    "hr.talent.pool": "recruitment",
    # Community apps added on 2026-10-11: email marketing and marketing cards, live chat and its
    # chatbots, lunch, eLearning, and data cleaning (a set-up tool).
    "mailing.": "marketing", "card.": "marketing",
    "im_livechat.": "livechat", "chatbot.": "livechat",
    "lunch.": "lunch", "slide.": "elearning", "data_recycle.": "structure",
}

# Entity names chosen by hand where the mechanical one would be unclear or would collide with a
# curated entity of another model.
ENTITY = {
    "account.account.tag": "account_tag",
    "account.group": "account_group",
    "account.analytic.account": "analytic_account",
    "account.analytic.distribution.model": "analytic_distribution_model",
    "account.analytic.line": "analytic_line",
    "payment.method": "online_payment_method",
    "payment.provider": "payment_provider",
    "payment.token": "payment_token",
    "payment.transaction": "payment_transaction",
    "res.bank": "bank",
    "res.currency": "currency",
    "res.currency.rate": "currency_rate",
    "res.partner.bank": "bank_account",
    "res.partner.category": "tag",
    "res.partner.industry": "industry",
    "res.country": "country",
    "res.country.group": "country_group",
    "res.country.state": "country_state",
    "phone.blacklist": "phone_blacklist",
    "stock.warehouse.orderpoint": "reorder_rule",
    "stock.scrap.reason.tag": "scrap_reason",
    "product.supplierinfo": "vendor_price",
    "product.uom": "product_uom",
    "product.template.attribute.line": "template_attribute_line",
    "product.template.attribute.value": "template_attribute_value",
    "hr.attendance": "attendance_log",
    "hr.attendance.overtime.line": "overtime_line",
    "hr.attendance.overtime.rule": "overtime_rule",
    "hr.attendance.overtime.ruleset": "overtime_ruleset",
    "hr.leave.accrual.level": "accrual_level",
    "hr.leave.accrual.plan": "accrual_plan",
    "hr.leave.allocation": "leave_allocation",
    "hr.leave.mandatory.day": "mandatory_day",
    "hr.leave.type": "leave_type",
    "hr.employee.category": "employee_tag",
    "hr.version": "employee_version",
    "resource.calendar.leaves": "calendar_leave",
    "resource.resource": "resource",
    "mrp.production": "order",
    "mrp.routing.workcenter": "operation",
    "project.project.stage": "project_stage",
    "project.task.type": "task_stage",
    "project.tags": "tag",
    "project.update": "project_update",
    "sale.order.template": "quotation_template",
    "sale.order.template.line": "quotation_template_line",
    "crm.team": "team",
    "crm.team.member": "team_member",
    "crm.tag": "tag",
    "calendar.event.type": "event_type",
    "l10n_vn_edi_viettel.sinvoice.symbol": "einvoice_symbol",
    "l10n_vn_edi_viettel.sinvoice.template": "einvoice_template",
    "delivery.carrier": "shipping_method",
    "delivery.price.rule": "shipping_price_rule",
    "delivery.zip.prefix": "shipping_zip_prefix",
    "stock.landed.cost": "landed_cost",
    "stock.landed.cost.lines": "landed_cost_line",
    "stock.valuation.adjustment.lines": "valuation_adjustment_line",
    "stock.picking.batch": "batch_transfer",
    "purchase.requisition": "agreement",
    "purchase.requisition.line": "agreement_line",
    "loyalty.card": "loyalty_card",
    "loyalty.history": "loyalty_history",
    "loyalty.mail": "loyalty_mail",
    "loyalty.program": "loyalty_program",
    "loyalty.reward": "loyalty_reward",
    "loyalty.rule": "loyalty_rule",
    "restaurant.floor": "floor",
    "restaurant.table": "table",
    "event.event": "event",
    "event.event.ticket": "ticket",
    "event.type": "template",
    "event.type.ticket": "template_ticket",
    "event.type.mail": "template_mail",
    "event.mail": "mail_schedule",
    "event.tag.category": "tag_category",
    "fleet.vehicle.model.brand": "vehicle_brand",
    "fleet.vehicle.model.category": "vehicle_category",
    "fleet.vehicle.state": "vehicle_status",
    "fleet.vehicle.log.contract": "vehicle_contract",
    "fleet.vehicle.log.services": "vehicle_service",
    "fleet.vehicle.odometer": "odometer",
    "fleet.vehicle.assignation.log": "driver_history",
    "maintenance.equipment.category": "equipment_category",
    "repair.order": "order",
    "repair.tags": "tag",
    "hr.applicant": "applicant",
    "hr.applicant.category": "applicant_tag",
    "hr.applicant.refuse.reason": "refuse_reason",
    "hr.applicant.skill": "applicant_skill",
    "hr.job.platform": "job_platform",
    "hr.recruitment.degree": "degree",
    "hr.recruitment.source": "source",
    "hr.recruitment.stage": "stage",
    "hr.talent.pool": "talent_pool",
    "survey.survey": "survey",
    "survey.user_input": "response",
    "survey.user_input.line": "response_line",
}

# Verbs chosen by hand where the method name would be opaque.
METHOD_VERBS = {
    "action_l10n_vn_edi_update_payment_status": "einvoice_send_payment_status",
    # List buttons, named as the list labels them.
    "button_pending": "pause",
    "action_replenish_auto": "automate",
    "action_remove_manual_qty_to_order": "clear_manual_quantity",
}

# Wizards a form opens through a method rather than a dialog action, written by hand because the
# values that select the business outcome cannot be read from a view. Each is still opened the way
# Odoo opens it (the record in the active context), with fixed values.
MANUAL_WIZARDS = [
    # Issue the invoice to Viettel SInvoice through Odoo's own Send & Print wizard, with the
    # Vietnamese EDI selected and no e-mail. Issuing a tax invoice is legally binding and cannot be
    # taken back, hence HIGH.
    {"model": "account.move", "verb": "send_einvoice", "wizard": "account.move.send.wizard",
     "method": "action_send_and_print", "values": {"extra_edis": ["vn_sinvoice_send"], "sending_methods": []},
     "module": "l10n_vn_edi_viettel", "risk": "RiskHigh", "label": "Send to SInvoice"},
]

# Dialogs a business method answers with (scripts/dev/odoo-dialog-scan.py, dialogs-19.0.json), and
# the operation each confirming button becomes: it calls the method as the web client's button does
# and completes the dialog Odoo answers with (WizardOpenerMethod, packages/odoo/provider_dialog.go).
# An entry with "acts" names a method that acts itself when it has nothing to ask (nothing left for
# a backorder): the operation then reads "do it; if Odoo asks this, answer that". Without "acts" the
# method only ever opens the dialog, and an answer without it means nothing happened. An entry with
# "replaces" takes the place of the METHOD_CALL of its method, which would report the dialog as a
# result. "allowed" overrides the
# dialog's writable fields where those are Odoo's own prefill (the scrapped product) or display
# switches (show_transfers) rather than a decision.
OPENED_WIZARDS = [
    {"model": "hr.leave", "opener": "action_cancel", "wizard": "hr.holidays.cancel.leave",
     "button": "action_cancel_leave", "verb": "cancel", "replaces": True},
    {"model": "hr.expense", "opener": "action_refuse", "wizard": "hr.expense.refuse.wizard",
     "button": "action_refuse", "verb": "refuse", "replaces": True},
    # Company-paid expenses post at once; an employee-paid one asks for the journal and date.
    {"model": "hr.expense", "opener": "action_post", "wizard": "hr.expense.post.wizard",
     "button": "action_post_entry", "verb": "post", "replaces": True, "acts": True},
    {"model": "hr.expense", "opener": "action_approve", "wizard": "hr.expense.approve.duplicate",
     "button": "action_approve", "verb": "approve_despite_duplicate", "acts": True},
    {"model": "stock.picking", "opener": "button_validate", "wizard": "stock.backorder.confirmation",
     "button": "process", "verb": "validate_with_backorder", "acts": True, "allowed": []},
    {"model": "stock.picking", "opener": "button_validate", "wizard": "stock.backorder.confirmation",
     "button": "process_cancel_backorder", "verb": "validate_without_backorder", "acts": True, "allowed": []},
    {"model": "mrp.production", "opener": "button_mark_done", "wizard": "mrp.production.backorder",
     "button": "action_backorder", "verb": "mark_done_with_backorder", "acts": True},
    {"model": "mrp.production", "opener": "button_mark_done", "wizard": "mrp.production.backorder",
     "button": "action_close_mo", "verb": "mark_done_without_backorder", "acts": True},
    {"model": "mrp.production", "opener": "button_mark_done", "wizard": "mrp.consumption.warning",
     "button": "action_confirm", "verb": "mark_done_confirm_consumption", "acts": True},
    # Odoo asks in a chain when both apply: the consumption first, then the backorder.
    {"model": "mrp.production", "opener": "button_mark_done", "wizard": "mrp.production.backorder",
     "button": "action_backorder", "verb": "mark_done_confirm_consumption_with_backorder", "acts": True,
     "also": {"mrp.consumption.warning": "action_confirm"}},
    {"model": "mrp.production", "opener": "button_mark_done", "wizard": "mrp.production.backorder",
     "button": "action_close_mo", "verb": "mark_done_confirm_consumption_without_backorder", "acts": True,
     "also": {"mrp.consumption.warning": "action_confirm"}},
    # Scrapping more than the location holds leaves negative stock.
    {"model": "stock.scrap", "opener": "action_validate", "wizard": "stock.warn.insufficient.qty.scrap",
     "button": "action_done", "verb": "validate_despite_shortage", "acts": True, "allowed": [], "risk": "RiskHigh"},
    # Cancelling an issued tax invoice at Viettel SInvoice (production only, see the README) cannot be
    # taken back. The account.move method serves every country; this is the Vietnamese dialog.
    {"model": "account.move", "opener": "button_request_cancel", "wizard": "l10n_vn_edi_viettel.cancellation",
     "button": "button_request_cancel", "verb": "request_cancel", "replaces": True,
     "module": "l10n_vn_edi_viettel", "risk": "RiskHigh"},
]
REPLACED_METHODS = {(w["model"], w["opener"]) for w in OPENED_WIZARDS if w.get("replaces")}

# Standard operations the curated catalog lacks for models it owns, generated under the curated name
# (found by setting up a whole company through the catalog: contracts/business/asao-hq.v0.yaml).
# Which verbs is a reviewed choice: journal items, stock moves and quants get no writes, because
# writing them directly bypasses the documents that keep the books and the stock consistent; and a
# company is updated, not created, because a new company is a provisioning step (chart, journals).
# An entry may name extra fields the form does not show but a business sets as code: a page's QWeb
# content (arch), a logo or favicon (base64 images), the website's <head> code.
CURATED_GAPS = {
    "res.company": ("structure.company", ("describe", "update"), None, ("logo",)),
    "website": ("website.site", ("update",), None, ("logo", "favicon", "custom_code_head", "custom_code_footer", "homepage_url")),
    "website.page": ("website.page", ("describe", "create", "update"), None, ("arch", "key")),
    "website.menu": ("website.menu", ("describe", "create", "update"), None, ()),
    "hr.department": ("people.department", ("update",), None, ()),
    "account.journal": ("finance.journal", ("describe", "create", "update"), "RiskHigh", ()),
    "account.tax": ("finance.tax", ("describe", "create", "update"), "RiskHigh", ()),
    "account.account": ("finance.account.chart", ("describe", "create", "update"), "RiskHigh", ()),
    "account.payment.term": ("finance.payment_term", ("describe", "create", "update"), None, ()),
    "account.payment": ("finance.payment", ("create", "update"), None, ()),
    "resource.calendar": ("workforce.calendar", ("describe", "create", "update"), None, ()),
    "hr.leave": ("workforce.leave", ("describe", "create", "update"), None, ()),
    "stock.picking": ("inventory.transfer", ("create", "update"), None, ()),
    "stock.warehouse": ("structure.warehouse", ("describe", "create", "update"), None, ()),
    "stock.location": ("inventory.location", ("describe", "create", "update"), None, ()),
    "uom.uom": ("catalog.uom", ("create", "update"), None, ()),
    "product.product": ("catalog.product_variant", ("update",), None, ()),
    "project.milestone": ("work.milestone", ("update",), None, ()),
}

# The website layout views a business sets as code (header call to action, text, search and sign-in,
# footer, copyright, brand promotion), and nothing else of ir.ui.view: every website.layout operation
# is held to these keys by its provider domain, so no caller reaches another view. The domain names
# active, so a view the business switched off is still found (Odoo applies no active test then).
WEBSITE_LAYOUT_KEYS = ("website.header_call_to_action", "website.header_text_element", "website.header_search_box",
                       "portal.user_sign_in", "website.footer_custom", "website.footer_copyright_company_name",
                       "website.brand_promotion")

# Website media (images, videos, fonts) a business publishes as code: public binary attachments that
# Odoo serves at their own url (ir.http._serve_fallback). The provider domain holds the records the
# operations reach to WEBSITE_MEDIA_PREFIX, and InputPrefixes holds the url they write there too.
# Creating and updating always makes the file public and binary.
WEBSITE_MEDIA_PREFIX = "/media/"

# Settings a business sets as code: fields of res.config.settings, Odoo's Settings page, with their
# CLI type or, for a choice, its values. They are the feature switches (group_*) and the preferences
# that are a business's own choice, for the modules a business spec installs. Never a module_* field
# (a spec's modules install modules), chart_template (loading a chart is a provisioning step), a field
# that names a record by id (accounts, journals, templates), or a credential (no secret goes in a
# spec). The read shows them as the page does (onchange on a new record); the write is the page's
# Save: a new res.config.settings with only these values set, then execute.
SETTINGS_FIELDS = {
    # CRM and sales
    "group_use_lead": "boolean", "group_use_recurring_revenues": "boolean", "crm_use_auto_assignment": "boolean",
    "group_discount_per_so_line": "boolean", "group_sale_order_template": "boolean",
    "group_proforma_sales": "boolean", "group_warning_sale": "boolean", "group_auto_done_setting": "boolean",
    "portal_confirmation_sign": "boolean", "portal_confirmation_pay": "boolean",
    "quotation_validity_days": "integer", "default_invoice_policy": ("order", "delivery"),
    # Products and purchase
    "group_product_variant": "boolean", "group_product_pricelist": "boolean", "group_uom": "boolean",
    "group_sale_delivery_address": "boolean", "group_send_reminder": "boolean",
    "group_warning_purchase": "boolean", "lock_confirmed_po": "boolean", "po_order_approval": "boolean",
    # Projects, timesheets and people
    "group_project_stages": "boolean", "timesheet_encode_method": ("hours", "days"),
    "reminder_user_allow": "boolean", "reminder_allow": "boolean", "hr_presence_control_login": "boolean",
    # Accounting
    "group_analytic_accounting": "boolean", "group_multi_currency": "boolean", "group_cash_rounding": "boolean",
    "tax_calculation_rounding_method": ("round_per_line", "round_globally"),
    "account_price_include": ("tax_included", "tax_excluded"),
    "qr_code": "boolean", "display_invoice_amount_total_words": "boolean", "pay_invoices_online": "boolean",
    # Access and mail
    "auth_signup_uninvited": ("b2b", "b2c"), "auth_signup_reset_password": "boolean",
    "auth_totp_policy": ("employee_required", "all_required"), "restrict_template_rendering": "boolean",
    "digest_emails": "boolean",
}

# Manual journal entries (account.move of type entry). Odoo refuses a line that leaves its entry
# unbalanced, even in draft, so an entry is made with all its lines in one call; a line names its
# account by code (1111) and its partner by name.
ENTRY_DOMAIN = [["move_type", "=", "entry"]]
INVOICE_DOMAIN = [["move_type", "in", ["out_invoice", "out_refund", "in_invoice", "in_refund", "out_receipt", "in_receipt"]]]

# Inventory counts (stock.quant in inventory mode): a count is set on the quant of a product and an
# internal location, reviewed, then applied or cleared. Applying is held to counted quants: Odoo's
# action_apply_inventory applies a quant's inventory quantity whether it was counted or not, and an
# uncounted quant's is 0, so applying one wipes its stock.
COUNT_DOMAIN = [["location_id.usage", "=", "internal"]]
COUNTED_DOMAIN = COUNT_DOMAIN + [["inventory_quantity_set", "=", True]]

# The system parameters a deployment sets as code, and nothing else of ir.config_parameter:
# report.url is the address Odoo's PDF renderer reaches Odoo itself on (inside a container, Odoo's own
# port, not the public one: without it a printed report loses its header, logo and style),
# web.base.url the public address links are made with, and web.base.url.freeze keeps that address
# from following the last administrator login.
SYSTEM_PARAMETER_KEYS = ("report.url", "web.base.url", "web.base.url.freeze")

# The PDF reports a business prints, by operation: (model, module, report, domain). Odoo serves a
# report only to a logged-in web session, so these operations log one in with the profile's web
# password, as the web client's Print menu does (packages/odoo/rpc_report.go).
PRINTS = {
    "sales.order.print": ("sale.order", "sale", "sale.report_saleorder", None),
    "procurement.rfq.print": ("purchase.order", "purchase", "purchase.report_purchasequotation", None),
    "procurement.order.print": ("purchase.order", "purchase", "purchase.report_purchaseorder", None),
    "finance.invoice.print": ("account.move", "account", "account.report_invoice_with_payments", INVOICE_DOMAIN),
    "finance.payment.print": ("account.payment", "account", "account.report_payment_receipt", None),
    "inventory.transfer.print": ("stock.picking", "stock", "stock.report_deliveryslip", None),
    "manufacturing.order.print": ("mrp.production", "mrp", "mrp.report_mrporder", None),
}

# The records a business talks about and files documents on: entity -> (model, module, domain). Each
# gets an internal note and a scheduled activity on its chatter, and its attachments. An activity names
# its due date: Odoo's activity_schedule would make it due today where the web client's dialog proposes
# the activity type's delay, and the catalog guesses neither.
COLLABORATION = {
    "crm.lead": ("crm.lead", "crm", None),
    "sales.order": ("sale.order", "sale", None),
    "procurement.order": ("purchase.order", "purchase", None),
    "finance.invoice": ("account.move", "account", INVOICE_DOMAIN),
    "finance.journal_entry": ("account.move", "account", ENTRY_DOMAIN),
    "work.project": ("project.project", "project", None),
    "work.task": ("project.task", "project", None),
    "identity.contact": ("res.partner", "mail", None),
    "inventory.transfer": ("stock.picking", "stock", None),
    "people.expense": ("hr.expense", "hr_expense", None),
}

# Guarded deletes for the records a business spec removes (absent: true): operation -> (model, module,
# the records that point at the target, by field). The delete is refused while any of those exists,
# archived ones included (DESTRUCTIVE_GUARDED), so removing a stage never strands the leads in it.
# Nothing points at website media (a page names a file by its url): its provider domain, the media
# under WEBSITE_MEDIA_PREFIX, is its whole guard.
GUARDED_DELETES = {
    "crm.stage.delete": ("crm.stage", "crm", (("crm.lead", "stage_id"),)),
    "crm.lost_reason.delete": ("crm.lost.reason", "crm", (("crm.lead", "lost_reason_id"),)),
    "work.task_stage.delete": ("project.task.type", "project", (("project.task", "stage_id"),)),
    "people.job.delete": ("hr.job", "hr", (("hr.employee", "job_id"),)),
    "website.menu.delete": ("website.menu", "website", (("website.menu", "parent_id"),)),
    "website.media.delete": ("ir.attachment", "website", ()),
}

# Models the curated catalog owns. The generator adds only their uncovered business buttons, named
# under the curated entity so `odoo sales order lock` sits next to `odoo sales order confirm`.
CURATED_ENTITY = {
    "account.move": "finance.invoice",
    "account.payment": "finance.payment",
    "calendar.event": "schedule.event",
    "crm.lead": "crm.lead",
    "hr.employee": "people.employee",
    "hr.leave": "workforce.leave",
    "product.product": "catalog.product_variant",
    "product.template": "catalog.product",
    "purchase.order": "procurement.order",
    "resource.calendar": "workforce.calendar",
    "sale.order": "sales.order",
    "stock.picking": "inventory.transfer",
}

# Wizards a business form opens in a dialog, and the verb each confirming button stands for. Only
# these are generated: mail.activity.schedule is messaging, and sale.advance.payment.inv and
# crm.lead2opportunity.partner are already curated (sales.invoice.create, crm.opportunity.convert).
WIZARD_VERBS = {
    ("account.move.reversal", "refund_moves"): "reverse",
    ("account.move.reversal", "modify_moves"): "reverse_and_replace",
    ("crm.lead.lost", "action_lost_reason_apply"): "mark_lost",
    ("stock.return.picking", "action_create_returns"): "return",
    ("stock.return.picking", "action_create_returns_all"): "return_all",
    ("stock.return.picking", "action_create_exchanges"): "return_for_exchange",
    ("pos.make.payment", "check"): "pay",
    ("change.production.qty", "change_prod_qty"): "change_quantity",
    ("hr.version.wizard", "action_load_template"): "load_contract_template",
}

# Models with a UI that are not business records an operator works on through this tool.
SKIP = {
    # framework helpers, SQL views, reports and UI preferences
    "account.full.reconcile", "account.partial.reconcile", "account.tax.repartition.line",
    "account.analytic.line.calendar.employee", "calendar.filters", "crm.lead.scoring.frequency",
    "crm.lead.scoring.frequency.field", "hr.employee.public", "hr.leave.report.calendar",
    "project.task.stage.personal", "project.sale.line.employee.map", "purchase.bill.line.match",
    "purchase.bill.union", "product.value", "product.attribute.custom.value",
    "product.template.attribute.exclusion", "stock.reference", "website.technical.page",
    "pos.pack.operation.lot", "pos.bill", "pos.printer", "pos.note", "product.removal",
    # paid IAP lead mining and PDF quote building
    "crm.iap.lead.industry", "crm.iap.lead.mining.request", "crm.iap.lead.role",
    "crm.iap.lead.seniority", "quotation.document", "sale.pdf.form.field",
    # identity and security belong to ASAO, not to the ERP provider
    "res.users", "res.role",
    # gamification, link tracking and scheduler internals the 2026-10-10 apps brought along
    "gamification.badge", "gamification.badge.user", "gamification.challenge", "gamification.challenge.line",
    "gamification.goal", "gamification.goal.definition", "gamification.karma.rank", "gamification.karma.tracking",
    "link.tracker", "link.tracker.click", "link.tracker.code", "event.mail.registration", "event.mail.slot",
    "event.lead.request", "pos_self_order.custom_link", "purchase.order.group", "sale.order.coupon.points",
    "restaurant.order.course",
    # An SQL view reporting live chat sessions: read-only by construction.
    "im_livechat.report.channel",
}

# Business buttons only: no navigation, printing, messaging, wizards, installs or account creation.
SKIP_METHOD_PREFIXES = (
    "action_view_", "action_open_", "action_see_", "action_show_", "open_", "print_", "preview_",
    "action_print", "action_preview", "action_customer_preview", "action_redirect", "action_get_",
    "action_product_forecast", "action_product_tmpl_forecast", "action_send", "action_sendmail",
    "action_invoice_sent", "action_quotation_send", "action_rfq_send", "mail_action_",
    "phone_action_", "action_join_video_call", "action_schedule_meeting", "schedule_meeting",
    "action_buy_credits", "iap_", "button_immediate_install", "close_ui", "do_print",
)
SKIP_METHODS = {
    "clear_videocall_location", "set_discuss_videocall_location", "geo_localize",
    "open_barcode_scanner", "open_frontend_cb", "action_picking_move_tree",
    "action_detailed_operations", "action_reconcile_stat", "action_lot_open_transfers",
    "action_in_attendance_maps", "action_out_attendance_maps", "action_time_off_dashboard",
    "action_used_in_bom", "action_purchase_comparison", "action_purchase_matching",
    "action_bill_matching", "action_related_contacts", "action_employee_from_department",
    "action_timesheet_from_employee", "action_plan_from_department", "project_update_all_action",
    "action_sale_quotations_new", "action_add_from_catalog", "action_add_from_catalog_raw",
    "action_save_new", "action_stock_picking", "action_next_transfer", "button_open_bills",
    "button_open_invoices", "button_open_statement_lines", "action_all_company_branches",
    "action_create_user", "create_company", "action_toggle_primary_bank_account_trust",
    "action_delete_duplicates", "unlink", "action_split_wizard", "action_refund_wizard",
    "action_register_payment", "action_force_register_payment", "action_add",
    # UI variants or wizard openers of operations the curated catalog already has
    "action_set_won_rainbowman", "action_reverse",
    # They open a form to create another record, which that record's create operation does
    # (manufacturing.unbuild.create, workforce.accrual_level.create).
    "button_unbuild", "action_create_accrual_plan_level",
    # It only opens the serial numbers dialog, whose buttons need a serial plan no view states.
    "action_generate_serial",
    # List buttons that are not business operations: a paid IAP service, an information popup, a
    # developer view, and a dialog that needs a destination no list states.
    "action_generate_leads", "action_stock_replenishment_info", "action_page_debug_view",
    "action_stock_quant_relocate",
    # copy_to_bom copies an operation into the bill of materials the list was opened from, which
    # only that list's context names. The POS list's Create Invoices opens the pos.make.invoice
    # dialog for what the form's Invoice button (pos.order.pos_order_invoice) already does.
    "copy_to_bom", "action_create_invoices",
    # Navigation: on the 19.0 lab each returns an ir.actions.act_window onto another model or an
    # act_url, and changes nothing (action conformance: AGREE_NO_CHANGE on twins).
    "action_event_view", "action_brand_model", "action_model_vehicle", "action_lot_open_repairs",
    "action_talent_pool_stat_button", "action_survey_see_leads", "action_result_survey",
    "action_survey_user_input", "action_survey_user_input_certified", "action_survey_user_input_completed",
    "action_survey_preview_certification_template", "action_test_survey", "action_create_meeting",
    "install_more_provider", "action_batch_detailed_operations", "action_lost_leads",
    # The apps added on 2026-10-11. Sending email or SMS stays out, as for quotations and invoices:
    # launching, queueing, scheduling, testing, retrying or picking the winner of a mailing; inviting
    # or enrolling eLearning attendees (the invitation is emailed) and inviting event guests; sending
    # a coupon, an abandoned-cart reminder or a survey again; sharing a marketing card; telling
    # employees their lunch has arrived. Buying SMS credits is a paid IAP service. The rest only
    # opens another window: a mailing to the attendees, the A/B versions, the chatbot's test page,
    # a survey's courses, a vehicle's contracts.
    "action_launch", "action_put_in_queue", "action_schedule", "action_test", "action_retry_failed",
    "action_select_as_winner", "action_channel_invite", "action_channel_enroll", "action_invite_contacts",
    "action_coupon_send", "action_recovery_email_send", "action_resend", "action_share", "action_notify",
    "action_buy_sms_credits",
    "action_mass_mailing_attendees", "action_compare_versions", "action_test_script",
    "action_survey_view_slide_channels", "return_action_to_open",
}
HIGH_RISK_VERBS = ("unbuild", "void", "reverse", "refund", "hash", "unlock", "reset")
# A field with one of these names would become a --model / --method flag, which the CLI reserves for
# raw provider RPC and refuses on canonical operations (TestNoRawProviderRPCSurface). Such a field is
# still read; it is never offered for writing.
RESERVED_INPUT_NAMES = {"model", "method", "odoo_model", "odoo_method"}
RESERVED_VERBS = {"rpc", "call", "raw", "exec_model", "xmlrpc", "jsonrpc"}

UNREADABLE_TYPES = {"binary", "properties", "properties_definition", "json"}
NOISE_FIELD_PREFIXES = ("message_", "activity_", "website_message", "rating_", "has_message", "my_activity")
LIST_LIMIT, DESCRIBE_LIMIT = 30, 80
CURATED_WRITE_CAPABILITY = {}


def curated_surface():
    """Semantic ids, provider models, (model, method) pairs and per-model write capability of the
    curated catalog."""
    text = open(CURATED_CATALOG, encoding="utf-8").read()
    ids, models, methods = set(), set(), set()
    global CURATED_WRITE_CAPABILITY
    for chunk in text.split("SemanticID:")[1:]:
        semantic = re.match(r'\s*"([^"]+)"', chunk).group(1)
        ids.add(semantic)
        model = re.search(r'ProviderModel:\s*"([^"]+)"', chunk)
        capability_key = re.search(r'RequiredCapability:\s*"([^"]+)"', chunk)
        if model and capability_key and capability_key.group(1).endswith(".write"):
            CURATED_WRITE_CAPABILITY.setdefault(model.group(1), capability_key.group(1))
        if model:
            models.add(model.group(1))
            method = re.search(r'\bMethod:\s*"([^"]+)"', chunk)
            if method:
                methods.add((model.group(1), method.group(1)))
        wizard = re.search(r'WizardModel:\s*"([^"]+)"', chunk)
        if wizard:
            models.add(wizard.group(1))
    return ids, models, methods


def family_of(model):
    best = ""
    for prefix in FAMILY:
        if (model == prefix.rstrip(".") or model.startswith(prefix)) and len(prefix) > len(best):
            best = prefix
    return FAMILY.get(best)


def entity_of(model):
    if model in ENTITY:
        return ENTITY[model]
    rest = model.split(".", 1)[1] if "." in model else model
    return rest.replace(".", "_")


def capability(family, entity, access):
    return f"{family}.{entity.replace('_', '')}.{access}"


def verb_of(method):
    if method in METHOD_VERBS:
        return METHOD_VERBS[method]
    verb = method
    for prefix in ("action_", "button_", "do_"):
        if verb.startswith(prefix):
            verb = verb[len(prefix):]
            break
    verb = verb.replace("payment_action_", "payment_").replace("_rainbowman", "")
    return verb


def readable(fields, name):
    meta = fields.get(name)
    if not meta or meta["type"] in UNREADABLE_TYPES or name.startswith(NOISE_FIELD_PREFIXES):
        return False
    return name not in ("id", "display_name")


def writable(fields, name, required_ok=False):
    meta = fields.get(name)
    # A required stored field is set at creation even when declared readonly (mrp.workorder's
    # production_id, project.update's project_id): without it the create could never succeed.
    if not meta or (meta["readonly"] and not (required_ok and meta["required"] and meta["store"])):
        return False
    if meta["type"] in UNREADABLE_TYPES | {"one2many", "many2many", "monetary_currency"}:
        return False
    # Rich text is left to the web editor, except where Odoo requires it (a lunch alert's message):
    # without it the create could never succeed.
    if meta["type"] == "html" and not (required_ok and meta["required"]):
        return False
    return (not name.startswith(NOISE_FIELD_PREFIXES) and name not in ("id", "display_name")
            and name not in RESERVED_INPUT_NAMES)


# Fields a form offers that Odoo sets itself whatever the create says: offering them as inputs would
# promise a value Odoo silently replaces (found by write conformance, DISAGREE on fresh twins).
OVERRIDDEN_INPUTS = {
    ("stock.warehouse", "lot_stock_id"): "the warehouse makes its own stock location",
    ("stock.warehouse", "view_location_id"): "the warehouse makes its own view location",
    ("repair.order", "location_dest_id"): "computed from the repair operation type",
    ("repair.order", "parts_location_id"): "computed from the repair operation type",
    ("purchase.requisition", "name"): "the agreement's reference comes from its sequence",
    ("hr.recruitment.source", "name"): "the source's name is its UTM source's",
    ("hr.expense", "price_unit"): "computed from the product's cost and the total",
    ("mailing.mailing", "source_id"): "the mailing's name is its UTM source's: a source given replaces the name",
    ("lunch.supplier", "cron_id"): "the supplier makes its own automatic-order job",
    ("lunch.alert", "cron_id"): "the alert makes its own notification job",
}

UI_ONLY_CONTEXT_KEYS = {"dialog_size"}


def parse_context(text):
    """Constant keys, and keys bound to the target record, of a button or action context.

    Only what can be read without evaluating Odoo's expression language is kept: a constant value,
    or the record itself (`id`, `active_id` -> its id; `active_ids`, `[id]` -> a one-element list).
    A key computed from anything else (a field of the record, a function) is dropped.
    """
    static, target = {}, {}
    try:
        node = ast.parse((text or "").strip() or "{}", mode="eval").body
    except SyntaxError:
        return static, target
    if not isinstance(node, ast.Dict):
        return static, target
    for key_node, value in zip(node.keys, node.values):
        if not isinstance(key_node, ast.Constant) or not isinstance(key_node.value, str):
            continue
        key = key_node.value
        if key in UI_ONLY_CONTEXT_KEYS:
            continue
        if isinstance(value, ast.Constant) and isinstance(value.value, (str, int, float, bool)):
            static[key] = value.value
        elif isinstance(value, ast.Name) and value.id in ("id", "active_id"):
            target[key] = "id"
        elif isinstance(value, ast.Name) and value.id == "active_ids":
            target[key] = "ids"
        elif (isinstance(value, ast.List) and len(value.elts) == 1 and isinstance(value.elts[0], ast.Name)
              and value.elts[0].id in ("id", "active_id")):
            target[key] = "ids"
    return static, target


def cli_type(meta):
    """The CLI input type of a provider field, so --set types the value before Odoo sees it."""
    return {"boolean": "boolean", "integer": "integer", "float": "number", "monetary": "number",
            "many2one": "reference"}.get(meta["type"], "string")


def input_typing(fields, names):
    types = {name: cli_type(fields[name]) for name in names if cli_type(fields[name]) != "string"}
    enums = {name: fields[name]["selection"] for name in names
             if fields[name]["type"] == "selection" and fields[name].get("selection")}
    return types, enums


def go_strings(values):
    return "[]string{" + ", ".join(json.dumps(v) for v in values) + "}"


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--inventory", required=True, help="surface inventory of the baseline series (19.0)")
    parser.add_argument("--inventory-next", help="surface inventory of a candidate series, for the report")
    parser.add_argument("--dialogs", required=True, help="dialog scan of the baseline series (scripts/dev/odoo-dialog-scan.py)")
    parser.add_argument("--report", required=True)
    options = parser.parse_args()

    inventory = json.load(open(options.inventory, encoding="utf-8"))
    dialog_scan = json.load(open(options.dialogs, encoding="utf-8"))
    if dialog_scan["provider"]["series"] != inventory["provider"]["server_serie"]:
        raise SystemExit("the dialog scan and the inventory are of different series")
    following = json.load(open(options.inventory_next, encoding="utf-8")) if options.inventory_next else None
    next_models = {m["model"]: m for m in (following or {}).get("models", []) if "error" not in m}
    curated_ids, curated_models, curated_methods = curated_surface()
    by_model = {m["model"]: m for m in inventory["models"] if "error" not in m}

    operations, report = [], {"series": inventory["provider"], "models": [], "skipped": sorted(SKIP)}
    taken = set(curated_ids)

    def add(op):
        if op["id"] in taken:
            raise SystemExit(f"generated id {op['id']} collides with an existing operation")
        taken.add(op["id"])
        operations.append(op)

    def business_buttons(model, views, seen_verbs):
        """(verb, method, label) for every business button the curated catalog does not cover: the
        form's, then the list's. A list button acts on the selected records as a form button acts
        on the open one (mrp.workorder's start/finish/pause, stock.picking's unreserve)."""
        out, methods_seen = [], set()
        for button in views.get("form", {}).get("buttons", []) + views.get("list", {}).get("buttons", []):
            method = button["name"]
            # A form often shows one method on two buttons (per state); it is one operation.
            if button["type"] != "object" or (model, method) in curated_methods or method in methods_seen:
                continue
            methods_seen.add(method)
            if method.startswith(SKIP_METHOD_PREFIXES) or method in SKIP_METHODS or method.startswith("_"):
                continue
            if (model, method) in REPLACED_METHODS:
                continue
            verb = verb_of(method)
            if verb in seen_verbs:
                verb = method
            if verb in seen_verbs or verb in RESERVED_VERBS:
                continue
            seen_verbs.add(verb)
            out.append((verb, method, button["string"]))
        return out

    wizards = inventory.get("wizards", {})

    def wizard_operations(model, record, base, capability_key, taken_verbs):
        """One WIZARD operation per confirming button of each dialog wizard the form opens."""
        out = []
        for button in record.get("views", {}).get("form", {}).get("buttons", []):
            action = button.get("action") or {}
            if button["type"] != "action" or action.get("target") != "new":
                continue
            wizard = action.get("model")
            spec = wizards.get(wizard)
            if not spec or "error" in spec:
                continue
            wfields = spec["fields"]
            # Odoo's own button opens the wizard with the target in the context (active_model,
            # active_id, active_ids) and lets default_get find it; a link field is not set, because
            # the wizard's fields relating back to the model can as well be outputs
            # (account.move.reversal.new_move_ids).
            link = ""
            allowed = [f for f in spec["form"]["own_fields"] if f in wfields and not wfields[f]["readonly"]
                       and wfields[f]["type"] not in ("one2many", "many2many", "binary")
                       and wfields[f]["relation"] != model and f not in RESERVED_INPUT_NAMES]
            types, enums = input_typing({k: {**v, "selection": v.get("selection", [])} for k, v in wfields.items()}, allowed)
            # The action's context first, then the button's, which Odoo lets override it.
            context, context_target = parse_context(action.get("context"))
            button_static, button_target = parse_context(button.get("context"))
            context.update(button_static)
            context_target.update(button_target)
            for wbutton in spec["form"]["buttons"]:
                verb = WIZARD_VERBS.get((wizard, wbutton["name"]))
                if wbutton["type"] != "object" or not verb or verb in taken_verbs:
                    continue
                taken_verbs.add(verb)
                out.append({"id": f"{base}.{verb}", "kind": "KindAction", "strategy": "StrategyWizard",
                            "model": model, "module": record["defining_module"], "capability": capability_key,
                            "risk": "RiskHigh" if any(w in verb for w in HIGH_RISK_VERBS) else "RiskMedium",
                            "wizard": wizard, "wizard_method": wbutton["name"], "link": link,
                            "link_multi": bool(link) and wfields[link]["type"] == "many2many",
                            "wizard_allowed": allowed, "types": types, "enums": enums,
                            "context": context, "context_target": context_target,
                            "label": record["name"], "button": f'{button["string"]} > {wbutton["string"]}'})
        return out

    def opened_operations(model, record, base, capability_key, taken_verbs):
        """The OPENED_WIZARDS operations of a model, each checked against the dialog scan: the method
        must be seen answering with the dialog, and the dialog's form must offer the button."""
        out = []
        form_buttons = {b["name"]: b for b in record.get("views", {}).get("form", {}).get("buttons", []) if b["type"] == "object"}
        for entry in OPENED_WIZARDS:
            if entry["model"] != model:
                continue
            seen = dialog_scan["buttons"].get(f"{model}|{entry['opener']}", {}).get("dialogs", {})
            if entry["wizard"] not in seen:
                raise SystemExit(f"{model}.{entry['opener']} is not seen answering with {entry['wizard']}: re-run the dialog scan or fix OPENED_WIZARDS")
            spec = dialog_scan["dialogs"].get(entry["wizard"]) or {}
            if "error" in spec or not spec:
                raise SystemExit(f"the dialog scan has no form for {entry['wizard']}")
            button = next((b for b in spec["form"]["buttons"] if b["name"] == entry["button"]), None)
            if button is None or entry["opener"] not in form_buttons:
                raise SystemExit(f"{entry['wizard']} has no button {entry['button']}, or {model}'s form no {entry['opener']}")
            also = entry.get("also", {})
            for other, other_button in also.items():
                other_spec = dialog_scan["dialogs"].get(other) or {}
                if other not in seen or not any(b["name"] == other_button for b in other_spec.get("form", {}).get("buttons", [])):
                    raise SystemExit(f"{model}.{entry['opener']} is not seen asking {other}, or it has no button {other_button}")
            verb = entry["verb"]
            if verb in taken_verbs:
                raise SystemExit(f"{base}.{verb} is taken")
            taken_verbs.add(verb)
            wfields = spec["fields"]
            if "allowed" in entry:
                allowed = list(entry["allowed"])
            else:
                allowed = [f for f in spec["form"]["own_fields"] if f in wfields and not wfields[f]["readonly"]
                           and wfields[f]["type"] not in ("one2many", "many2many", "binary")
                           and wfields[f]["relation"] != model and f not in RESERVED_INPUT_NAMES]
            types, enums = input_typing(wfields, allowed)
            out.append({"id": f"{base}.{verb}", "kind": "KindAction", "strategy": "StrategyWizard", "model": model,
                        "module": entry.get("module") or record["defining_module"], "capability": capability_key,
                        "risk": entry.get("risk") or ("RiskHigh" if any(w in verb for w in HIGH_RISK_VERBS) else "RiskMedium"),
                        "wizard": entry["wizard"], "wizard_method": entry["button"], "opener": entry["opener"],
                        "opener_acts": bool(entry.get("acts")), "also": also,
                        "link": "", "link_multi": False, "wizard_allowed": allowed, "types": types, "enums": enums,
                        "context": {}, "context_target": {}, "label": record["name"],
                        "button": f'{form_buttons[entry["opener"]]["string"]} > {button["string"]}'})
        return out

    def standard_operations(model, record, base, verbs, read_capability, write_capability, write_risk=None, extra=()):
        """The list/describe READ and create/update FIELD_WRITE operations of a model, for the verbs
        asked: reads project the list and form views' own fields; writes allow the form's writable
        scalar fields and every required stored field, resolving many2one names through relations."""
        views = record.get("views", {})
        form, listing = views.get("form", {}), views.get("list", {})
        fields = record["fields"]
        module = record["defining_module"]
        label = record["name"]
        out = []
        projections = {"list": [f for f in listing.get("own_fields", []) if readable(fields, f)][:LIST_LIMIT],
                       "describe": [f for f in form.get("own_fields", []) if readable(fields, f)][:DESCRIBE_LIMIT]}
        missing = [f for f in extra if f not in fields or fields[f]["readonly"]]
        if missing:
            raise SystemExit(f"CURATED_GAPS extra fields of {model} are not writable fields: {missing}")
        projections["describe"] += [f for f in extra if readable(fields, f) and f not in projections["describe"]]
        for verb in ("list", "describe"):
            if verb in verbs:
                out.append({"id": f"{base}.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": model,
                            "module": module, "capability": read_capability, "risk": "RiskLow",
                            "read": ["id", "display_name"], "optional": projections[verb], "label": label})
        allowed = [f for f in form.get("own_fields", []) if writable(fields, f) and (model, f) not in OVERRIDDEN_INPUTS]
        for name, meta in sorted(fields.items()):
            if (meta["required"] and meta["store"] and writable(fields, name, required_ok=True) and name not in allowed
                    and (model, name) not in OVERRIDDEN_INPUTS):
                allowed.append(name)
        allowed += [f for f in extra if f not in allowed]
        relations = {}
        for name in allowed:
            meta = fields[name]
            target = by_model.get(meta["relation"]) if meta["type"] == "many2one" else None
            if target:
                name_field = target["fields"].get("name")
                relations[name] = {"model": meta["relation"],
                                   "search": "name" if name_field and name_field["type"] == "char" else ""}
        if allowed:
            types, enums = input_typing(fields, allowed)
            for verb, mode in (("create", "WriteModeCreate"), ("update", "WriteModeUpdate")):
                if verb in verbs:
                    out.append({"id": f"{base}.{verb}", "kind": "KindCommand", "strategy": "StrategyFieldWrite",
                                "model": model, "module": module, "capability": write_capability,
                                "risk": write_risk or ("RiskMedium" if "state" in fields else "RiskLow"), "mode": mode,
                                "allowed": allowed, "relations": relations, "label": label, "types": types, "enums": enums})
        return out, projections, allowed

    # Uncovered business buttons of the models the curated catalog owns.
    for model, base in sorted(CURATED_ENTITY.items()):
        record = by_model.get(model)
        if record is None:
            continue
        family, entity = base.split(".", 1)
        taken_verbs = {i[len(base) + 1:] for i in curated_ids if i.startswith(base + ".") and "." not in i[len(base) + 1:]}
        entry = {"model": model, "curated_entity": base, "operations": []}
        for verb, method, label in business_buttons(model, record.get("views", {}), taken_verbs):
            risk = "RiskHigh" if any(word in verb for word in HIGH_RISK_VERBS) else "RiskMedium"
            add({"id": f"{base}.{verb}", "kind": "KindAction", "strategy": "StrategyMethodCall",
                 "model": model, "module": record["defining_module"],
                 "capability": CURATED_WRITE_CAPABILITY.get(model) or capability(family, entity, "write"),
                 "risk": risk, "method": method, "label": record["name"], "button": label})
            entry["operations"].append(f"{base}.{verb}")
        capability_key = CURATED_WRITE_CAPABILITY.get(model) or capability(family, entity, "write")
        for op in wizard_operations(model, record, base, capability_key, taken_verbs):
            add(op)
            entry["operations"].append(op["id"])
        for op in opened_operations(model, record, base, capability_key, taken_verbs):
            add(op)
            entry["operations"].append(op["id"])
        for manual in MANUAL_WIZARDS:
            if manual["model"] != model or manual["verb"] in taken_verbs:
                continue
            taken_verbs.add(manual["verb"])
            add({"id": f"{base}.{manual['verb']}", "kind": "KindAction", "strategy": "StrategyWizard",
                 "model": model, "module": manual["module"], "capability": capability_key, "risk": manual["risk"],
                 "wizard": manual["wizard"], "wizard_method": manual["method"], "link": "", "link_multi": False,
                 "wizard_allowed": [], "wizard_values": manual["values"], "types": {}, "enums": {},
                 "context": {}, "context_target": {}, "label": record["name"], "button": manual["label"]})
            entry["operations"].append(f"{base}.{manual['verb']}")
        if entry["operations"]:
            report["models"].append(entry)

    # Standard operations the curated catalog lacks for models it owns (CURATED_GAPS).
    for model, (base, verbs, risk, extra) in sorted(CURATED_GAPS.items()):
        record = by_model.get(model)
        if record is None:
            continue
        if model not in curated_models:
            raise SystemExit(f"CURATED_GAPS names {model}, which the curated catalog does not own")
        taken = {i[len(base) + 1:] for i in curated_ids if i.startswith(base + ".") and "." not in i[len(base) + 1:]}
        if taken & set(verbs):
            raise SystemExit(f"CURATED_GAPS asks {base}.{sorted(taken & set(verbs))}, which the curated catalog has")
        family, entity = base.split(".", 1)
        made, _, _ = standard_operations(model, record, base, verbs, capability(family, entity, "read"),
                                         CURATED_WRITE_CAPABILITY.get(model) or capability(family, entity, "write"), risk, extra)
        for op in made:
            add(op)
        report["models"].append({"model": model, "curated_entity": base, "operations": [op["id"] for op in made]})

    # Website layout views (WEBSITE_LAYOUT_KEYS): read and updated; never created or deleted.
    layout_domain = [["key", "in", list(WEBSITE_LAYOUT_KEYS)], ["active", "in", [True, False]]]
    for verb in ("list", "describe"):
        add({"id": f"website.layout.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": "ir.ui.view",
             "module": "website", "capability": "website.layout.read", "risk": "RiskLow", "read": ["id", "display_name"],
             "optional": ["key", "name", "active", "website_id", "arch"], "label": "Website layout view", "domain": layout_domain})
    add({"id": "website.layout.update", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "ir.ui.view",
         "module": "website", "capability": "website.layout.write", "risk": "RiskMedium", "mode": "WriteModeUpdate",
         "allowed": ["arch", "active"], "relations": {}, "label": "Website layout view", "types": {"active": "boolean"},
         "enums": {}, "domain": layout_domain})
    report["models"].append({"model": "ir.ui.view", "scope": list(WEBSITE_LAYOUT_KEYS),
                             "operations": ["website.layout.list", "website.layout.describe", "website.layout.update"]})

    # Website media (WEBSITE_MEDIA_PREFIX): public binary attachments served at their url.
    media_domain = [["url", "=like", WEBSITE_MEDIA_PREFIX + "%"], ["type", "=", "binary"]]
    media_forced = {"public": True, "type": "binary", "res_model": "ir.ui.view"}
    for verb in ("list", "describe"):
        add({"id": f"website.media.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": "ir.attachment",
             "module": "website", "capability": "website.media.read", "risk": "RiskLow", "read": ["id", "display_name"],
             "optional": ["name", "url", "mimetype", "file_size", "checksum", "public"], "label": "Website media", "domain": media_domain})
    for verb, mode in (("create", "WriteModeCreate"), ("update", "WriteModeUpdate")):
        add({"id": f"website.media.{verb}", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "ir.attachment",
             "module": "website", "capability": "website.media.write", "risk": "RiskMedium", "mode": mode,
             "allowed": ["name", "url", "datas", "mimetype"], "relations": {}, "label": "Website media",
             "types": {}, "enums": {}, "domain": media_domain, "forced": media_forced,
             "prefixes": {"url": WEBSITE_MEDIA_PREFIX}})
    report["models"].append({"model": "ir.attachment", "scope": WEBSITE_MEDIA_PREFIX,
                             "operations": [f"website.media.{v}" for v in ("list", "describe", "create", "update")]})

    # Settings (SETTINGS_FIELDS).
    settings = sorted(SETTINGS_FIELDS)
    add({"id": "structure.settings.describe", "kind": "KindRead", "strategy": "StrategyRead", "model": "res.config.settings",
         "module": "base", "capability": "structure.settings.read", "risk": "RiskLow", "read": [], "optional": settings,
         "label": "Settings", "new_record": True})
    add({"id": "structure.settings.update", "kind": "KindAction", "strategy": "StrategyWizard", "model": "res.config.settings",
         "module": "base", "capability": "structure.settings.write", "risk": "RiskHigh", "wizard": "res.config.settings",
         "wizard_method": "execute", "link": "", "link_multi": False, "wizard_allowed": settings, "wizard_values": {},
         "types": {f: t for f, t in SETTINGS_FIELDS.items() if isinstance(t, str)},
         "enums": {f: list(t) for f, t in SETTINGS_FIELDS.items() if isinstance(t, tuple)},
         "context": {}, "context_target": {}, "label": "Settings", "button": "Save", "standalone": True})
    report["models"].append({"model": "res.config.settings", "settings": settings,
                             "operations": ["structure.settings.describe", "structure.settings.update"]})

    # Guarded deletes (GUARDED_DELETES).
    for op_id, (model, module, impacts) in sorted(GUARDED_DELETES.items()):
        family, entity, _ = op_id.split(".")
        add({"id": op_id, "kind": "KindCommand", "strategy": "StrategyDestructiveGuarded", "model": model,
             "module": module, "capability": capability(family, entity, "delete"), "risk": "RiskHigh",
             "label": by_model.get(model, {}).get("name", model), "impacts": list(impacts),
             "domain": media_domain if model == "ir.attachment" else None})
        report["models"].append({"model": model, "guarded_delete": op_id,
                                 "dependents": [f"{m}.{f}" for m, f in impacts]})

    # Manual journal entries (ENTRY_DOMAIN).
    for verb in ("list", "describe"):
        add({"id": f"finance.journal_entry.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": "account.move",
             "module": "account", "capability": "finance.journalentry.read", "risk": "RiskLow", "read": ["id", "display_name"],
             "optional": ["name", "date", "ref", "journal_id", "state", "amount_total", "partner_id", "company_id"],
             "label": "Journal entry", "domain": ENTRY_DOMAIN})
    entry_relations = {"journal_id": {"model": "account.journal", "search": "name"}}
    add({"id": "finance.journal_entry.create", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "account.move",
         "module": "account", "capability": "finance.journalentry.write", "risk": "RiskMedium", "mode": "WriteModeCreate",
         "allowed": ["date", "ref", "journal_id", "line_ids"], "relations": entry_relations, "label": "Journal entry",
         "types": {"line_ids": "json"}, "enums": {}, "forced": {"move_type": "entry"},
         "lines": {"line_ids": {"allowed": ["account_id", "name", "debit", "credit", "partner_id", "date_maturity"],
                                "types": {"debit": "number", "credit": "number"},
                                "relations": {"account_id": {"model": "account.account", "search": "code", "by_name": True},
                                              "partner_id": {"model": "res.partner", "search": "name"}}}}})
    add({"id": "finance.journal_entry.update", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "account.move",
         "module": "account", "capability": "finance.journalentry.write", "risk": "RiskMedium", "mode": "WriteModeUpdate",
         "allowed": ["date", "ref", "journal_id"], "relations": entry_relations, "label": "Journal entry",
         "types": {}, "enums": {}, "domain": ENTRY_DOMAIN})
    for verb, method, risk in (("post", "action_post", "RiskHigh"), ("reset_to_draft", "button_draft", "RiskMedium")):
        add({"id": f"finance.journal_entry.{verb}", "kind": "KindAction", "strategy": "StrategyMethodCall", "model": "account.move",
             "module": "account", "capability": "finance.journalentry.write", "risk": risk, "method": method,
             "label": "Journal entry", "domain": ENTRY_DOMAIN})

    # Inventory counts (COUNT_DOMAIN, COUNTED_DOMAIN).
    add({"id": "inventory.count.list", "kind": "KindRead", "strategy": "StrategyRead", "model": "stock.quant",
         "module": "stock", "capability": "inventory.count.read", "risk": "RiskLow", "read": ["id", "display_name"],
         "optional": ["product_id", "location_id", "lot_id", "quantity", "inventory_quantity", "inventory_diff_quantity",
                      "inventory_quantity_set", "inventory_date", "user_id"],
         "label": "Inventory count", "domain": COUNTED_DOMAIN})
    add({"id": "inventory.count.set", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "stock.quant",
         "module": "stock", "capability": "inventory.count.write", "risk": "RiskMedium", "mode": "WriteModeCreate",
         "allowed": ["product_id", "location_id", "lot_id", "inventory_quantity"],
         "relations": {"product_id": {"model": "product.product", "search": "name"},
                       "location_id": {"model": "stock.location", "search": "complete_name", "domain": [["usage", "=", "internal"]]},
                       "lot_id": {"model": "stock.lot", "search": "name"}},
         "label": "Inventory count", "types": {"inventory_quantity": "number"}, "enums": {},
         "write_context": {"inventory_mode": True}})
    for verb, method, risk in (("apply", "action_apply_inventory", "RiskHigh"), ("clear", "action_clear_inventory_quantity", "RiskLow")):
        add({"id": f"inventory.count.{verb}", "kind": "KindAction", "strategy": "StrategyMethodCall", "model": "stock.quant",
             "module": "stock", "capability": "inventory.count.write", "risk": risk, "method": method,
             "label": "Inventory count", "domain": COUNTED_DOMAIN})
    report["models"].append({"model": "account.move", "scope": "journal entries", "operations": [o["id"] for o in operations if o["id"].startswith("finance.journal_entry.")]})
    report["models"].append({"model": "stock.quant", "scope": "inventory counts", "operations": [o["id"] for o in operations if o["id"].startswith("inventory.count.")]})

    # Deployment system parameters (SYSTEM_PARAMETER_KEYS).
    parameter_domain = [["key", "in", list(SYSTEM_PARAMETER_KEYS)]]
    for verb in ("list", "describe"):
        add({"id": f"structure.system_parameter.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": "ir.config_parameter",
             "module": "base", "capability": "structure.systemparameter.read", "risk": "RiskLow", "read": ["id", "display_name"],
             "optional": ["key", "value"], "label": "System parameter", "domain": parameter_domain})
    add({"id": "structure.system_parameter.create", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "ir.config_parameter",
         "module": "base", "capability": "structure.systemparameter.write", "risk": "RiskHigh", "mode": "WriteModeCreate",
         "allowed": ["key", "value"], "relations": {}, "label": "System parameter", "types": {},
         "enums": {"key": list(SYSTEM_PARAMETER_KEYS)}, "domain": parameter_domain})
    add({"id": "structure.system_parameter.update", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "ir.config_parameter",
         "module": "base", "capability": "structure.systemparameter.write", "risk": "RiskHigh", "mode": "WriteModeUpdate",
         "allowed": ["value"], "relations": {}, "label": "System parameter", "types": {}, "enums": {}, "domain": parameter_domain})
    report["models"].append({"model": "ir.config_parameter", "scope": list(SYSTEM_PARAMETER_KEYS),
                             "operations": [f"structure.system_parameter.{v}" for v in ("list", "describe", "create", "update")]})

    # Printed reports (PRINTS).
    for op_id, (model, module, report_name, domain) in sorted(PRINTS.items()):
        family, entity, _ = op_id.split(".")
        add({"id": op_id, "kind": "KindRead", "strategy": "StrategyReport", "model": model, "module": module,
             "capability": capability(family, entity, "read"), "risk": "RiskLow", "report": report_name,
             "label": by_model.get(model, {}).get("name", model), "domain": domain})
        report["models"].append({"model": model, "print": op_id, "report": report_name})

    # Notes, activities and files on the records a business talks about (COLLABORATION).
    for base, (model, module, domain) in sorted(COLLABORATION.items()):
        family, entity = base.split(".", 1)
        write = capability(family, entity, "write")
        add({"id": f"{base}.log_note", "kind": "KindAction", "strategy": "StrategyMethodCall", "model": model, "module": module,
             "capability": write, "risk": "RiskLow", "method": "message_post", "label": by_model.get(model, {}).get("name", model),
             "domain": domain, "kwargs": {"message_type": "comment", "subtype_xmlid": "mail.mt_note"},
             "inputs": ["body"], "required": ["body"], "types": {}, "relations": {}})
        add({"id": f"{base}.schedule_activity", "kind": "KindAction", "strategy": "StrategyMethodCall", "model": model, "module": module,
             "capability": write, "risk": "RiskLow", "method": "activity_schedule", "label": by_model.get(model, {}).get("name", model),
             "domain": domain, "kwargs": {"act_type_xmlid": "mail.mail_activity_data_todo"},
             "inputs": ["summary", "note", "date_deadline", "user_id"], "required": ["summary", "date_deadline"], "types": {"user_id": "reference"},
             "relations": {"user_id": {"model": "res.users", "search": "name"}}})
        files = [["res_model", "=", model], ["type", "=", "binary"]]
        for verb, optional in (("list", ["name", "mimetype", "file_size", "res_id", "create_date", "checksum"]),
                               ("describe", ["name", "mimetype", "file_size", "res_id", "create_date", "checksum", "datas"])):
            add({"id": f"{base}.attachment.{verb}", "kind": "KindRead", "strategy": "StrategyRead", "model": "ir.attachment",
                 "module": module, "capability": capability(family, entity + ".attachment", "read"), "risk": "RiskLow",
                 "read": ["id", "display_name"], "optional": optional, "label": "Attachment", "domain": files})
        add({"id": f"{base}.attachment.create", "kind": "KindCommand", "strategy": "StrategyFieldWrite", "model": "ir.attachment",
             "module": module, "capability": capability(family, entity + ".attachment", "write"), "risk": "RiskMedium",
             "mode": "WriteModeCreate", "allowed": ["res_id", "name", "datas", "mimetype"], "label": "Attachment",
             "relations": {"res_id": {"model": model, "search": "", "domain": domain}}, "types": {"res_id": "reference"}, "enums": {},
             "forced": {"res_model": model, "type": "binary"}, "domain": files})
        report["models"].append({"model": model, "collaboration": base,
                                 "operations": [f"{base}.{v}" for v in ("log_note", "schedule_activity", "attachment.list", "attachment.describe", "attachment.create")]})

    for model in sorted(by_model):
        record = by_model[model]
        views = record.get("views", {})
        form, listing = views.get("form", {}), views.get("list", {})
        if not form.get("own_fields") or not listing.get("own_fields"):
            continue
        if model in SKIP or model in curated_models:
            continue
        family = family_of(model)
        if not family:
            raise SystemExit(f"no family for {model}: add it to FAMILY or SKIP")
        entity = entity_of(model)
        fields = record["fields"]
        module = record["defining_module"]
        label = record["name"]
        base = f"{family}.{entity}"
        has_state = "state" in fields
        entry = {"model": model, "operations": []}

        made, projections, allowed = standard_operations(model, record, base, ("list", "describe", "create", "update"),
                                                         capability(family, entity, "read"), capability(family, entity, "write"))
        list_fields, describe_fields = projections["list"], projections["describe"]
        for op in made:
            add(op)
            entry["operations"].append(op["id"])

        seen = {"list", "describe", "create", "update"}
        for verb, method, button_label in business_buttons(model, views, seen):
            risk = "RiskHigh" if any(word in verb for word in HIGH_RISK_VERBS) else "RiskMedium"
            add({"id": f"{base}.{verb}", "kind": "KindAction", "strategy": "StrategyMethodCall",
                 "model": model, "module": module, "capability": capability(family, entity, "write"),
                 "risk": risk, "method": method, "label": label, "button": button_label})
            entry["operations"].append(f"{base}.{verb}")
        for op in wizard_operations(model, record, base, capability(family, entity, "write"), seen):
            add(op)
            entry["operations"].append(op["id"])
        for op in opened_operations(model, record, base, capability(family, entity, "write"), seen):
            add(op)
            entry["operations"].append(op["id"])

        if following is not None:
            nxt = next_models.get(model)
            entry["next_series"] = "MODEL_ABSENT" if nxt is None else {
                "missing_fields": sorted(set(list_fields + describe_fields + allowed) - set(nxt["fields"])),
                "missing_methods": sorted({b["name"] for b in form.get("buttons", []) if b["type"] == "object"}
                                          - {b["name"] for b in nxt.get("views", {}).get("form", {}).get("buttons", [])}),
            }
        report["models"].append(entry)

    # Every dialog a METHOD_CALL can answer with must be reviewed in provider_dialog.go (asked before
    # the method acts, or offered after): the executor reports any other without claiming either.
    # A method that opens a form in a dialog is navigation and belongs in SKIP_METHODS.
    reviewed = open("packages/odoo/provider_dialog.go", encoding="utf-8").read()
    method_calls = {(o["model"], o["method"]): o["id"] for o in operations if o["strategy"] == "StrategyMethodCall"}
    resolvers = {}
    for op in operations:
        if op.get("opener"):
            for dialog in [op["wizard"], *op.get("also", {})]:
                resolvers.setdefault((op["model"], dialog), []).append(op["id"])
    report["dialogs"], problems = [], []
    for model, method in sorted(set(method_calls) | curated_methods):
        for dialog, where in sorted(dialog_scan["buttons"].get(f"{model}|{method}", {}).get("dialogs", {}).items()):
            if not where["transient"]:
                problems.append(f"{model}.{method} opens a {dialog} form (SKIP_METHODS)")
            elif f'"{dialog}"' not in reviewed:
                problems.append(f"{model}.{method} -> {dialog} (provider_dialog.go)")
            report["dialogs"].append({"operation": method_calls.get((model, method), f"curated {model}.{method}"),
                                      "dialog": dialog, "via": where["via"], "module": where["module"],
                                      "resolveWith": sorted(resolvers.get((model, dialog), []))})
    if problems:
        raise SystemExit("unreviewed dialogs: " + "; ".join(problems))

    with open(OUTPUT, "w", encoding="utf-8") as out:
        out.write(render(operations, inventory["provider"]))
    report["operations"] = len(operations)
    report["by_strategy"] = {s: sum(1 for o in operations if o["strategy"] == s) for s in
                             ("StrategyRead", "StrategyFieldWrite", "StrategyMethodCall", "StrategyWizard",
                              "StrategyDestructiveGuarded", "StrategyReport")}
    with open(options.report, "w", encoding="utf-8") as out:
        json.dump(report, out, ensure_ascii=False, indent=1, sort_keys=True)
    print(f"{len(operations)} surface operations over {len(report['models'])} models -> {OUTPUT}", file=sys.stderr)


def go_value(value):
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, list):
        return "[]interface{}{" + ", ".join(go_value(v) for v in value) + "}"
    return json.dumps(value)


def render_typing(lines, op):
    if op.get("types"):
        lines.append("\t\tInputTypes: map[string]string{" + ", ".join(
            f"{json.dumps(k)}: {json.dumps(v)}" for k, v in sorted(op["types"].items())) + "},")
    if op.get("enums"):
        lines.append("\t\tInputEnums: map[string][]string{" + ", ".join(
            f"{json.dumps(k)}: {go_strings(v)}" for k, v in sorted(op["enums"].items())) + "},")


def render_relation(rel):
    search = f", SearchField: {json.dumps(rel['search'])}" if rel.get("search") else ""
    domain = (", Domain: []interface{}{" + ", ".join(go_value(c) for c in rel["domain"]) + "}") if rel.get("domain") else ""
    by_name = ", ByName: true" if rel.get("by_name") else ""
    return f"{{TargetModel: {json.dumps(rel['model'])}{search}{domain}{by_name}}}"


def render(operations, provider):
    lines = [
        "// Code generated by scripts/dev/odoo-catalog-surface.py. DO NOT EDIT.",
        "//",
        f"// Source: the business surface of Odoo {provider['server_version']} (list and form views and",
        "// their business buttons, read from a live instance by scripts/dev/odoo-surface-inventory.py).",
        "// These operations are SURFACE_GENERATED: standalone-only, UNVERIFIED until a conformance run",
        "// promotes them, and never part of the governed catalog. See surface_catalog.go.",
        "",
        "package odoo",
        "",
        "var odoo19SurfaceCatalog = []Odoo19Operation{",
    ]
    for op in operations:
        comment = f"// {op['model']} ({op['label']})"
        if op.get("method"):
            comment += f": {op['method']}" + (f" \"{op['button']}\"" if op.get("button") else "")
        elif op.get("opener"):
            comment += f": {op['opener']}, then dialog {op['wizard']}.{op['wizard_method']} \"{op['button']}\""
        elif op.get("wizard"):
            comment += f": wizard {op['wizard']}.{op['wizard_method']} \"{op['button']}\""
        lines.append("\t" + comment)
        lines.append("\t{")
        lines.append(f"\t\tSemanticID: {json.dumps(op['id'])}, Kind: {op['kind']}, Strategy: {op['strategy']},")
        lines.append(f"\t\tRequiredModules: {go_strings([op['module']])}, RequiredCapability: {json.dumps(op['capability'])},")
        lines.append(f"\t\tRisk: {op['risk']}, Confidence: \"SURFACE_DISCOVERED\", ProviderModel: {json.dumps(op['model'])},")
        if op.get("domain"):
            lines.append("\t\tProviderDomain: []interface{}{" + ", ".join(go_value(clause) for clause in op["domain"]) + "},")
        if op["strategy"] == "StrategyRead":
            if op["read"]:
                lines.append(f"\t\tReadFields: {go_strings(op['read'])},")
            if op["optional"]:
                lines.append(f"\t\tOptionalReadFields: {go_strings(op['optional'])},")
            if op.get("new_record"):
                lines.append("\t\tReadNewRecord: true,")
        elif op["strategy"] == "StrategyWizard":
            lines.append(f"\t\tWizardModel: {json.dumps(op['wizard'])}, WizardMethod: {json.dumps(op['wizard_method'])},")
            if op["link"]:
                lines.append(f"\t\tWizardLinkField: {json.dumps(op['link'])}, WizardLinkMulti: {'true' if op['link_multi'] else 'false'},")
            if op.get("opener"):
                # The dialog brings its own context: the active one does not apply.
                lines.append(f"\t\tWizardOpenerMethod: {json.dumps(op['opener'])}, WizardOpenerActs: {'true' if op['opener_acts'] else 'false'},")
                if op.get("also"):
                    lines.append("\t\tWizardAlsoAnswers: map[string]string{" + ", ".join(
                        f"{json.dumps(k)}: {json.dumps(v)}" for k, v in sorted(op["also"].items())) + "},")
            elif op.get("standalone"):
                lines.append("\t\tWizardStandalone: true,")
            else:
                lines.append("\t\tWizardActiveContext: true,")
            if op.get("wizard_values"):
                lines.append("\t\tWizardValues: map[string]interface{}{" + ", ".join(
                    f"{json.dumps(k)}: {go_value(v)}" for k, v in sorted(op["wizard_values"].items())) + "},")
            if op["wizard_allowed"]:
                lines.append(f"\t\tWizardAllowedFields: {go_strings(op['wizard_allowed'])},")
            if op.get("context"):
                lines.append("\t\tWizardContext: map[string]interface{}{" + ", ".join(
                    f"{json.dumps(k)}: {go_value(v)}" for k, v in sorted(op["context"].items())) + "},")
            if op.get("context_target"):
                lines.append("\t\tWizardContextTarget: map[string]string{" + ", ".join(
                    f"{json.dumps(k)}: {json.dumps(v)}" for k, v in sorted(op["context_target"].items())) + "},")
            render_typing(lines, op)
        elif op["strategy"] == "StrategyFieldWrite":
            lines.append(f"\t\tWriteMode: {op['mode']}, AllowedFields: {go_strings(op['allowed'])},")
            if op.get("forced"):
                lines.append("\t\tForcedValues: map[string]interface{}{" + ", ".join(
                    f"{json.dumps(k)}: {go_value(v)}" for k, v in sorted(op["forced"].items())) + "},")
            if op.get("prefixes"):
                lines.append("\t\tInputPrefixes: map[string]string{" + ", ".join(
                    f"{json.dumps(k)}: {json.dumps(v)}" for k, v in sorted(op["prefixes"].items())) + "},")
            render_typing(lines, op)
            if op["relations"]:
                lines.append("\t\tRelations: map[string]RelationSpec{")
                for field in sorted(op["relations"]):
                    rel = op["relations"][field]
                    lines.append(f"\t\t\t{json.dumps(field)}: {render_relation(rel)},")
                lines.append("\t\t},")
            if op.get("write_context"):
                lines.append("\t\tWriteContext: map[string]interface{}{" + ", ".join(
                    f"{json.dumps(k)}: {go_value(v)}" for k, v in sorted(op["write_context"].items())) + "},")
            if op.get("lines"):
                lines.append("\t\tLineInputs: map[string]LineSpec{")
                for field, spec in sorted(op["lines"].items()):
                    lines.append(f"\t\t\t{json.dumps(field)}: {{AllowedFields: {go_strings(spec['allowed'])},")
                    if spec.get("types"):
                        lines.append("\t\t\t\tInputTypes: map[string]string{" + ", ".join(
                            f"{json.dumps(k)}: {json.dumps(v)}" for k, v in sorted(spec["types"].items())) + "},")
                    lines.append("\t\t\t\tRelations: map[string]RelationSpec{" + ", ".join(
                        f"{json.dumps(k)}: {render_relation(v)}" for k, v in sorted(spec["relations"].items())) + "}},")
                lines.append("\t\t},")
        elif op["strategy"] == "StrategyReport":
            lines.append(f"\t\tReportName: {json.dumps(op['report'])},")
        elif op["strategy"] == "StrategyDestructiveGuarded":
            if op["impacts"]:
                (model, field), rest = op["impacts"][0], op["impacts"][1:]
                lines.append(f"\t\tImpactModel: {json.dumps(model)}, ImpactField: {json.dumps(field)},")
                if rest:
                    lines.append("\t\tAdditionalImpacts: []ImpactSpec{" + ", ".join(
                        f"{{Model: {json.dumps(m)}, Field: {json.dumps(f)}}}" for m, f in rest) + "},")
        else:
            lines.append(f"\t\tMethod: {json.dumps(op['method'])},")
            if op.get("kwargs"):
                lines.append("\t\tMethodKwargs: map[string]interface{}{" + ", ".join(
                    f"{json.dumps(k)}: {go_value(v)}" for k, v in sorted(op["kwargs"].items())) + "},")
            if op.get("inputs"):
                lines.append(f"\t\tMethodInputs: {go_strings(op['inputs'])},")
            if op.get("required"):
                lines.append(f"\t\tMethodRequiredInputs: {go_strings(op['required'])},")
            render_typing(lines, op)
            if op.get("relations"):
                lines.append("\t\tRelations: map[string]RelationSpec{" + ", ".join(
                    f"{json.dumps(k)}: {render_relation(v)}" for k, v in sorted(op["relations"].items())) + "},")
        lines.append("\t},")
    lines.append("}")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    main()
