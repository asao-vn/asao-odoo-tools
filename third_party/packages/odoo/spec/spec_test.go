package spec

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	odoo "github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// fakeExecutor answers reads from a table keyed by "operation|domain" or "operation#target", and
// records every request.
type fakeExecutor struct {
	reads    map[string][]map[string]interface{}
	fail     map[string]error
	created  int
	requests []direct.Request
}

func (f *fakeExecutor) Execute(_ context.Context, r direct.Request) (direct.Outcome, error) {
	f.requests = append(f.requests, r)
	if err := f.fail[r.Operation]; err != nil {
		return direct.Outcome{}, err
	}
	key := fmt.Sprintf("%s|%v", r.Operation, r.Domain)
	if r.ProviderTargetID != 0 {
		key = fmt.Sprintf("%s#%d", r.Operation, r.ProviderTargetID)
	}
	if rows, ok := f.reads[key]; ok {
		return direct.Outcome{Operation: r.Operation, Status: "SUCCESS", Data: rows, ProviderSeries: "19.0"}, nil
	}
	if strings.HasSuffix(r.Operation, ".create") {
		f.created++
		return direct.Outcome{Operation: r.Operation, Status: "SUCCESS", ProviderReference: fmt.Sprint(100 + f.created)}, nil
	}
	return direct.Outcome{Operation: r.Operation, Status: "SUCCESS", ProviderSeries: "19.0"}, nil
}

func row(values ...interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for i := 0; i < len(values); i += 2 {
		out[values[i].(string)] = values[i+1]
	}
	return out
}

func testSpec() Spec {
	return Spec{
		APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "asao-hq", Placeholders: []string{"vat"}},
		Modules: []string{"crm", "hr", "no_such_module"},
		Resources: []Resource{
			{Entity: "people.department", Values: map[string]interface{}{"name": "Kỹ thuật"}},
			{Entity: "identity.contact", Values: map[string]interface{}{"name": "ASAO", "email": "hello@asao.vn", "comment": "x"}},
			{Entity: "people.department", Update: "people.department.rename", Match: map[string]interface{}{"name": "Kinh doanh"}, Values: map[string]interface{}{"name": "Kinh doanh", "parent": "Ban Giám đốc"}},
			{Entity: "structure.company", Update: "structure.company.reconfigure", Select: SelectFirst, Values: map[string]interface{}{"name": "ASAO"}},
			{Entity: "identity.contact", Values: map[string]interface{}{"name": "Twin"}},
			{Entity: "people.employee", Values: map[string]interface{}{"name": "An", "department": "Kỹ thuật"}},
		},
	}
}

func testExecutor() *fakeExecutor {
	return &fakeExecutor{reads: map[string][]map[string]interface{}{
		"structure.module.list|[[name = crm]]":            {row("id", 5.0, "name", "crm", "state", "uninstalled")},
		"structure.module.list|[[name = hr]]":             {row("id", 6.0, "name", "hr", "state", "installed")},
		"structure.module.list|[[name = no_such_module]]": {},
		"people.department.list|[[name = Kỹ thuật]]":      {},
		"identity.contact.list|[[name = ASAO]]":           {row("id", 9.0, "name", "ASAO")},
		"identity.contact.describe#9":                     {row("id", 9.0, "name", "ASAO", "email", "old@asao.vn")},
		"people.department.list|[[name = Kinh doanh]]":    {row("id", 3.0, "name", "Kinh doanh")},
		"people.department.describe#3":                    {row("id", 3.0, "name", "Kinh doanh", "parent", []interface{}{1.0, "Ban Điều hành"})},
		"structure.company.list|[]":                       {row("id", 1.0, "name", "My Company")},
		"identity.contact.list|[[name = Twin]]":           {row("id", 11.0, "name", "Twin"), row("id", 12.0, "name", "Twin")},
		"people.employee.list|[[name = An]]":              {row("id", 21.0, "name", "An")},
		"people.employee.describe#21":                     {row("id", 21.0, "name", "An", "department", []interface{}{4.0, "Kỹ thuật"})},
	}}
}

