package odoo

import "testing"

// OptionalReadFields decides which fields an operation may read when the provider declares them.
// A client and a runtime that disagree about it do not share a contract, so it must be part of the
// semantic contract identity. Its absence was a real freshness gap found by V2.5 Gate #3: both
// OptionalReadFields fixes in that gate were caught only incidentally, because ReadFields shrank at
// the same time. A change that merely ADDS an optional field moved no identity at all.
//
// It is identified as a SET, not as an ordered list: execRead consumes it purely as a membership
// test, so reordering or repeating an entry changes no behaviour and must not change the identity,
// while adding or removing a field changes what may be read and must change it.

func identityOf(ops []Odoo19Operation) string {
	return ComputeSemanticContractIdentityFromOperationsAndContracts(ops, Odoo19Contracts)
}

// twoOps returns a minimal, stable pair of operations to hash, with the optional reads of the first
// replaced by the supplied declaration.
// The two synthetic operations below assign their ids from variables rather than with a literal
// `SemanticID:` field, deliberately: the single-catalog invariant is enforced by grepping the tree
// for that literal, and a test fixture must not make a second catalog appear to exist. These
// operations are never registered and never shipped.
func opsWithOptional(optional []string) []Odoo19Operation {
	first, second := "test.read.one", "test.read.two"
	a := Odoo19Operation{
		Kind: KindRead, Strategy: StrategyRead,
		RequiredCapability: "test.read", ProviderModel: "test.model",
		ReadFields:         []string{"id", "name"},
		OptionalReadFields: optional,
	}
	a.SemanticID = first
	b := Odoo19Operation{
		Kind: KindRead, Strategy: StrategyRead,
		RequiredCapability: "test.read", ProviderModel: "test.other",
		ReadFields: []string{"id"},
	}
	b.SemanticID = second
	return []Odoo19Operation{a, b}
}

func TestContractIdentity_SameContractSameIdentity(t *testing.T) {
	a := identityOf(opsWithOptional([]string{"qty_available", "is_storable"}))
	b := identityOf(opsWithOptional([]string{"qty_available", "is_storable"}))
	if a != b {
		t.Fatalf("an identical contract must hash identically:\n  %s\n  %s", a, b)
	}
	if a == "" {
		t.Fatal("identity must not be empty")
	}
}

// Order carries no meaning for a membership test, so it must not move the identity.
func TestContractIdentity_OptionalReadFieldsReorderIsSameIdentity(t *testing.T) {
	a := identityOf(opsWithOptional([]string{"qty_available", "is_storable", "purchase_id"}))
	b := identityOf(opsWithOptional([]string{"purchase_id", "qty_available", "is_storable"}))
	if a != b {
		t.Fatalf("reordering OptionalReadFields must not change the identity:\n  %s\n  %s", a, b)
	}
}

// A repeated entry is the same set, so it must not move the identity either.
func TestContractIdentity_OptionalReadFieldsDuplicateIsSameIdentity(t *testing.T) {
	a := identityOf(opsWithOptional([]string{"qty_available", "is_storable"}))
	b := identityOf(opsWithOptional([]string{"qty_available", "is_storable", "qty_available"}))
	if a != b {
		t.Fatalf("duplicating an OptionalReadFields entry must not change the identity:\n  %s\n  %s", a, b)
	}
}

// Membership IS the contract: adding, removing or substituting a field must move the identity.
func TestContractIdentity_OptionalReadFieldsMembershipChangesIdentity(t *testing.T) {
	base := identityOf(opsWithOptional([]string{"qty_available"}))

	for _, tc := range []struct {
		name     string
		optional []string
	}{
		{"field added", []string{"qty_available", "is_storable"}},
		{"field removed", nil},
		{"field substituted", []string{"is_storable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := identityOf(opsWithOptional(tc.optional))
			if got == base {
				t.Fatalf("%s must change the semantic contract identity, but it did not (%s)", tc.name, got)
			}
		})
	}
}

// An absent declaration and an explicitly empty one mean the same thing and must hash the same.
func TestContractIdentity_OptionalReadFieldsEmptyEqualsAbsent(t *testing.T) {
	if identityOf(opsWithOptional(nil)) != identityOf(opsWithOptional([]string{})) {
		t.Fatal("an empty OptionalReadFields must hash identically to an absent one")
	}
}

// The real catalog must exercise this: if no shipping operation declared optional reads, the tests
// above would be proving a property nothing depends on.
func TestContractIdentity_ShippingCatalogDeclaresOptionalReads(t *testing.T) {
	declaring := 0
	for _, op := range Catalog() {
		if len(op.OptionalReadFields) > 0 {
			declaring++
		}
	}
	if declaring == 0 {
		t.Fatal("no shipping operation declares OptionalReadFields; this identity binding would be vacuous")
	}
	t.Logf("%d shipping operations declare OptionalReadFields", declaring)
}
