package contractbundle

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/asao-platform/atoc/packages/cli/internal/contract"
)

//go:embed bundle.gen.json
var embeddedBundleBytes []byte

func LoadBundle() (*contract.ContractBundle, error) {
	if len(embeddedBundleBytes) == 0 {
		return nil, fmt.Errorf("embedded contract bundle is empty")
	}
	var bundle contract.ContractBundle
	if err := json.Unmarshal(embeddedBundleBytes, &bundle); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embedded contract bundle: %w", err)
	}
	if len(bundle.Descriptors) == 0 {
		return nil, fmt.Errorf("embedded contract bundle contains no descriptors")
	}
	return &bundle, nil
}
