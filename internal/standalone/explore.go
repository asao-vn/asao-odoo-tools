package standalone

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

// The explore commands read ANY model of the Odoo, not only the catalog's: what Odoo 20 Enterprise's
// own MCP server offers (models, fields, search, grouped reads), on Community as on Enterprise, and on
// the models an Enterprise app or a customisation adds. They read and never write, and they are not
// part of the verified catalog: their answer is Odoo's own, unchecked by conformance, and says so.
//
// What they never show: models that hold credentials (system parameters, mail servers, payment
// providers, API keys, certificates, online bank links), fields whose names mark a secret, and binary
// fields (files and images, read through a print or an attachment operation instead).

const (
	exploreDefaultLimit = 20
	exploreMaxLimit     = 200
	// exploreDefaultFields bounds the fields a search returns when none are asked for.
	exploreDefaultFields = 25
)

var exploreSecretModelPrefixes = []string{
	"ir.config_parameter", "ir.mail_server", "fetchmail.server", "res.users.apikeys", "res.users.identitycheck",
	"res.users.deletion", "payment.provider", "auth.oauth.provider", "auth_totp", "iap.account", "certificate.",
	"account.online.", "ir.attachment", "ir.module.module", "ir.cron", "ir.actions", "ir.rule", "ir.model.access",
	// Odoo 20 merges ir.model.access and ir.rule into ir.access: the same access configuration, refused
	// the same way.
	"ir.access",
}

var exploreSecretFieldWords = []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "totp", "credential"}

