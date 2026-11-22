package findings

import (
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/model"
)

func TestWriteHTMLReportEscapesAndOrders(t *testing.T) {
	list := []model.Finding{
		{Rule: "low-one", Severity: "low", File: "a.py", StartLine: 1, EndLine: 1, Message: "m"},
		{Rule: "crit<script>", Severity: "critical", File: "b.py", StartLine: 2, EndLine: 4, Message: "x", Code: "<b>boom</b>", Confidence: 0.9},
	}
	data, err := WriteHTMLReport(list, ReportOptions{Package: "demo", Version: "1.0", AuditID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, "<script>") || strings.Contains(out, "<b>boom") {
		t.Fatal("report must escape finding text")
	}
	if strings.Index(out, "crit&lt;script&gt;") > strings.Index(out, "low-one") {
		t.Fatal("critical findings must come first")
	}
	if !strings.Contains(out, "demo") || !strings.Contains(out, "90%") {
		t.Fatalf("missing title or confidence: %s", out[:200])
	}
}
