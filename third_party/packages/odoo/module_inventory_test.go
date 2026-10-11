package odoo

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type moduleInventoryFakeTransport struct {
	version     string
	versionErr  error
	records     []map[string]interface{}
	searchErr   error
	searchCalls int
	model       string
	domain      interface{}
	fields      []string
	limit       int
	order       string
	providerCtx map[string]interface{}
}

func (f *moduleInventoryFakeTransport) ProviderVersion(context.Context) (string, error) {
	return f.version, f.versionErr
}

func (f *moduleInventoryFakeTransport) SearchRead(_ context.Context, model string, domain interface{}, fields []string, limit int, order string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	f.searchCalls++
	f.model = model
	f.domain = domain
	f.fields = append([]string(nil), fields...)
	f.limit = limit
	f.order = order
	f.providerCtx = providerCtx
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.records, nil
}

func TestFetchModuleInventoryQueriesOnlyInstalledSafeMetadata(t *testing.T) {
	fake := &moduleInventoryFakeTransport{
		version: "19.0-test",
		records: []map[string]interface{}{
			{"name": "sale", "state": "installed", "latest_version": "19.0"},
			{"name": "base", "state": "installed", "latest_version": "19.0"},
			{"name": "sale", "state": "installed", "latest_version": "19.0"},
			{"name": "hr", "state": "installed", "latest_version": "19.0"},
		},
	}

	got, err := FetchModuleInventory(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderVersion != "19.0-test" {
		t.Fatalf("ProviderVersion = %q", got.ProviderVersion)
	}
	if want := []string{"base", "hr", "sale"}; !reflect.DeepEqual(got.Installed, want) {
		t.Fatalf("Installed = %v, want %v", got.Installed, want)
	}
	if fake.model != "ir.module.module" {
		t.Fatalf("model = %q", fake.model)
	}
	if want := [][]interface{}{{"state", "=", "installed"}}; !reflect.DeepEqual(fake.domain, want) {
		t.Fatalf("domain = %#v, want %#v", fake.domain, want)
	}
	if want := []string{"name", "state", "latest_version"}; !reflect.DeepEqual(fake.fields, want) {
		t.Fatalf("fields = %v, want %v", fake.fields, want)
	}
	if fake.limit != 0 || fake.order != "name" || fake.providerCtx != nil {
		t.Fatalf("query options limit=%d order=%q context=%#v", fake.limit, fake.order, fake.providerCtx)
	}
	if fake.searchCalls != 1 {
		t.Fatalf("search calls = %d, want 1", fake.searchCalls)
	}
}

func TestFetchModuleInventoryRejectsMalformedRecordsAndPreservesProviderErrors(t *testing.T) {
	t.Run("malformed record", func(t *testing.T) {
		fake := &moduleInventoryFakeTransport{
			version: "19.0-test",
			records: []map[string]interface{}{{"name": 42, "state": "installed"}},
		}
		_, err := FetchModuleInventory(context.Background(), fake)
		if !errors.Is(err, ErrModuleInventoryMalformed) {
			t.Fatalf("error = %v, want ErrModuleInventoryMalformed", err)
		}
	})

	t.Run("version error", func(t *testing.T) {
		want := errors.New("version probe failed")
		fake := &moduleInventoryFakeTransport{versionErr: want}
		_, err := FetchModuleInventory(context.Background(), fake)
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want cause %v", err, want)
		}
		if fake.searchCalls != 0 {
			t.Fatalf("search calls = %d after version failure", fake.searchCalls)
		}
	})

	t.Run("search error", func(t *testing.T) {
		want := errors.New("module search failed")
		fake := &moduleInventoryFakeTransport{version: "19.0-test", searchErr: want}
		_, err := FetchModuleInventory(context.Background(), fake)
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want cause %v", err, want)
		}
	})
}

func TestAvailabilityUsesCatalogRequiredModulesAndDoesNotInferWrappers(t *testing.T) {
	leave, err := AvailabilityForOperation("workforce.leave.list", ModuleInventory{
		ProviderVersion: "19.0-test",
		Installed:       []string{"base", "hr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if leave.Available || !reflect.DeepEqual(leave.RequiredModules, []string{"hr_holidays"}) || !reflect.DeepEqual(leave.MissingModules, []string{"hr_holidays"}) {
		t.Fatalf("leave availability = %+v", leave)
	}

	sales, err := AvailabilityForOperation("sales.order.confirm", ModuleInventory{
		ProviderVersion: "19.0-test",
		Installed:       []string{"base", "sale", "sale_management"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sales.Available || len(sales.MissingModules) != 0 || !reflect.DeepEqual(sales.RequiredModules, []string{"sale"}) {
		t.Fatalf("sales availability = %+v", sales)
	}
}

func TestAvailabilityRejectsUnknownOperation(t *testing.T) {
	if _, err := AvailabilityForOperation("not.a.catalog.operation", ModuleInventory{}); err == nil {
		t.Fatal("unknown operation unexpectedly accepted")
	}
}