func TestPlanSaysWhatEachStepWouldDo(t *testing.T) {
	plan, err := BuildPlan(context.Background(), testExecutor(), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan.Steps))
	for i, step := range plan.Steps {
		got[i] = fmt.Sprintf("%d %s %s %s %s #%d", step.Index, step.Entity, step.Key, step.Action, step.Operation, step.TargetID)
	}
	want := []string{
		"1 module name=crm INSTALL admin.module.install #5",
		"2 module name=hr NO_CHANGE  #0",
		"3 module name=no_such_module UNSUPPORTED  #0",
		"4 people.department name=Kỹ thuật CREATE people.department.create #0",
		"5 identity.contact name=ASAO UPDATE identity.contact.update #9",
		"6 people.department name=Kinh doanh UNSUPPORTED  #3",
		"7 structure.company first UNSUPPORTED  #1",
		"8 identity.contact name=Twin AMBIGUOUS  #0",
		"9 people.employee name=An NO_CHANGE  #21",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	update := plan.Steps[4]
	if fmt.Sprint(update.Changes) != "map[email:{old@asao.vn hello@asao.vn}]" || fmt.Sprint(update.Blind) != "[comment]" {
		t.Fatalf("update changes %v blind %v", update.Changes, update.Blind)
	}
	if fmt.Sprint(update.Values) != "map[comment:x email:hello@asao.vn]" {
		t.Fatalf("update sends %v: want only what changed and what cannot be read", update.Values)
	}
	if !strings.Contains(plan.Steps[5].Reason, "people.department.rename") {
		t.Fatalf("a record the catalog cannot change was not reported as a gap: %+v", plan.Steps[5])
	}
	if !strings.Contains(plan.Steps[6].Reason, "structure.company.reconfigure") {
		t.Fatalf("the company gap was not named: %+v", plan.Steps[6])
	}
	if plan.Summary[ActionUnsupported] != 3 || plan.Summary[ActionAmbiguous] != 1 || len(plan.Gaps()) != 4 {
		t.Fatalf("summary %v gaps %d", plan.Summary, len(plan.Gaps()))
	}
	if !strings.HasPrefix(plan.Hash, "sha256:") || plan.ProviderSeries != "19.0" {
		t.Fatalf("hash %q series %q", plan.Hash, plan.ProviderSeries)
	}
}