func exploreSecretModel(model string) bool {
	for _, prefix := range exploreSecretModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func exploreSecretField(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range exploreSecretFieldWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func exploreGroup(config programConfig) *clicore.Command {
	modelFlag := clicore.Flag{Name: "model", Kind: clicore.KindString, Required: true, Description: "The Odoo model, e.g. res.partner or planning.slot."}
	filterFlag := clicore.Flag{Name: "filter", Kind: clicore.KindJSON, Description: "Odoo search domain, as a JSON array, e.g. [[\"state\",\"=\",\"done\"]]."}
	readOnly := func(command *clicore.Command) *clicore.Command {
		command.SideEffect = clicore.EffectRead
		command.Authority = localAuthority
		command.AgentAccess = clicore.AgentAccessRead
		command.Flags = append([]clicore.Flag{profileFlag}, command.Flags...)
		for index := range command.Flags {
			if command.Flags[index].Name == "profile" {
				command.Flags[index].PersonOnly = true
			}
		}
		return local(command)
	}
	models := readOnly(&clicore.Command{
		Name:    "models",
		Summary: "List the Odoo's business models, any app's, not only the catalog's.",
		Description: `Lists the models the profile's login can read (transient and credential-holding ones left out), with
their names, matching --search in the technical or the human name. Generic and unverified: the catalog's
operations are the verified way to act on a model.`,
		Flags: []clicore.Flag{
			{Name: "search", Kind: clicore.KindString, Description: "Words to look for in the model's technical or human name, e.g. \"appointment\"."},
			{Name: "limit", Kind: clicore.KindInt, Description: "At most this many models (default 200)."},
		},
		Examples: []string{config.name + " explore models --search planning --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			domain := []interface{}{[]interface{}{"transient", "=", false}}
			if words := strings.TrimSpace(invocation.String("search")); words != "" {
				domain = append(domain, "|", []interface{}{"model", "ilike", words}, []interface{}{"name", "ilike", words})
			}
			limit := clampLimit(invocation, 200, 1000)
			rows, err := exploreCall(ctx, invocation, config, "ir.model", "search_read", []interface{}{domain},
				map[string]interface{}{"fields": []string{"model", "name"}, "limit": limit, "order": "model"})
			if err != nil {
				return nil, err
			}
			out := []map[string]interface{}{}
			for _, row := range records(rows) {
				model, _ := row["model"].(string)
				if model == "" || exploreSecretModel(model) {
					continue
				}
				out = append(out, map[string]interface{}{"model": model, "name": row["name"]})
			}
			return exploreAnswer(map[string]interface{}{"models": out, "count": len(out)}), nil
		},
	})
	fields := readOnly(&clicore.Command{
		Name:        "fields",
		Summary:     "List a model's fields: name, label, type, relation, and whether it is stored and required.",
		Description: `What can be filtered, grouped and read on a model. Secret and binary fields are not listed.`,
		Flags:       []clicore.Flag{modelFlag},
		Examples:    []string{config.name + " explore fields --model planning.slot --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			model, err := exploreModel(invocation)
			if err != nil {
				return nil, err
			}
			described, err := exploreFields(ctx, invocation, config, model)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(described))
			for name := range described {
				names = append(names, name)
			}
			sort.Strings(names)
			out := make([]map[string]interface{}, 0, len(names))
			for _, name := range names {
				spec := described[name]
				entry := map[string]interface{}{"name": name, "label": spec["string"], "type": spec["type"]}
				for _, key := range []string{"relation", "store", "required", "readonly", "selection"} {
					if value, ok := spec[key]; ok && value != nil && value != false {
						entry[key] = value
					}
				}
				out = append(out, entry)
			}
			return exploreAnswer(map[string]interface{}{"model": model, "fields": out, "count": len(out)}), nil
		},
	})
	search := readOnly(&clicore.Command{
		Name:    "search",
		Summary: "Read the records of any model that match a filter.",
		Description: fmt.Sprintf(`Reads records of any model. Without --fields it returns the record's id, its name and up to %d of its
stored plain fields. At most %d records (default %d). Secret and binary fields are never returned.`,
			exploreDefaultFields, exploreMaxLimit, exploreDefaultLimit),
		Flags: []clicore.Flag{modelFlag, filterFlag,
			{Name: "fields", Kind: clicore.KindString, Repeatable: true, Description: "Fields to return (see explore fields)."},
			{Name: "limit", Kind: clicore.KindInt, Description: fmt.Sprintf("At most this many records (default %d, at most %d).", exploreDefaultLimit, exploreMaxLimit)},
			{Name: "offset", Kind: clicore.KindInt, Description: "Skip this many records first."},
			{Name: "order", Kind: clicore.KindString, Description: "Sort, e.g. \"date desc, id\"."},
		},
		Examples: []string{config.name + ` explore search --model planning.slot --filter '[["state","=","published"]]' --limit 5 --output json`},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			model, err := exploreModel(invocation)
			if err != nil {
				return nil, err
			}
			described, err := exploreFields(ctx, invocation, config, model)
			if err != nil {
				return nil, err
			}
			wanted, err := exploreProjection(invocation, described)
			if err != nil {
				return nil, err
			}
			domain, err := exploreDomain(invocation)
			if err != nil {
				return nil, err
			}
			kwargs := map[string]interface{}{"fields": wanted, "limit": clampLimit(invocation, exploreDefaultLimit, exploreMaxLimit)}
			if offset, _ := invocation.Int("offset", 0); offset > 0 {
				kwargs["offset"] = offset
			}
			if order := strings.TrimSpace(invocation.String("order")); order != "" {
				kwargs["order"] = order
			}
			rows, err := exploreCall(ctx, invocation, config, model, "search_read", []interface{}{domain}, kwargs)
			if err != nil {
				return nil, err
			}
			found := records(rows)
			return exploreAnswer(map[string]interface{}{"model": model, "records": found, "count": len(found), "fields": wanted}), nil
		},
	})
	group := readOnly(&clicore.Command{
		Name:    "group",
		Summary: "Group any model's records and total them, computed by Odoo.",
		Description: `Groups by fields or date buckets ("date:month") and aggregates ("amount_total:sum"), as Odoo's own
pivot does; every group carries its count. Secret fields cannot be grouped or aggregated.`,
		Flags: []clicore.Flag{modelFlag, filterFlag,
			{Name: "group-by", Kind: clicore.KindString, Repeatable: true, Required: true, Description: "A field or a date bucket, e.g. stage_id or create_date:month."},
			{Name: "aggregate", Kind: clicore.KindString, Repeatable: true, Description: "field:sum, field:avg, field:max, field:min or field:count_distinct."},
			{Name: "limit", Kind: clicore.KindInt, Description: fmt.Sprintf("At most this many groups (default %d, at most %d).", exploreMaxLimit, exploreMaxLimit)},
		},
		Examples: []string{config.name + ` explore group --model helpdesk.ticket --group-by stage_id --aggregate priority:count_distinct --output json`},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			model, err := exploreModel(invocation)
			if err != nil {
				return nil, err
			}
			groupBy := invocation.StringSlice("group-by")
			aggregates := invocation.StringSlice("aggregate")
			for _, spec := range append(append([]string{}, groupBy...), aggregates...) {
				field, _, _ := strings.Cut(spec, ":")
				if exploreSecretField(field) {
					return nil, clicore.UsageError("EXPLORE_SECRET_FIELD", "%s holds a secret and cannot be grouped or aggregated", field)
				}
			}
			domain, err := exploreDomain(invocation)
			if err != nil {
				return nil, err
			}
			rows, err := exploreCall(ctx, invocation, config, model, "formatted_read_group", []interface{}{},
				map[string]interface{}{"domain": domain, "groupby": groupBy, "aggregates": append(aggregates, "__count"),
					"limit": clampLimit(invocation, exploreMaxLimit, exploreMaxLimit)})
			if err != nil {
				return nil, err
			}
			found := records(rows)
			return exploreAnswer(map[string]interface{}{"model": model, "groups": found, "count": len(found)}), nil
		},
	})
	return &clicore.Command{
		Name:     "explore",
		Summary:  "Read any model of the Odoo, not only the catalog's: models, fields, records and groups. Generic and unverified.",
		Children: []*clicore.Command{models, fields, search, group},
	}
}

func exploreAnswer(data map[string]interface{}) map[string]interface{} {
	data["generic"] = true
	data["note"] = "a generic read outside the verified catalog: Odoo's own answer, not checked by conformance"
	return data
}

