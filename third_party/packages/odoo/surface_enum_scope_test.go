package odoo

import (
	"context"
	"errors"
	"testing"
)

// The scoped system parameters reach three keys. A create naming any other key is refused before the
// provider is contacted: Odoo accepts any key, so the operation's own closed list is the only scope.
func TestASystemParameterOutsideItsScopeIsNotCreated(t *testing.T) {
	create := func(key string) *ExecutionIntent {
		return &ExecutionIntent{ID: "test", Operation: "structure.system_parameter.create",
			Input: `{"values":{"key":"` + key + `","value":"x"}}`}
	}
	for _, key := range []string{"auth_signup.invitation_scope", "database.secret", "Conformance key", ""} {
		tr := &fakeTransport{}
		a := NewAdapter(tr)
		a.Surface = true
		_, err := a.Execute(context.Background(), create(key))
		var refused *EnumValueError
		if !errors.As(err, &refused) || refused.Field != "key" {
			t.Fatalf("key %q: want an EnumValueError naming key, got %v", key, err)
		}
		assertNoMutation(t, tr, key)
	}
	a := NewAdapter(&fakeTransport{})
	a.Surface = true
	if err := a.ValidateInvocation(create("report.url")); err != nil {
		t.Fatalf("report.url is in scope: %v", err)
	}
}