func TestApplyRefusesGapsUnlessAskedForAPartialApply(t *testing.T) {
	exec := testExecutor()
	plan, _ := BuildPlan(context.Background(), exec, testSpec())
	before := len(exec.requests)
	if _, err := Apply(context.Background(), exec, plan, ApplyOptions{}); !errors.Is(err, ErrPlanHasGaps) {
		t.Fatalf("err = %v, want the gaps refused", err)
	}
	if len(exec.requests) != before {
		t.Fatal("a refused apply ran something")
	}
	report, err := Apply(context.Background(), exec, plan, ApplyOptions{Partial: true, AllowUnverified: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Summary[ResultApplied] != 3 || report.Summary[ResultSkipped] != 4 || report.Summary[ResultUnchanged] != 2 {
		t.Fatalf("report %+v", report.Summary)
	}
	if created := report.Results[3]; created.Status != ResultApplied || created.TargetID != 101 {
		t.Fatalf("create result %+v: want the new record's id", created)
	}
	ran := exec.requests[before:]
	if ran[0].Operation != "admin.module.install" || ran[0].ProviderTargetID != 5 || !ran[0].AllowUnverified {
		t.Fatalf("first applied request %+v", ran[0])
	}
	if ran[2].Operation != "identity.contact.update" || fmt.Sprint(ran[2].Values) != "map[comment:x email:hello@asao.vn]" {
		t.Fatalf("update request %+v", ran[2])
	}
}

func TestApplyStopsAtTheFirstFailure(t *testing.T) {
	exec := testExecutor()
	s := testSpec()
	s.Modules = nil
	s.Resources = s.Resources[:2]
	plan, _ := BuildPlan(context.Background(), exec, s)
	exec.fail = map[string]error{"people.department.create": &direct.OperationError{Operation: "people.department.create", Class: "VALIDATION_FAILED", Message: "name is required"}}
	report, err := Apply(context.Background(), exec, plan, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Status != ResultFailed || report.Results[0].ErrorClass != "VALIDATION_FAILED" || report.Results[1].Status != ResultNotRun || report.Complete {
		t.Fatalf("results %+v", report.Results)
	}
}

func TestApplyRefusesAPlanOtherThanTheReviewedOne(t *testing.T) {
	exec := testExecutor()
	plan, _ := BuildPlan(context.Background(), exec, testSpec())
	if _, err := Apply(context.Background(), exec, plan, ApplyOptions{Partial: true, PlanHash: "sha256:other"}); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplySpecRefusesTheReviewedPlansGapsBeforeInstallingAModule(t *testing.T) {
	exec := testExecutor()
	if _, _, _, err := ApplySpec(context.Background(), exec, testSpec(), ApplyOptions{AllowUnverified: true}); !errors.Is(err, ErrPlanHasGaps) {
		t.Fatalf("err = %v, want the gaps refused", err)
	}
	for _, request := range exec.requests {
		if request.Operation == "admin.module.install" {
			t.Fatal("a refused apply installed a module first")
		}
	}
}

func TestARecordOnlyAModuleBringsIsPlannedAfterTheModules(t *testing.T) {
	layout := Resource{Entity: "website.layout", Match: map[string]interface{}{"key": "website.header_search_box"}, Values: map[string]interface{}{"active": false}}
	exec := &fakeExecutor{reads: map[string][]map[string]interface{}{
		"structure.module.list|[[name = website]]": {row("id", 7.0, "name", "website", "state", "uninstalled")},
	}}
	s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "blank"}, Modules: []string{"website"}, Resources: []Resource{layout}}
	plan, err := BuildPlan(context.Background(), exec, s)
	if err != nil {
		t.Fatal(err)
	}
	if step := plan.Steps[1]; step.Action != ActionAfterModules || len(plan.Gaps()) != 0 {
		t.Fatalf("step %+v, %d gaps: want the layout view planned after the website module", step, len(plan.Gaps()))
	}
	s.Modules = nil
	if plan, err = BuildPlan(context.Background(), exec, s); err != nil {
		t.Fatal(err)
	}
	if step := plan.Steps[0]; step.Action != ActionUnsupported || !strings.Contains(step.Reason, "website.layout.create") {
		t.Fatalf("step %+v: with nothing to install, a missing record the catalog cannot create is a gap", step)
	}
}

// A record whose operation the catalog refuses for a missing module (MODULE_NOT_INSTALLED, before
// Odoo is asked) is planned after the modules when the plan installs some, as a model Odoo does not
// know yet is: a restaurant's tables come with pos_restaurant.
func TestARecordWhoseOperationNeedsAModuleToComeIsPlannedAfterTheModules(t *testing.T) {
	table := Resource{Entity: "pos.table", Match: map[string]interface{}{"table_number": 1}, Values: map[string]interface{}{"table_number": 1}}
	exec := &fakeExecutor{
		reads: map[string][]map[string]interface{}{
			"structure.module.list|[[name = pos_restaurant]]": {row("id", 9.0, "name", "pos_restaurant", "state", "uninstalled")},
		},
		fail: map[string]error{"pos.table.list": &direct.OperationError{Class: string(odoo.ErrClassModuleNotInstalled),
			Message: "pos.table.list needs Odoo modules that are not installed: pos_restaurant; nothing was executed"}},
	}
	s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "nha-hang"}, Modules: []string{"pos_restaurant"}, Resources: []Resource{table}}
	plan, err := BuildPlan(context.Background(), exec, s)
	if err != nil {
		t.Fatal(err)
	}
	if step := plan.Steps[1]; step.Action != ActionAfterModules || len(plan.Gaps()) != 0 {
		t.Fatalf("step %+v, %d gaps: want the table planned after pos_restaurant", step, len(plan.Gaps()))
	}
	s.Modules = nil
	if plan, err = BuildPlan(context.Background(), exec, s); err != nil {
		t.Fatal(err)
	}
	if step := plan.Steps[0]; step.Action != ActionUnsupported {
		t.Fatalf("step %+v: with nothing to install, a missing module is a gap", step)
	}
}

