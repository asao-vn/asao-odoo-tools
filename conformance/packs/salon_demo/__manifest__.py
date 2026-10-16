# A small custom module that ships an odoo-cli pack (DEC-2026-0176): the live proof that a module's
# own operations, declared as data, run through odoo-cli like the catalog's. Apache-2.0, as the
# repository it lives in.
{
    "name": "Salon demo (odoo-cli pack)",
    "summary": "Bookings with buttons, a wizard and a report, and the odoo-cli pack that names them.",
    "version": "19.0.1.0.0",
    "license": "Other OSI approved licence",
    "depends": ["base", "web"],
    "data": [
        "security/ir.model.access.csv",
        "views/booking_views.xml",
        "report/booking_report.xml",
        "data/bookings.xml",
    ],
    "installable": True,
    "application": False,
}
