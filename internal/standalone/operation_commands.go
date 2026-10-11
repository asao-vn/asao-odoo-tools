package standalone

import (
	"context"
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/aerpsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// ContractInputFlags derives the contract input flags from an operation.
func ContractInputFlags(op direct.Operation) []clicore.Flag {
	flags := make([]clicore.Flag, 0, len(op.Inputs))
	for _, f := range op.Inputs {
		desc := f.Description
		if strings.TrimSpace(desc) == "" {
			desc = fmt.Sprintf("Input %s for %s.", f.Name, op.ID)
		}
		flags = append(flags, clicore.Flag{
			Name:        f.Name,
			Kind:        flagKindForType(f.Type),
			Description: desc,
			Required:    f.Required,
			Enum:        f.EnumValues,
		})
	}
	return flags
}

func flagKindForType(t string) clicore.ValueKind {
	switch t {
	case "boolean":
		return clicore.KindBool
	case "integer":
		return clicore.KindInt
	case "number":
		return clicore.KindNumber
	default:
		return clicore.KindString
	}
}

func buildOperationCommandFlags(op direct.Operation) []clicore.Flag {
	inputFlags := ContractInputFlags(op)
	seen := map[string]bool{}
	var flags []clicore.Flag
	for _, f := range inputFlags {
		if !seen[f.Name] {
			seen[f.Name] = true
			flags = append(flags, f)
		}
	}
	common := []clicore.Flag{
		profileFlag,
		{
			Name:        "allow-unverified",
			Kind:        clicore.KindBool,
			Description: "Explicitly allow direct execution of an UNVERIFIED operation. This does not upgrade its readiness or evidence.",
		},
		{
			Name:        "plan",
			Kind:        clicore.KindBool,
			Description: "Describe what an execution would do, without contacting Odoo.",
		},
		{
			Name:        "provider-target-id",
			Kind:        clicore.KindInt,
			Description: "Provider-native record id of the record to act on.",
		},
	}
	if op.RequiresImpactConfirmation || op.Strategy == string(odoo.StrategyDestructiveGuarded) {
		common = append(common, clicore.Flag{
			Name:        "confirm-impact",
			Kind:        clicore.KindBool,
			Description: "Accept the dependent-record impact a destructive operation reports.",
		})
	}
	if op.Strategy == string(odoo.StrategyReport) || strings.HasSuffix(op.ID, ".attachment.describe") {
		common = append(common, saveFileFlag)
	}
	if (op.Kind == string(odoo.KindRead) || op.Strategy == string(odoo.StrategyRead)) && op.Strategy != string(odoo.StrategyReport) {
		common = append(common,
			clicore.Flag{Name: "filter", Kind: clicore.KindJSON, Description: "Provider search domain, as a JSON array. READ operations only."},
			clicore.Flag{Name: "fields", Kind: clicore.KindString, Repeatable: true, Description: "Field list to project."},
			clicore.Flag{Name: "limit", Kind: clicore.KindInt, Description: "Maximum records to return. READ operations only."},
			clicore.Flag{Name: "order", Kind: clicore.KindString, Description: "Provider sort order. READ operations only."},
		)
		common = append(common, aerpsurface.ReadPagingFlags()...)
	} else {
		common = append(common, aerpsurface.VerifyFlag())
	}
	common = append(common,
		clicore.Flag{Name: "set", Kind: clicore.KindKeyValue, Repeatable: true, Description: "Set one semantic field, as name=value."},
		clicore.Flag{Name: "values", Kind: clicore.KindJSON, Description: "Whole values object as one JSON document."},
		clicore.Flag{Name: "provider-context", Kind: clicore.KindJSON, Description: "Provider execution context, as a JSON object."},
	)
	for _, f := range common {
		if !seen[f.Name] {
			seen[f.Name] = true
			flags = append(flags, f)
		}
	}
	return flags
}

func sideEffectFor(op direct.Operation) clicore.SideEffect {
	if op.Kind == string(odoo.KindRead) || op.Strategy == string(odoo.StrategyRead) {
		return clicore.EffectRead
	}
	return clicore.EffectWriteProvider
}

func authorityFor(op direct.Operation) *clicore.Authority {
	if op.RequiresImpactConfirmation || op.Strategy == string(odoo.StrategyDestructiveGuarded) {
		return destructiveLocalAuthority
	}
	return localAuthority
}

func findOrCreateChild(parent *clicore.Command, name string) *clicore.Command {
	for _, child := range parent.Children {
		if child.Name == name {
			return child
		}
	}
	group := &clicore.Command{
		Name:     name,
		Summary:  fmt.Sprintf("Commands for %s.", name),
		Children: []*clicore.Command{},
	}
	parent.Children = append(parent.Children, group)
	return group
}

func mountOperationCommands(root *clicore.Command, config programConfig) {
	for _, op := range direct.AllOperations() {
		parts := strings.Split(op.ID, ".")
		current := root
		for i := 0; i < len(parts)-1; i++ {
			current = findOrCreateChild(current, parts[i])
		}
		leafName := parts[len(parts)-1]
		leaf := local(&clicore.Command{
			Name:        leafName,
			Summary:     fmt.Sprintf("Run %s against the selected Odoo.", op.ID),
			Description: fmt.Sprintf("Semantic alias for %s (strategy: %s, risk: %s, readiness: %s).", op.ID, op.Strategy, op.Risk, op.Readiness),
			SideEffect:  sideEffectFor(op),
			Authority:   authorityFor(op),
			Operation:   op.ID,
			TargetRule:  op.TargetRequirement,
			Readiness:   op.Readiness,
			Risk:        op.Risk,
			Flags:       buildOperationCommandFlags(op),
			Examples: []string{
				fmt.Sprintf("%s %s --profile lab-a --output json", config.name, strings.Join(parts, " ")),
			},
			Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
				return runOperationAlias(ctx, invocation, op, config)
			},
		})
		current.Children = append(current.Children, leaf)
	}
}

func runOperationAlias(ctx context.Context, invocation *clicore.Invocation, op direct.Operation, config programConfig) (interface{}, error) {
	if !invocation.Has("operation") {
		invocation.Raw["operation"] = []string{op.ID}
	}
	for _, f := range op.Inputs {
		if invocation.Has(f.Name) {
			val := invocation.String(f.Name)
			invocation.Raw["set"] = append(invocation.Raw["set"], f.Name+"="+val)
		}
	}
	executor, request, err := standaloneExecutor(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	if isPlan, _ := invocation.Bool("plan"); isPlan {
		plan, err := executor.Plan(ctx, request)
		if err != nil {
			return nil, classifyExecution(err, request.Operation, config.surface)
		}
		return plan, nil
	}
	if request.Verify, err = invocation.Bool("verify"); err != nil {
		return nil, err
	}
	outcome, err := executor.Execute(ctx, request)
	if err != nil {
		return nil, classifyExecution(err, request.Operation, config.surface)
	}
	return presentOutcome(invocation, outcome)
}

// FindOperationCommand resolves the generated command for a catalog operation ID.
func FindOperationCommand(root *clicore.Command, operationID string) *clicore.Command {
	parts := strings.Split(operationID, ".")
	current := root
	for _, part := range parts {
		found := false
		for _, child := range current.Children {
			if child.Name == part {
				current = child
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	if current.IsGroup() {
		return nil
	}
	return current
}