func TestAnAbsentRecordIsDeletedWhenItExists(t *testing.T) {
	exec := &fakeExecutor{reads: map[string][]map[string]interface{}{
		"structure.module.list|[[name = crm]]":       {row("id", 5.0, "name", "crm", "state", "uninstalled")},
		"people.department.list|[[name = Kỹ thuật]]": {row("id", 7.0, "name", "Kỹ thuật")},
		"people.department.list|[[name = Twin]]":     {row("id", 8.0, "name", "Twin"), row("id", 9.0, "name", "Twin")},
		"structure.company.list|[[name = ASAO]]":     {row("id", 1.0, "name", "ASAO")},
	}}
	s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "absent"}, Resources: []Resource{
		{Entity: "people.department", Absent: true, Values: map[string]interface{}{"name": "Kỹ thuật"}},
		{Entity: "people.department", Absent: true, Match: map[string]interface{}{"name": "Pháp chế"}},
		{Entity: "people.department", Absent: true, Values: map[string]interface{}{"name": "Twin"}},
		{Entity: "structure.company", Absent: true, Values: map[string]interface{}{"name": "ASAO"}},
	}}
	plan, err := BuildPlan(context.Background(), exec, s)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan.Steps))
	for i, step := range plan.Steps {
		got[i] = fmt.Sprintf("%s %s %s #%d", step.Key, step.Action, step.Operation, step.TargetID)
	}
	want := []string{
		"name=Kỹ thuật DELETE people.department.delete #7",
		"name=Pháp chế NO_CHANGE  #0",
		"name=Twin AMBIGUOUS  #0",
		"name=ASAO UNSUPPORTED  #1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(plan.Steps[3].Reason, "structure.company.delete") {
		t.Fatalf("a record the catalog cannot delete was not reported as a gap: %+v", plan.Steps[3])
	}
	before := len(exec.requests)
	if _, err := Apply(context.Background(), exec, plan, ApplyOptions{Partial: true}); err != nil {
		t.Fatal(err)
	}
	ran := exec.requests[before:]
	if len(ran) != 1 || ran[0].Operation != "people.department.delete" || ran[0].ProviderTargetID != 7 || ran[0].ConfirmImpact {
		t.Fatalf("applied %+v: want only the delete of #7, never with its impact confirmed", ran)
	}

	// A module the plan installs may bring the record (Odoo's own CRM stages come with crm).
	s.Modules = []string{"crm"}
	s.Resources = []Resource{{Entity: "crm.stage", Absent: true, Values: map[string]interface{}{"name": "Proposition"}}}
	if plan, err = BuildPlan(context.Background(), exec, s); err != nil {
		t.Fatal(err)
	}
	if step := plan.Steps[1]; step.Action != ActionAfterModules {
		t.Fatalf("step %+v: want the absent stage planned after crm is installed", step)
	}
}

func TestARecordTheSpecArchivesIsFoundArchived(t *testing.T) {
	exec := &fakeExecutor{reads: map[string][]map[string]interface{}{}}
	s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "archive"}, Resources: []Resource{
		{Entity: "crm.lost_reason", Values: map[string]interface{}{"name": "Cũ", "active": false}},
		{Entity: "crm.lost_reason", Absent: true, Values: map[string]interface{}{"name": "Bỏ"}},
		{Entity: "crm.lost_reason", Values: map[string]interface{}{"name": "Giá cao"}},
	}}
	if _, err := BuildPlan(context.Background(), exec, s); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, request := range exec.requests {
		seen = append(seen, fmt.Sprintf("%v %v", request.Domain, request.ProviderContext["active_test"]))
	}
	want := "[[name = Cũ]] false|[[name = Bỏ]] false|[[name = Giá cao]] <nil>"
	if strings.Join(seen, "|") != want {
		t.Fatalf("finds %q: want archived records seen only where the spec states active or absent (%q)", strings.Join(seen, "|"), want)
	}
}

