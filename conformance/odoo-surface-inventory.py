#!/usr/bin/env python3
"""Inventory the business surface an Odoo instance exposes, read from Odoo itself.

The catalog in packages/odoo is curated by hand. This tool is how its coverage is measured and
extended: it asks a live Odoo which business models its installed modules define, what their list
and form views show (the combined, inherited views the web client renders), what each field is,
and which business actions the form views offer: header buttons calling a model method
(type="object") and buttons opening a wizard (type="action"). Those buttons are Odoo's own
statement of its workflow transitions (confirm, post, validate, cancel, ...).

It reads only. The credential comes from an environment variable and is never printed or written.

    ODOO_INVENTORY_PASSWORD=... scripts/dev/odoo-surface-inventory.py \
        --endpoint http://127.0.0.1:58719 --database lab19 --login admin --output inventory-19.0.json
"""

import argparse
import json
import os
import sys
import urllib.request
import xml.etree.ElementTree as ET

# Models that are framework, configuration or plumbing rather than business records.
TECHNICAL_PREFIXES = (
    "ir.", "res.config", "res.groups", "res.users.", "res.device", "res.lang", "base.", "base_",
    "bus.", "mail.", "web_", "web.", "report.", "digest.", "auth_", "auth.", "iap.", "sms.",
    "snailmail.", "spreadsheet", "onboarding.", "discuss.", "privacy.", "rating.", "utm.",
    "theme.", "website.visitor", "website.track", "website.rewrite", "website.controller",
    "website.configurator", "website.base", "website.robots", "website.snippet", "website.custom",
    "website.seo", "website.route", "html_", "html.", "barcode.", "decimal.precision",
    "format.", "publisher_warranty", "portal.", "uom.category", "properties.", "fetchmail.",
    "account.report", "account.root", "account.code.mapping", "account.lock_exception",
)
TECHNICAL_SUFFIXES = (".report", ".mixin", ".wizard", ".line.report")


def rpc(endpoint, service, method, args):
    payload = json.dumps({"jsonrpc": "2.0", "method": "call", "id": 1,
                          "params": {"service": service, "method": method, "args": args}}).encode()
    request = urllib.request.Request(endpoint.rstrip("/") + "/jsonrpc", data=payload,
                                     headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=120) as response:
        answer = json.loads(response.read())
    if "error" in answer:
        data = answer["error"].get("data") or {}
        raise RuntimeError(data.get("message") or answer["error"].get("message"))
    return answer["result"]


class Odoo:
    def __init__(self, endpoint, database, login, password):
        self.endpoint, self.database, self.password = endpoint, database, password
        self.version = rpc(endpoint, "common", "version", [])
        self.uid = rpc(endpoint, "common", "login", [database, login, password])
        if not self.uid:
            raise SystemExit("login refused")

    def call(self, model, method, args, kwargs=None):
        return rpc(self.endpoint, "object", "execute_kw",
                   [self.database, self.uid, self.password, model, method, args, kwargs or {}])


def is_business(model, transient):
    if transient:
        return False
    if model.startswith(TECHNICAL_PREFIXES) or model.endswith(TECHNICAL_SUFFIXES):
        return False
    return True


def parse_view(arch):
    """Fields and buttons of one combined view arch."""
    if not arch:
        return {"fields": [], "buttons": []}
    root = ET.fromstring(arch)
    fields, buttons = [], []

    def own_elements(element):
        # Not those of an x2many field's embedded sub-views: a default form (one the model does not
        # declare) embeds the list view of every x2many field, header buttons of other models included.
        for child in element:
            yield child
            if not (child.tag == "field" and child.get("name")):
                yield from own_elements(child)

    for element in own_elements(root):
        if element.tag == "field" and element.get("name"):
            fields.append({
                "name": element.get("name"),
                "invisible": element.get("invisible") or element.get("column_invisible") or "",
                "readonly": element.get("readonly") or "",
                "required": element.get("required") or "",
            })
        elif element.tag == "button" and element.get("name") and element.get("type") in ("object", "action"):
            buttons.append({
                "name": element.get("name"),
                "type": element.get("type"),
                "string": element.get("string") or element.get("title") or "",
                "invisible": element.get("invisible") or "",
                "confirm": element.get("confirm") or "",
                "context": element.get("context") or "",
                "in_header": False,
            })
    header = root.find("header")
    if header is not None:
        header_names = {(b.get("name"), b.get("type")) for b in header.iter("button")}
        for button in buttons:
            if (button["name"], button["type"]) in header_names:
                button["in_header"] = True
    return {"fields": fields, "buttons": buttons}


