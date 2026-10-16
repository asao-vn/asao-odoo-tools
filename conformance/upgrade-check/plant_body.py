# Body of plant.py: SPEC and MODE are prepended. Runs inside `odoo shell`.
# For each customization in SPEC: create it as a person would, then use it once, each in a savepoint,
# and record what Odoo answered. MODE "plant" commits what was created; "replay" rolls everything back.
import ast
import json


def attempt(function):
    try:
        with env.cr.savepoint():
            return {"ok": True, "value": function()}
    except Exception as error:  # noqa: BLE001 - what Odoo answered is the result
        return {"ok": False, "error": (type(error).__name__ + ": " + str(error).strip().splitlines()[0])[:300]}


def model_id(name):
    return env["ir.model"]._get_id(name)


def sample(model):
    return env[model].search([], limit=1, order="id")


def create(item):
    kind, model = item["kind"], item["model"]
    if kind == "filter":
        return env["ir.filters"].create({"name": item["name"], "model_id": model, "domain": item.get("domain", "[]"),
                                         "context": item.get("context", "{}"), "sort": item.get("sort", "[]")}).id
    if kind in ("code", "automation"):
        action = env["ir.actions.server"].create({"name": item["name"], "model_id": model_id(model), "state": "code",
                                                  "code": item.get("code", "pass")})
        if kind == "code":
            return action.id
        values = {"name": item["name"], "model_id": model_id(model), "trigger": "on_create_or_write",
                  "filter_domain": item.get("domain", "[]"), "action_server_ids": [(6, 0, [action.id])]}
        return env["base.automation"].create(values).id
    if kind == "field":
        values = {"name": item["name"], "model_id": model_id(model), "ttype": item["ttype"],
                  "field_description": item["name"], "state": "manual"}
        if item.get("related"):
            values["related"] = item["related"]
        if item.get("relation"):
            values["relation"] = item["relation"]
        return env["ir.model.fields"].create(values).id
    if kind == "template":
        return env["mail.template"].create({"name": item["name"], "model_id": model_id(model), "subject": item.get("subject", "x"),
                                            "body_html": item["body"]}).id
    if kind == "view":
        parent = env.ref(item["parent"])
        return env["ir.ui.view"].create({"name": item["name"], "type": parent.type, "model": model, "inherit_id": parent.id,
                                         "mode": "extension", "arch_db": item["arch"]}).id
    if kind == "export":
        return env["ir.exports"].create({"name": item["name"], "resource": model,
                                         "export_fields": [(0, 0, {"name": path}) for path in item["paths"]]}).id
    if kind == "default":
        env["ir.default"].set(model, item["field"], item["value"])
        return item["field"]
    if kind == "rule":
        return env["ir.rule"].create({"name": item["name"], "model_id": model_id(model), "domain_force": item["domain"],
                                      "groups": [(4, env.ref("base.group_user").id)]}).id
    if kind == "access":
        return env["ir.model.access"].create({"name": item["name"], "model_id": model_id(model),
                                              "group_id": env.ref("base.group_user").id, "perm_read": True}).id
    if kind == "window_action":
        return env["ir.actions.act_window"].create({"name": item["name"], "res_model": model, "domain": item.get("domain", "[]"),
                                                    "context": item.get("context", "{}")}).id
    raise ValueError("unknown kind " + kind)


def use(item, created):
    kind, model = item["kind"], item["model"]
    if kind == "filter":
        domain = ast.literal_eval(item.get("domain", "[]"))
        count = env[model].search_count(domain)
        if json.loads(item.get("sort", "[]")):
            env[model].search(domain, order=", ".join(json.loads(item["sort"])), limit=5)
        context = ast.literal_eval(item.get("context", "{}"))
        if context.get("group_by"):
            groups = context["group_by"] if isinstance(context["group_by"], list) else [context["group_by"]]
            env[model]._read_group([], groupby=groups, aggregates=["__count"])
        return count
    if kind == "code":
        record = sample(model)
        env["ir.actions.server"].browse(created).with_context(active_model=model, active_id=record.id, active_ids=record.ids).run()
        return record.id
    if kind == "automation":
        record = sample(model)
        record.write(item["touch"])
        return record.id
    if kind == "field":
        record = sample(model)
        return str(record[item["name"]])[:60]
    if kind == "template":
        record = sample(model)
        template = env["mail.template"].browse(created)
        subject = template._render_field("subject", record.ids)
        rendered = template._render_field("body_html", record.ids)
        return (str(subject.get(record.id)) + " | " + str(rendered.get(record.id)))[:80]
    if kind == "view":
        view = env["ir.ui.view"].browse(created)
        return len(env[model].get_view(view.inherit_id.id)["arch"])
    if kind == "export":
        return env[model].search([], limit=1).export_data(item["paths"])["datas"][:1]
    if kind == "default":
        return env["ir.default"]._get(model, item["field"])
    if kind == "window_action":
        return env[model].search_count(ast.literal_eval(item.get("domain", "[]")))
    if kind in ("rule", "access"):
        user = env.ref("base.user_demo")
        return env[model].with_user(user).search_count([])
    raise ValueError(kind)


results = []
for item in SPEC:
    created = attempt(lambda: create(item))
    result = {"key": item["key"], "kind": item["kind"], "create": created}
    if created["ok"]:
        result["use"] = attempt(lambda: use(item, created["value"]))
    results.append(result)
if MODE == "plant":
    env.cr.commit()
else:
    env.cr.rollback()
print("RESULTS-BEGIN")
print(json.dumps(results, default=str, ensure_ascii=False))
print("RESULTS-END")