func TestSettingsArePlannedAsOneSaveOfWhatDiffers(t *testing.T) {
	exec := &fakeExecutor{reads: map[string][]map[string]interface{}{
		"structure.module.list|[[name = project]]": {row("id", 4.0, "name", "project", "state", "uninstalled")},
		"structure.settings.describe|[]":           {row("group_use_lead", false, "auth_signup_uninvited", "b2b", "group_uom", true)},
	}}
	s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "settings"},
		Settings: map[string]interface{}{"group_use_lead": true, "auth_signup_uninvited": "b2b", "group_uom": true}}
	plan, err := BuildPlan(context.Background(), exec, s)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	if step.Action != ActionUpdate || step.Operation != "structure.settings.update" || fmt.Sprint(step.Values) != "map[group_use_lead:true]" || fmt.Sprint(step.Changes) != "map[group_use_lead:{false true}]" {
		t.Fatalf("step %+v: want one save of group_use_lead only", step)
	}
	before := len(exec.requests)
	if _, err := Apply(context.Background(), exec, plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if ran := exec.requests[before:]; len(ran) != 1 || ran[0].Operation != "structure.settings.update" || ran[0].ProviderTargetID != 0 || fmt.Sprint(ran[0].Values) != "map[group_use_lead:true]" {
		t.Fatalf("applied %+v", ran)
	}

	for name, c := range map[string]struct {
		modules  []string
		settings map[string]interface{}
		action   Action
		reason   string
	}{
		"a module switch":               {nil, map[string]interface{}{"module_crm_iap_enrich": true}, ActionUnsupported, "does not set module_crm_iap_enrich"},
		"a setting of a missing module": {nil, map[string]interface{}{"group_project_stages": true}, ActionUnsupported, "no setting group_project_stages"},
		"a setting of a module to come": {[]string{"project"}, map[string]interface{}{"group_project_stages": true}, ActionAfterModules, "group_project_stages"},
		"nothing to change":             {nil, map[string]interface{}{"group_uom": true}, ActionNoChange, ""},
	} {
		s := Spec{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "settings"}, Modules: c.modules, Settings: c.settings}
		plan, err := BuildPlan(context.Background(), exec, s)
		if err != nil {
			t.Fatal(err)
		}
		step := plan.Steps[len(plan.Steps)-1]
		if step.Action != c.action || !strings.Contains(step.Reason, c.reason) {
			t.Fatalf("%s: step %+v, want %s (%q)", name, step, c.action, c.reason)
		}
	}
}