func exploreModel(invocation *clicore.Invocation) (string, error) {
	model := strings.TrimSpace(invocation.String("model"))
	if model == "" {
		return "", clicore.UsageError("MISSING_REQUIRED_FLAG", "--model is required").WithContextValue("flag", "model")
	}
	if exploreSecretModel(model) {
		return "", clicore.NewError(clicore.CategoryDenied, "EXPLORE_MODEL_REFUSED",
			fmt.Sprintf("%s holds credentials or system configuration and is not explored", model)).
			WithContextValue("model", model)
	}
	return model, nil
}

func exploreDomain(invocation *clicore.Invocation) ([]interface{}, error) {
	domain := []interface{}{}
	if invocation.Has("filter") {
		if err := invocation.JSON("filter", &domain); err != nil {
			return nil, err
		}
	}
	// A filter on a secret field would reveal it one guess at a time.
	var check func(items []interface{}) error
	check = func(items []interface{}) error {
		for _, item := range items {
			leaf, isLeaf := item.([]interface{})
			if !isLeaf || len(leaf) == 0 {
				continue
			}
			if name, isName := leaf[0].(string); isName {
				field, _, _ := strings.Cut(name, ".")
				if exploreSecretField(name) || exploreSecretField(field) {
					return clicore.UsageError("EXPLORE_SECRET_FIELD", "%s holds a secret and cannot be filtered on", name)
				}
				continue
			}
			if err := check(leaf); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(domain); err != nil {
		return nil, err
	}
	return domain, nil
}

func exploreFields(ctx context.Context, invocation *clicore.Invocation, config programConfig, model string) (map[string]map[string]interface{}, error) {
	answer, err := exploreCall(ctx, invocation, config, model, "fields_get", []interface{}{},
		map[string]interface{}{"attributes": []string{"string", "type", "relation", "store", "required", "readonly", "selection"}})
	if err != nil {
		return nil, err
	}
	raw, _ := answer.(map[string]interface{})
	out := map[string]map[string]interface{}{}
	for name, value := range raw {
		spec, _ := value.(map[string]interface{})
		if spec == nil || exploreSecretField(name) || spec["type"] == "binary" {
			continue
		}
		out[name] = spec
	}
	return out, nil
}

// exploreProjection is the fields a search returns: the ones asked for (each one known, plain and not
// secret), or the id, the name and the first stored plain fields.
func exploreProjection(invocation *clicore.Invocation, described map[string]map[string]interface{}) ([]string, error) {
	var asked []string
	for _, value := range invocation.StringSlice("fields") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				asked = append(asked, name)
			}
		}
	}
	if len(asked) > 0 {
		for _, name := range asked {
			if _, known := described[name]; !known && name != "id" {
				return nil, clicore.UsageError("EXPLORE_UNKNOWN_FIELD", "%s is not a readable field of this model (secret and binary fields are not)", name).
					WithContextValue("field", name)
			}
		}
		return asked, nil
	}
	names := make([]string, 0, len(described))
	for name, spec := range described {
		if spec["store"] == true && spec["type"] != "one2many" && spec["type"] != "many2many" && spec["type"] != "html" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	wanted := []string{"id"}
	if _, ok := described["display_name"]; ok {
		wanted = append(wanted, "display_name")
	}
	for _, name := range names {
		if len(wanted) >= exploreDefaultFields+2 {
			break
		}
		if name != "id" && name != "display_name" {
			wanted = append(wanted, name)
		}
	}
	return wanted, nil
}

func clampLimit(invocation *clicore.Invocation, fallback, maximum int) int {
	limit, _ := invocation.Int("limit", 0)
	if limit <= 0 {
		return fallback
	}
	if limit > maximum {
		return maximum
	}
	return limit
}

func records(answer interface{}) []map[string]interface{} {
	list, _ := answer.([]interface{})
	out := make([]map[string]interface{}, 0, len(list))
	for _, item := range list {
		if row, ok := item.(map[string]interface{}); ok {
			for name := range row {
				if exploreSecretField(name) {
					delete(row, name)
				}
			}
			out = append(out, row)
		}
	}
	return out
}

// exploreCall runs one of the explore commands' fixed read calls against the profile's Odoo.
func exploreCall(ctx context.Context, invocation *clicore.Invocation, config programConfig, model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	material, err := localprofile.ResolveCredential(selection.Profile)
	if err != nil {
		return nil, classifyProfile(err, selection.Profile.Name, config.name)
	}
	executor, err := direct.NewExecutor(ctx, direct.Target{
		ProfileName: selection.Profile.Name,
		Endpoint:    selection.Profile.Endpoint,
		Database:    selection.Profile.Database,
		Login:       material.Login,
		Password:    material.Password,
		Protocol:    selection.Profile.Protocol,
	}, providerendpoint.CheckHost)
	if err != nil {
		return nil, classifyComposition(err, selection.Profile.Name, config.name)
	}
	// formatted_read_group reads, but is not on the raw call's read allowlist; the method here is one of
	// this file's three fixed reads, never the caller's.
	outcome, err := executor.Call(ctx, direct.RawRequest{Model: model, Method: method, Args: args, Kwargs: kwargs,
		AdmitCandidateSeries: true}, method == "formatted_read_group")
	if err != nil {
		return nil, classifyRawError(err, model, method, config.surface)
	}
	return outcome.Data, nil
}
