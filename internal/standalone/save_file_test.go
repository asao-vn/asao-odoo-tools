package standalone

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// --save-file writes the file an operation returned: a print's report, or the content of the one
// attachment a describe read. The result then names the path instead of carrying the content.
func TestSaveFileWritesTheReturnedDocument(t *testing.T) {
	dir := t.TempDir()
	report := map[string]interface{}{"filename": "INV_2026_00017.pdf", "content": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invoice"))}
	if err := saveReturnedFile(direct.Outcome{Operation: "finance.invoice.print", Data: report}, filepath.Join(dir, "invoice.pdf")); err != nil {
		t.Fatal(err)
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "invoice.pdf")); string(saved) != "%PDF-1.4 invoice" || report["content"] != nil || report["savedTo"] == nil {
		t.Fatalf("saved %q, result %v", saved, report)
	}
	attachment := []interface{}{map[string]interface{}{"name": "hop-dong.txt", "datas": base64.StdEncoding.EncodeToString([]byte("hợp đồng"))}}
	if err := saveReturnedFile(direct.Outcome{Operation: "sales.order.attachment.describe", Data: attachment}, filepath.Join(dir, "hop-dong.txt")); err != nil {
		t.Fatal(err)
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "hop-dong.txt")); string(saved) != "hợp đồng" {
		t.Fatalf("saved %q", saved)
	}
	if err := saveReturnedFile(direct.Outcome{Operation: "sales.quotation.list", Data: []interface{}{map[string]interface{}{"name": "S00001"}}}, filepath.Join(dir, "x")); err == nil {
		t.Fatal("a read with no file saved something")
	}
}