func TestValidateRefusesWhatCannotBePlanned(t *testing.T) {
	good := testSpec()
	for name, mutate := range map[string]func(*Spec){
		"version":   func(s *Spec) { s.APIVersion = "v9" },
		"name":      func(s *Spec) { s.Metadata.Name = "" },
		"no values": func(s *Spec) { s.Resources[0].Values = nil },
		"select":    func(s *Spec) { s.Resources[0].Select = "last" },
		"identity":  func(s *Spec) { s.Resources[0].Values = map[string]interface{}{"email": "x"} },
		"entity":    func(s *Spec) { s.Resources[0].Entity = "" },
		"absent with values": func(s *Spec) {
			s.Resources[0] = Resource{Entity: "people.department", Absent: true, Values: map[string]interface{}{"name": "x", "parent": "y"}}
		},
		"absent with match and values": func(s *Spec) {
			s.Resources[0] = Resource{Entity: "people.department", Absent: true, Match: map[string]interface{}{"name": "x"}, Values: map[string]interface{}{"name": "x"}}
		},
		"absent by select": func(s *Spec) {
			s.Resources[0] = Resource{Entity: "structure.company", Absent: true, Select: SelectFirst}
		},
		"absent unnamed": func(s *Spec) { s.Resources[0] = Resource{Entity: "people.department", Absent: true} },
		"absent without ops": func(s *Spec) {
			s.Resources[0] = Resource{Find: "people.department.list", Absent: true, Values: map[string]interface{}{"name": "x"}}
		},
	} {
		s := good
		s.Resources = append([]Resource(nil), good.Resources...)
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSameValueReadsRelationsAndEmptiness(t *testing.T) {
	cases := []struct {
		want, have interface{}
		same       bool
	}{
		{"Kỹ thuật", []interface{}{4.0, "Kỹ thuật"}, true},
		{"Kỹ thuật", []interface{}{4.0, "Ban Giám đốc / Kỹ thuật"}, true},
		{"Stock", []interface{}{8.0, "WH/Stock"}, true},
		{"Kỹ", []interface{}{4.0, "Ban Giám đốc / Kỹ thuật"}, false},
		{"Ban Giám đốc / Kỹ thuật", []interface{}{4.0, "Ban Giám đốc / Kỹ thuật"}, true},
		{4, []interface{}{4.0, "Kỹ thuật"}, true},
		{"Khác", []interface{}{4.0, "Kỹ thuật"}, false},
		{"", false, true},
		{nil, false, true},
		{false, false, true},
		{true, false, false},
		{10, 10.0, true},
		{"VND", map[string]interface{}{"id": 23.0, "display_name": "VND"}, true},
		{"a", "b", false},
		{"<t>page</t>\n", "<t>page</t>", true},
		{"<t>page</t>", "<t>other</t>", false},
	}
	for _, c := range cases {
		if got := sameValue(c.want, c.have); got != c.same {
			t.Fatalf("sameValue(%v, %v) = %v", c.want, c.have, got)
		}
	}
}

func TestFileValuesAreReadRelativeToTheSpec(t *testing.T) {
	s := testSpec()
	s.Resources = []Resource{{Entity: "website.page", Match: map[string]interface{}{"url": "/"}, Values: map[string]interface{}{
		"arch": map[string]interface{}{"file": "website/home.xml"},
		"logo": map[string]interface{}{"file": "logo.png", "encoding": "base64"},
		"name": "Trang chủ",
	}}}
	files := map[string][]byte{"website/home.xml": []byte("<t>xin chào</t>"), "logo.png": {0x89, 'P', 'N', 'G'}}
	read := func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("no %s", path)
	}
	resolved, err := ResolveFiles(s, read)
	if err != nil {
		t.Fatal(err)
	}
	values := resolved.Resources[0].Values
	if values["arch"] != "<t>xin chào</t>" || values["logo"] != "iVBORw==" || values["name"] != "Trang chủ" {
		t.Fatalf("resolved = %v", values)
	}
	if _, isRef := s.Resources[0].Values["arch"].(map[string]interface{}); !isRef {
		t.Fatal("ResolveFiles changed the spec it was given")
	}
	for name, bad := range map[string]interface{}{
		"absolute": map[string]interface{}{"file": "/etc/passwd"},
		"encoding": map[string]interface{}{"file": "logo.png", "encoding": "hex"},
		"option":   map[string]interface{}{"file": "logo.png", "mode": "x"},
		"missing":  map[string]interface{}{"file": "nope.png"},
	} {
		s.Resources[0].Values = map[string]interface{}{"name": "x", "arch": bad}
		if _, err := ResolveFiles(s, read); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestAPublishedFileIsNotSentAgainWhenItsChecksumMatches(t *testing.T) {
	video := []byte("webm bytes")
	encoded := "d2VibSBieXRlcw==" // base64 of video
	changes, blind := compare(map[string]interface{}{"datas": encoded, "name": "hero"}, map[string]interface{}{"name": "hero", "checksum": sha1Hex(video)})
	if len(changes) != 0 || len(blind) != 0 {
		t.Fatalf("changes %v blind %v: an unchanged file would be sent again", changes, blind)
	}
	_, blind = compare(map[string]interface{}{"datas": encoded}, map[string]interface{}{"checksum": sha1Hex([]byte("other"))})
	if fmt.Sprint(blind) != "[datas]" {
		t.Fatalf("blind %v: a changed file must be sent", blind)
	}
}

func sha1Hex(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}
