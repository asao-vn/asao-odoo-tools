package review

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeBooks answers each read operation with its fixtures; filters are not applied, so a fixture
// holds only records a filter would let through. A refused operation answers a refusal.
type fakeBooks struct {
	rows    map[string][]map[string]interface{}
	refused map[string]bool
}

type refusal struct{ operation string }

func (r refusal) Error() string { return r.operation + " refused" }
func (r refusal) Refusal() string {
	return "AERP_CAPABILITY_DENIED: " + r.operation + " is not granted"
}

func (f fakeBooks) ReadAll(_ context.Context, operation string, _ []interface{}) ([]map[string]interface{}, error) {
	if f.refused[operation] {
		return nil, refusal{operation}
	}
	return f.rows[operation], nil
}

func bill(id int, vendor int, date, ref string, total float64) map[string]interface{} {
	return map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("BILL/%d", id), "document_type": "in_invoice", "state": "posted",
		"invoice_date": date, "ref": ref, "total_amount": total, "amount_untaxed": total, "amount_residual": 0.0, "payment_status": "paid",
		"partner": map[string]interface{}{"id": float64(vendor), "display_name": fmt.Sprintf("NCC %d", vendor)}}
}

func claim(id, employee int, date string, amount float64) map[string]interface{} {
	return map[string]interface{}{"id": float64(id), "name": fmt.Sprintf("chi %d", id), "employee_id": []interface{}{float64(employee), fmt.Sprintf("NV %d", employee)},
		"date": date, "total_amount": amount, "state": "approved"}
}

func run(t *testing.T, books fakeBooks, checks ...string) *Report {
	t.Helper()
	report, err := Run(context.Background(), books, Options{
		From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Checks: checks})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestABillEnteredTwiceIsFoundEvenRetypedAndAMonthlyRentIsNot(t *testing.T) {
	rows := []map[string]interface{}{
		bill(1, 7, "2026-05-14", "HD0001234", 34_320_000), bill(2, 7, "2026-05-20", "HD0001234", 34_320_000),
		bill(3, 8, "2026-07-08", "HD0045871", 47_850_000), bill(4, 8, "2026-07-17", "hd-0045871", 47_850_000),
	}
	for month := 4; month <= 9; month++ {
		rows = append(rows, bill(10+month, 9, fmt.Sprintf("2026-%02d-05", month), fmt.Sprintf("TK-%02d/2026", month), 15_000_000))
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opInvoice: rows}}, "O01")
	var pairs []string
	for _, f := range report.Findings {
		pairs = append(pairs, fmt.Sprintf("%d+%d", f.Evidence[0].ID, f.Evidence[1].ID))
		if f.Severity != XemNgay || f.Facts["bothPaid"] != true || f.Formula == "" || f.Params["window_days"] != 14 {
			t.Errorf("finding %+v", f)
		}
	}
	if strings.Join(pairs, ",") != "4+3,2+1" && strings.Join(pairs, ",") != "3+4,1+2" {
		t.Fatalf("pairs = %v, want the two duplicates and not the rent", pairs)
	}
}

