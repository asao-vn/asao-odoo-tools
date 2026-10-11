package direct_test

import (
	"testing"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// A create input the adapter holds to a prefix is required on create (a record without it would fall
// outside the operation's scope), and the catalog publishes it so; an update need not restate it.
func TestAPrefixScopedInputIsPublishedAsRequiredOnCreateOnly(t *testing.T) {
	for id, want := range map[string]bool{"website.media.create": true, "website.media.update": false} {
		op, err := direct.DescribeAnyOperation(id)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, input := range op.Inputs {
			if input.Name == "url" {
				found = true
				if input.Required != want {
					t.Errorf("%s: url required = %v, want %v", id, input.Required, want)
				}
			}
		}
		if !found {
			t.Errorf("%s publishes no url input", id)
		}
	}
}
