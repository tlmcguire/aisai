package sast

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/findings"
	"github.com/tlmcguire/aisai/internal/model"
)

// This file holds the fuzz targets for the static engine and for the SARIF
// document it feeds.
//
// The engine runs over attacker-controlled source, so it must survive
// arbitrary input. The SARIF document is what the user and the CI systems
// consume, so it must always be a valid SARIF 2.1.0 document: a malformed
// export is a broken result even when the scan itself was fine.

// sarifSeeds are the interesting starting points.
var sarifSeeds = []string{
	// Empty input.
	"",
	// A file that triggers several rules.
	"import subprocess, hashlib, yaml\ndef run(cmd):\n    subprocess.run(cmd, shell=True)\n    hashlib.md5(b'x')\n    yaml.load(data)\n",
	// A file with a syntax error.
	"def broken(:\n    return\n",
	// A file with unbalanced quotes.
	"x = 'unterminated\ny = \"also unterminated\n",
	// A file with a byte order mark and CRLF line endings.
	"\ufeffimport os\r\nos.system('ls')\r\n",
	// A file that is only a comment.
	"# nothing to see here\n",
	// A file with deeply nested brackets.
	"x = " + strings.Repeat("(", 50) + "1" + strings.Repeat(")", 50) + "\n",
	// A file with a very long line.
	"x = '" + strings.Repeat("a", 4096) + "'\n",
	// A file with a null byte and control characters.
	"x = 1\x00\ny = '\x01\x02'\n",
	// A file with a hardcoded secret.
	"password = \"Xy9!kQ2mLp4vTz\"\n",
}

// FuzzSARIF fuzzes the whole static path: arbitrary bytes are parsed as
// Python, scanned by the rule engine, and exported as SARIF.
//
// It asserts that the engine never panics, that the findings stay bounded by
// the size of the input, and that the exported document is always valid SARIF
// with the fields a consumer needs.
func FuzzSARIF(f *testing.F) {
	for _, seed := range sarifSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if !utf8.Valid(data) {
			// The parser rejects non-UTF-8 input on purpose.
			return
		}
		source := string(data)

		file, err := ast.ParseString("fuzz.py", source)
		if file == nil {
			// The source could not be parsed at all: nothing to scan.
			return
		}
		// A syntax error is not fatal: the parser returns what it recovered
		// together with the error, and the engine scans that.

		engine := NewEngine()
		found := engine.Scan([]*ast.File{file})

		// The number of findings is bounded by the number of rules times the
		// number of syntactic events, which is bounded by the input.
		inputLines := strings.Count(source, "\n") + 1
		if len(found) > len(Rules())*(inputLines+1) {
			t.Fatalf("the engine produced %d findings for %d lines of input", len(found), inputLines)
		}

		sarifBytes, err := findings.WriteSARIF(found, findings.SARIFOptions{
			ToolName:    "AISAI",
			ToolVersion: "fuzz",
		})
		if err != nil {
			t.Fatalf("WriteSARIF: %v", err)
		}

		assertValidSARIF(t, sarifBytes, len(found))
	})
}

// FuzzSARIFFindings fuzzes the SARIF emitter directly with arbitrary text in
// every field of a finding.
//
// The emitter takes strings that came out of an attacker-controlled package,
// so it must never produce a document that is not valid JSON, and it must
// never lose a result.
func FuzzSARIFFindings(f *testing.F) {
	f.Add([]byte("rule"), []byte("CWE-78"), []byte("message"), []byte("pkg/mod.py"), []byte("high"), []byte("remediation"))

	f.Fuzz(func(t *testing.T, rule, cwe, message, file, severity, remediation []byte) {
		found := []model.Finding{{
			ID:          model.FindingID(string(file), 1, string(cwe), string(rule)),
			Rule:        string(rule),
			Tool:        ToolName,
			CWE:         string(cwe),
			Severity:    model.Severity(string(severity)),
			Confidence:  0.5,
			File:        string(file),
			StartLine:   1,
			EndLine:     1,
			Message:     string(message),
			Remediation: string(remediation),
			State:       model.StateOpen,
		}}

		data, err := findings.WriteSARIF(found, findings.SARIFOptions{})
		if err != nil {
			t.Fatalf("WriteSARIF: %v", err)
		}
		assertValidSARIF(t, data, 1)
	})
}

// assertValidSARIF checks that the document is valid JSON, that it carries
// the fields a SARIF consumer needs, and that it holds the expected number of
// results.
func assertValidSARIF(t *testing.T, data []byte, wantResults int) {
	t.Helper()

	if err := findings.ValidateSARIF(data); err != nil {
		t.Fatalf("the exported SARIF is not valid: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the exported SARIF is not valid JSON: %v", err)
	}

	if version, _ := doc["version"].(string); version != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", doc["version"])
	}
	runs, ok := doc["runs"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatalf("the exported SARIF has no runs")
	}
	run, ok := runs[0].(map[string]any)
	if !ok {
		t.Fatalf("the first run is not an object")
	}
	tool, ok := run["tool"].(map[string]any)
	if !ok {
		t.Fatalf("the first run has no tool")
	}
	driver, ok := tool["driver"].(map[string]any)
	if !ok {
		t.Fatalf("the tool has no driver")
	}
	if name, _ := driver["name"].(string); name == "" {
		t.Error("tool.driver.name is empty")
	}
	results, ok := run["results"].([]any)
	if !ok {
		t.Fatalf("the first run has no results array")
	}
	if len(results) != wantResults {
		t.Errorf("the document holds %d results, want %d", len(results), wantResults)
	}
}

// FuzzEngineScan fuzzes the rule engine on its own.
//
// It asserts that the engine never panics and that it is deterministic: the
// same source always produces the same findings in the same order. A scanner
// whose output changed between two runs over the same file would make every
// golden snapshot and every benchmark number meaningless.
func FuzzEngineScan(f *testing.F) {
	for _, seed := range sarifSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if !utf8.Valid(data) {
			return
		}
		file, _ := ast.ParseString("fuzz.py", string(data))
		if file == nil {
			return
		}

		engine := NewEngine()
		first := engine.Scan([]*ast.File{file})
		second := engine.Scan([]*ast.File{file})

		if len(first) != len(second) {
			t.Fatalf("the engine is not deterministic: %d findings then %d", len(first), len(second))
		}
		for i := range first {
			a, b := first[i], second[i]
			if a.Rule != b.Rule || a.File != b.File || a.StartLine != b.StartLine ||
				a.CWE != b.CWE || a.Severity != b.Severity || a.Message != b.Message {
				t.Fatalf("the engine is not deterministic for finding %d: %+v then %+v", i, a, b)
			}
		}

		// The findings are sorted by file, then start line, then rule.
		for i := 1; i < len(first); i++ {
			prev, cur := first[i-1], first[i]
			if prev.File > cur.File {
				t.Fatalf("the findings are not sorted by file: %q then %q", prev.File, cur.File)
			}
			if prev.File == cur.File && prev.StartLine > cur.StartLine {
				t.Fatalf("the findings are not sorted by line: %d then %d", prev.StartLine, cur.StartLine)
			}
		}
	})
}
