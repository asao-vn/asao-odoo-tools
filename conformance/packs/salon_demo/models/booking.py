from odoo import fields, models
from odoo.exceptions import UserError


class SalonDemoBooking(models.Model):
    _name = "salon_demo.booking"
    _description = "Salon booking (demo)"
    _order = "start desc, id desc"

    name = fields.Char(required=True, default="Booking")
    partner_id = fields.Many2one("res.partner", string="Customer")
    start = fields.Datetime()
    seats = fields.Integer(default=1)
    vip = fields.Boolean()
    note = fields.Text()
    state = fields.Selection(
        [("draft", "Draft"), ("confirmed", "Confirmed"), ("done", "Done"), ("cancel", "Cancelled")],
        default="draft", readonly=True, required=True)

    def action_confirm(self):
        if any(booking.state != "draft" for booking in self):
            raise UserError("Only a draft booking can be confirmed.")
        self.write({"state": "confirmed"})
        return True

    def action_done(self):
        if any(booking.state != "confirmed" for booking in self):
            raise UserError("Only a confirmed booking can be done.")
        self.write({"state": "done"})
        return True

    def action_cancel(self):
        self.write({"state": "cancel"})
        return True


class SalonDemoReschedule(models.TransientModel):
    _name = "salon_demo.reschedule"
    _description = "Reschedule a booking (demo)"

    start = fields.Datetime(required=True)

    def action_apply(self):
        bookings = self.env["salon_demo.booking"].browse(self.env.context.get("active_ids") or [])
        bookings.write({"start": self.start})
        return {"type": "ir.actions.act_window_close"}
