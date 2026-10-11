package odoo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrModuleInventoryMalformed indicates that the module inventory records were invalid or malformed.
	ErrModuleInventoryMalformed = errors.New("odoo: module inventory malformed")
)

// ModuleInventoryTransport defines the transport capabilities required to inspect module inventory.
type ModuleInventoryTransport interface {
	SearchRead(ctx context.Context, model string, domain interface{}, fields []string, limit int, order string, context map[string]interface{}) ([]map[string]interface{}, error)
	ProviderVersion(ctx context.Context) (string, error)
}

// ModuleInventory holds the provider version and the sorted unique list of installed modules.
type ModuleInventory struct {
	ProviderVersion string   `json:"providerVersion"`
	Installed       []string `json:"installed"`
}

// OperationAvailability captures whether an operation can be performed given the installed modules.
type OperationAvailability struct {
	OperationID     string   `json:"operationId"`
	RequiredModules []string `json:"requiredModules"`
	MissingModules  []string `json:"missingModules"`
	Available       bool     `json:"available"`
}

// FetchModuleInventory queries ir.module.module for installed modules and returns a validated inventory.
func FetchModuleInventory(ctx context.Context, transport ModuleInventoryTransport) (ModuleInventory, error) {
	if transport == nil {
		return ModuleInventory{}, fmt.Errorf("%w: transport is nil", ErrModuleInventoryMalformed)
	}

	version, err := transport.ProviderVersion(ctx)
	if err != nil {
		return ModuleInventory{}, fmt.Errorf("failed to get provider version: %w", err)
	}

	domain := [][]interface{}{
		{"state", "=", "installed"},
	}
	fields := []string{"name", "state", "latest_version"}

	records, err := transport.SearchRead(ctx, "ir.module.module", domain, fields, 0, "name", nil)
	if err != nil {
		return ModuleInventory{}, fmt.Errorf("failed to query ir.module.module: %w", err)
	}

	installedSet := make(map[string]struct{})
	installed := make([]string, 0, len(records))

	for _, rec := range records {
		if rec == nil {
			return ModuleInventory{}, fmt.Errorf("%w: nil record encountered", ErrModuleInventoryMalformed)
		}

		rawName, ok := rec["name"]
		if !ok {
			return ModuleInventory{}, fmt.Errorf("%w: missing name field", ErrModuleInventoryMalformed)
		}
		name, ok := rawName.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return ModuleInventory{}, fmt.Errorf("%w: invalid module name: %v", ErrModuleInventoryMalformed, rawName)
		}

		rawState, ok := rec["state"]
		if !ok {
			return ModuleInventory{}, fmt.Errorf("%w: missing state field", ErrModuleInventoryMalformed)
		}
		state, ok := rawState.(string)
		if !ok || state != "installed" {
			return ModuleInventory{}, fmt.Errorf("%w: invalid or uninstalled state: %v", ErrModuleInventoryMalformed, rawState)
		}

		trimmedName := strings.TrimSpace(name)
		if _, exists := installedSet[trimmedName]; !exists {
			installedSet[trimmedName] = struct{}{}
			installed = append(installed, trimmedName)
		}
	}

	sort.Strings(installed)

	return ModuleInventory{
		ProviderVersion: version,
		Installed:       installed,
	}, nil
}

// AvailabilityForOperation determines whether an operation of either catalog is available based on
// its required modules.
func AvailabilityForOperation(operationID string, inventory ModuleInventory) (OperationAvailability, error) {
	op, ok := lookupOperation(operationID, true)
	if !ok {
		return OperationAvailability{}, fmt.Errorf("unknown operation: %s", operationID)
	}

	installedMap := make(map[string]struct{}, len(inventory.Installed))
	for _, mod := range inventory.Installed {
		installedMap[mod] = struct{}{}
	}

	required := make([]string, 0, len(op.RequiredModules))
	missing := make([]string, 0)
	missingMap := make(map[string]struct{})

	for _, mod := range op.RequiredModules {
		required = append(required, mod)
		if _, exists := installedMap[mod]; !exists {
			if _, seen := missingMap[mod]; !seen {
				missingMap[mod] = struct{}{}
				missing = append(missing, mod)
			}
		}
	}

	return OperationAvailability{
		OperationID:     operationID,
		RequiredModules: required,
		MissingModules:  missing,
		Available:       len(missing) == 0,
	}, nil
}