def own_fields(arch):
    """Field names of the view's model itself, excluding fields of embedded sub-views."""
    if not arch:
        return []
    root = ET.fromstring(arch)
    names = []

    def walk(element, depth_in_subview):
        for child in element:
            if child.tag == "field" and child.get("name"):
                if not depth_in_subview:
                    names.append(child.get("name"))
                # An x2many field may embed list/form sub-views of another model.
                walk(child, True)
            else:
                walk(child, depth_in_subview)

    walk(root, False)
    seen, ordered = set(), []
    for name in names:
        if name not in seen:
            seen.add(name)
            ordered.append(name)
    return ordered


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--database", required=True)
    parser.add_argument("--login", required=True)
    parser.add_argument("--password-env", default="ODOO_INVENTORY_PASSWORD")
    parser.add_argument("--output", required=True)
    options = parser.parse_args()
    password = os.environ.get(options.password_env)
    if not password:
        raise SystemExit(f"set {options.password_env}")

    odoo = Odoo(options.endpoint, options.database, options.login, password)
    installed = sorted(r["name"] for r in odoo.call("ir.module.module", "search_read",
                                                     [[["state", "=", "installed"]]], {"fields": ["name"]}))
    models = odoo.call("ir.model", "search_read", [[["transient", "=", False]]],
                       {"fields": ["model", "name", "modules", "transient"], "order": "model"})
    # The module that DEFINES a model is the owner of its oldest ir.model xmlid; later modules
    # extending it add their own. That module is what an operation on the model requires.
    defining = {}
    for data in odoo.call("ir.model.data", "search_read", [[["model", "=", "ir.model"]]],
                          {"fields": ["module", "res_id"], "order": "id"}):
        defining.setdefault(data["res_id"], data["module"])
    model_ids = {m["model"]: m["id"] for m in models}
    action_cache = {}

    def action_target(name):
        # A type="action" button names an action by database id in the combined arch.
        if not name.isdigit():
            return {}
        if name not in action_cache:
            found = odoo.call("ir.actions.actions", "read", [[int(name)]], {"fields": ["type", "name"]})
            target = {}
            if found and found[0].get("type") == "ir.actions.act_window":
                window = odoo.call("ir.actions.act_window", "read", [[int(name)]], {"fields": ["res_model", "name", "target", "context"]})
                if window:
                    target = {"model": window[0]["res_model"], "name": window[0]["name"], "target": window[0]["target"],
                              "context": window[0].get("context") or ""}
            elif found:
                target = {"action_type": found[0].get("type"), "name": found[0].get("name")}
            action_cache[name] = target
        return action_cache[name]

    inventory = {"provider": {"server_version": odoo.version.get("server_version"),
                              "server_serie": odoo.version.get("server_serie")},
                 "installed_modules": installed, "models": []}
    skipped = 0
    for record in models:
        model = record["model"]
        if not is_business(model, record["transient"]):
            skipped += 1
            continue
        try:
            meta = odoo.call(model, "fields_get", [], {"attributes": [
                "type", "string", "required", "readonly", "store", "relation", "selection", "depends", "related"]})
        except RuntimeError as error:
            inventory["models"].append({"model": model, "error": f"fields_get: {error}"})
            continue
        views = {}
        try:
            got = odoo.call(model, "get_views", [[[False, "list"], [False, "form"]]], {})
            for kind in ("list", "form"):
                arch = (got.get("views") or {}).get(kind, {}).get("arch")
                views[kind] = {"own_fields": own_fields(arch), **parse_view(arch)}
        except RuntimeError as error:
            views["error"] = str(error)
        for button in views.get("form", {}).get("buttons", []):
            if button["type"] == "action":
                button["action"] = action_target(button["name"])
        fields = {}
        for name, attributes in meta.items():
            fields[name] = {
                "type": attributes.get("type"),
                "string": attributes.get("string"),
                "required": bool(attributes.get("required")),
                "readonly": bool(attributes.get("readonly")),
                "store": bool(attributes.get("store")),
                "relation": attributes.get("relation") or "",
                "selection": [value for value, _ in (attributes.get("selection") or [])] if isinstance(attributes.get("selection"), list) else [],
                "computed": bool(attributes.get("depends")) or bool(attributes.get("related")),
            }
        inventory["models"].append({
            "model": model, "name": record["name"], "modules": record["modules"],
            "defining_module": defining.get(model_ids[model], ""),
            "fields": fields, "views": views,
        })

    # Wizards the business forms open in a dialog (type="action", target "new"): what they ask for
    # and which buttons confirm them. Transient models are skipped above, so they are read here.
    wizard_models = sorted({b["action"]["model"] for m in inventory["models"] if "error" not in m
                            for b in m["views"].get("form", {}).get("buttons", [])
                            if b["type"] == "action" and (b.get("action") or {}).get("target") == "new"
                            and (b.get("action") or {}).get("model")})
    transient = {r["model"] for r in odoo.call("ir.model", "search_read",
                                               [[["model", "in", wizard_models], ["transient", "=", True]]],
                                               {"fields": ["model"]})}
    inventory["wizards"] = {}
    for wizard in wizard_models:
        if wizard not in transient:
            continue
        try:
            meta = odoo.call(wizard, "fields_get", [], {"attributes": [
                "type", "string", "required", "readonly", "store", "relation", "selection", "depends", "related"]})
            arch = odoo.call(wizard, "get_views", [[[False, "form"]]], {})["views"]["form"]["arch"]
        except RuntimeError as error:
            inventory["wizards"][wizard] = {"error": str(error)}
            continue
        inventory["wizards"][wizard] = {
            "fields": {name: {"type": a.get("type"), "string": a.get("string"), "required": bool(a.get("required")),
                              "readonly": bool(a.get("readonly")), "relation": a.get("relation") or "",
                              "computed": bool(a.get("depends")) or bool(a.get("related"))}
                       for name, a in meta.items()},
            "form": {"own_fields": own_fields(arch), **parse_view(arch)},
        }

    inventory["skipped_technical_models"] = skipped
    with open(options.output, "w", encoding="utf-8") as handle:
        json.dump(inventory, handle, ensure_ascii=False, indent=1, sort_keys=True)
    business = [m for m in inventory["models"] if "error" not in m]
    buttons = sum(len(m["views"].get("form", {}).get("buttons", [])) for m in business)
    print(f"{inventory['provider']['server_version']}: {len(business)} business models, "
          f"{buttons} form buttons, {skipped} technical models skipped -> {options.output}", file=sys.stderr)


if __name__ == "__main__":
    main()
