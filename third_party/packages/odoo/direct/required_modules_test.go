package direct

import (
	"reflect"
	"testing"
)

func TestDescribeOperationRequiredModules(t *testing.T) {
	op, err := DescribeOperation("sales.order.confirm")
	if err != nil {
		t.Fatalf("DescribeOperation failed: %v", err)
	}

	expected := []string{"sale"}
	if !reflect.DeepEqual(op.RequiredModules, expected) {
		t.Fatalf("expected RequiredModules to be %v, got %v", expected, op.RequiredModules)
	}
}
