package direct

import (
	"context"
	"fmt"

	"github.com/asao-platform/atoc/packages/odoo"
)

// ModuleInventory reads the provider's installed addon inventory through the
// same direct Odoo JSON-RPC connection used for operation execution.
func (e *Executor) ModuleInventory(ctx context.Context) (odoo.ModuleInventory, error) {
	if e == nil || e.client == nil {
		return odoo.ModuleInventory{}, fmt.Errorf("odoo direct: executor is not initialized")
	}
	return odoo.FetchModuleInventory(ctx, odoo.NewClientTransport(e.client))
}

// OperationAvailability reports whether an operation's catalog-declared
// required addons are installed on the selected Odoo provider.
func (e *Executor) OperationAvailability(ctx context.Context, operationID string) (odoo.OperationAvailability, error) {
	if _, ok := odoo.LookupStandaloneOperation(operationID); !ok {
		return odoo.OperationAvailability{}, fmt.Errorf("%w: %q", ErrUnknownOperation, operationID)
	}
	inventory, err := e.ModuleInventory(ctx)
	if err != nil {
		return odoo.OperationAvailability{}, err
	}
	return odoo.AvailabilityForOperation(operationID, inventory)
}