func TestAVendorPaidIntoAnEmployeesAccount(t *testing.T) {
	books := fakeBooks{rows: map[string][]map[string]interface{}{
		opEmployee: {{"id": 5.0, "name": "Trần Văn Bình", "work_contact_id": []interface{}{50.0, "Trần Văn Bình"}}},
		opBank: {{"id": 1.0, "acc_number": "9999 000 460122", "partner_id": []interface{}{50.0, "Trần Văn Bình"}},
			{"id": 2.0, "acc_number": "9999000460122", "partner_id": []interface{}{40.0, "Vận tải Hoà Phát"}}},
		opContact: {{"id": 40.0, "name": "Vận tải Hoà Phát", "vendor_priority": 1.0}},
		opPayment: {{"id": 9.0, "name": "P9", "payment_type": "outbound", "partner": []interface{}{40.0, "x"}, "amount": 14_800_000.0, "date": "2026-07-07", "state": "paid"}},
	}}
	report := run(t, books, "O17")
	if len(report.Findings) != 1 || report.Findings[0].AmountVND != 14_800_000 {
		t.Fatalf("findings %+v", report.Findings)
	}
	var names []string
	for _, e := range report.Findings[0].Evidence {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "Vận tải Hoà Phát,Trần Văn Bình,9999000460122" {
		t.Fatalf("evidence %v", names)
	}
}

func TestRoundClaimsAreJudgedAgainstEveryoneElse(t *testing.T) {
	var rows []map[string]interface{}
	for i := 0; i < 6; i++ {
		rows = append(rows, claim(i, 1, fmt.Sprintf("2026-07-0%d", i%9+1), float64(500_000*(1+i%3))))
	}
	for i := 0; i < 20; i++ {
		rows = append(rows, claim(100+i, 2, fmt.Sprintf("2026-07-1%d", i%9), float64(123_456+i)))
	}
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opExpense: rows}}, "O25")
	if len(report.Findings) != 1 || report.Findings[0].Facts["employee"] != "NV 1" {
		t.Fatalf("findings %+v", report.Findings)
	}
	// When every claim is a flat allowance, round is normal and nobody stands out.
	var flat []map[string]interface{}
	for i := 0; i < 30; i++ {
		flat = append(flat, claim(i, i%3, "2026-07-01", 200_000))
	}
	if report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opExpense: flat}}, "O25"); len(report.Findings) != 0 {
		t.Fatalf("flat allowances flagged: %+v", report.Findings)
	}
}

// Over a long period, a burst of round claims is not diluted by the ordinary claims around it (the
// 12-month simulation missed eight round claims among forty when the share was taken over the year).
func TestABurstOfRoundClaimsStandsOutOverALongPeriod(t *testing.T) {
	var rows []map[string]interface{}
	id := 0
	for month := 4; month <= 9; month++ {
		for day := 3; day <= 27; day += 4 { // seven ordinary claims a month
			rows = append(rows, claim(id, 1, fmt.Sprintf("2026-%02d-%02d", month, day), float64(87_000+id*1_013)))
			id++
		}
	}
	for i, day := range []string{"2026-08-02", "2026-08-09", "2026-08-16", "2026-08-23", "2026-08-30", "2026-09-06"} {
		rows = append(rows, claim(500+i, 1, day, 1_000_000))
	}
	for i := 0; i < 30; i++ {
		rows = append(rows, claim(900+i, 2, fmt.Sprintf("2026-06-%02d", i%28+1), float64(123_456+i)))
	}
	got := run(t, fakeBooks{rows: map[string][]map[string]interface{}{opExpense: rows}}, "O25").Findings
	if len(got) != 1 || got[0].Facts["rounded"] != 6 || !strings.Contains(got[0].Why, " từ ") {
		t.Fatalf("findings %+v", got)
	}
}

func TestACheckThatCannotReadIsNotRunNeverClean(t *testing.T) {
	report := run(t, fakeBooks{rows: map[string][]map[string]interface{}{}, refused: map[string]bool{opExpense: true}}, "O01", "O23")
	ran := map[string]CheckInfo{}
	for _, c := range report.Checks {
		ran[c.ID] = c
	}
	if !ran["O01"].Ran || ran["O23"].Ran || !strings.Contains(ran["O23"].NotRun, "AERP_CAPABILITY_DENIED") {
		t.Fatalf("checks %+v", report.Checks)
	}
	if !strings.Contains(Markdown(report, ""), "Không chạy được") {
		t.Fatal("the report hides a check that did not run")
	}
}

func TestEveryCheckHasItsKnowledgeAndFormula(t *testing.T) {
	for _, c := range Catalog() {
		if c.Formula == "" || !strings.Contains(Knowledge(c.ID), "## Hay báo nhầm khi") {
			t.Errorf("%s lacks its formula or its knowledge note", c.ID)
		}
	}
	if len(Catalog()) != 20 {
		t.Fatalf("%d checks, want 20", len(Catalog()))
	}
}

func TestMoneyAndShareReadAsVietnameseWritesThem(t *testing.T) {
	if money(12_500_000) != "12.500.000 đ" || money(950) != "950 đ" || pct(0.1838, 1) != "18,4%" {
		t.Fatalf("%s %s %s", money(12_500_000), money(950), pct(0.1838, 1))
	}
}
