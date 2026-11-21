package sast

import (
	"strings"
	"sync"
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// ruleCase is a table-driven rule test: a snippet that must trigger the rule
// and a safe snippet that must not.
type ruleCase struct {
	rule string
	// vuln must trigger the rule.
	vuln string
	// safe must not trigger the rule.
	safe string
}

// ruleCases covers every built-in rule with a triggering and a safe snippet.
var ruleCases = []ruleCase{
	{
		rule: RuleSubprocessShellTrue,
		vuln: "import subprocess\nsubprocess.run(\"ls\", shell=True)\n",
		safe: "import subprocess\nsubprocess.run([\"ls\", \"-l\"])\n",
	},
	{
		rule: RuleEvalExecNonliteral,
		vuln: "def handler():\n    return eval(user_input)\n",
		safe: "def handler():\n    return eval(\"1 + 1\")\n",
	},
	{
		rule: RulePickleYamlUnsafeLoad,
		vuln: "import pickle\npickle.loads(data)\n",
		safe: "import yaml\nyaml.load(data, Loader=yaml.SafeLoader)\n",
	},
	{
		rule: RuleTempfileMktemp,
		vuln: "import tempfile\ntempfile.mktemp()\n",
		safe: "import tempfile\ntempfile.mkstemp()\n",
	},
	{
		rule: RuleWeakHashMD5SHA1,
		vuln: "import hashlib\nhashlib.md5(data)\n",
		safe: "import hashlib\nhashlib.sha256(data)\n",
	},
	{
		rule: RuleHardcodedSecret,
		vuln: "password = \"Xy9!kQ2mLp4vTz\"\n",
		safe: "password = os.environ[\"DB_PASSWORD\"]\n",
	},
	{
		rule: RuleDebugTrueEnabled,
		vuln: "app.run(debug=True)\n",
		safe: "app.run(debug=False)\n",
	},
	{
		rule: RuleAssertUsedForValidation,
		vuln: "def handler():\n    assert request.args[\"x\"] == \"1\"\n",
		safe: "def handler():\n    assert 1 == 1\n",
	},
	{
		rule: RuleTLSVerifyDisabled,
		vuln: "import requests\nrequests.get(url, verify=False)\n",
		safe: "import requests\nrequests.get(url, verify=True)\n",
	},
	{
		rule: RulePathTraversal,
		vuln: "def handler():\n    return open(request.args[\"f\"])\n",
		safe: "def handler():\n    return open(\"static/index.html\")\n",
	},
}

// scan parses a snippet and scans it.
func scan(t *testing.T, src string) []model.Finding {
	t.Helper()
	f, err := ast.ParseString("pkg/x.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	return NewEngine().Scan([]*ast.File{f})
}

// findingsFor returns the findings of a rule.
func findingsFor(findings []model.Finding, rule string) []model.Finding {
	var out []model.Finding
	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func TestRules(t *testing.T) {
	for _, tc := range ruleCases {
		t.Run(tc.rule, func(t *testing.T) {
			got := findingsFor(scan(t, tc.vuln), tc.rule)
			if len(got) == 0 {
				t.Fatalf("rule %s did not fire on the vulnerable snippet", tc.rule)
			}
			for _, f := range got {
				if f.Tool != ToolName {
					t.Errorf("tool = %q, want %q", f.Tool, ToolName)
				}
				if f.CWE == "" {
					t.Error("finding has no CWE")
				}
				if !f.Severity.Valid() {
					t.Errorf("severity %q is not a normalized severity", f.Severity)
				}
				if f.Confidence < 0 || f.Confidence > 1 {
					t.Errorf("confidence %v is out of [0,1]", f.Confidence)
				}
				if f.State != model.StateOpen {
					t.Errorf("state = %q, want %q", f.State, model.StateOpen)
				}
				if f.File != "pkg/x.py" {
					t.Errorf("file = %q, want pkg/x.py", f.File)
				}
				if f.StartLine < 1 {
					t.Errorf("start line = %d, want >= 1", f.StartLine)
				}
				if f.EndLine < f.StartLine {
					t.Errorf("end line %d is before start line %d", f.EndLine, f.StartLine)
				}
				if strings.TrimSpace(f.Code) == "" {
					t.Error("finding has no code snippet")
				}
				if f.Message == "" {
					t.Error("finding has no message")
				}
				if f.ID == "" {
					t.Error("finding has no id")
				}
			}
		})
	}
}

func TestRulesNoFalsePositives(t *testing.T) {
	for _, tc := range ruleCases {
		t.Run(tc.rule, func(t *testing.T) {
			got := findingsFor(scan(t, tc.safe), tc.rule)
			if len(got) != 0 {
				t.Errorf("rule %s fired on the safe snippet: %+v", tc.rule, got)
			}
		})
	}
}

func TestRulesCatalogue(t *testing.T) {
	rules := Rules()
	if len(rules) != len(ruleCases) {
		t.Errorf("catalogue has %d rules, want %d", len(rules), len(ruleCases))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.ID == "" {
			t.Error("rule with empty id")
		}
		if seen[r.ID] {
			t.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if r.Name == "" {
			t.Errorf("rule %s has no name", r.ID)
		}
		if !strings.HasPrefix(r.CWE, "CWE-") {
			t.Errorf("rule %s has CWE %q", r.ID, r.CWE)
		}
		if r.Description == "" {
			t.Errorf("rule %s has no description", r.ID)
		}
		if !r.Severity.Valid() {
			t.Errorf("rule %s has severity %q", r.ID, r.Severity)
		}
		if r.Confidence < 0 || r.Confidence > 1 {
			t.Errorf("rule %s has confidence %v", r.ID, r.Confidence)
		}
	}
	for _, tc := range ruleCases {
		if !seen[tc.rule] {
			t.Errorf("rule %s is missing from the catalogue", tc.rule)
		}
	}

	// The catalogue must be a copy: mutating it cannot affect the engine.
	rules[0].ID = "mutated"
	if Rules()[0].ID == "mutated" {
		t.Error("Rules() returned the backing array")
	}
}

func TestScanIsDeterministicAndSorted(t *testing.T) {
	src := "import subprocess\nimport requests\n\nsubprocess.run(cmd, shell=True)\nrequests.get(u, verify=False)\n"
	f, err := ast.ParseString("pkg/a.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	g, err := ast.ParseString("pkg/b.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	engine := NewEngine()
	first := engine.Scan([]*ast.File{f, g})
	second := engine.Scan([]*ast.File{f, g})
	if len(first) != len(second) {
		t.Fatalf("scan lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("finding %d differs between runs: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
	for i := 1; i < len(first); i++ {
		a, b := first[i-1], first[i]
		if a.File > b.File {
			t.Errorf("findings not sorted by file: %q before %q", a.File, b.File)
			continue
		}
		if a.File == b.File && a.StartLine > b.StartLine {
			t.Errorf("findings not sorted by start line in %q: %d before %d", a.File, a.StartLine, b.StartLine)
		}
	}
}

func TestScanSkipsNilFiles(t *testing.T) {
	if got := NewEngine().Scan([]*ast.File{nil}); len(got) != 0 {
		t.Errorf("got %d findings for a nil file, want 0", len(got))
	}
}

func TestEngineIsConcurrencySafe(t *testing.T) {
	src := "import subprocess\nsubprocess.run(cmd, shell=True)\n"
	f, err := ast.ParseString("pkg/x.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	engine := NewEngine()
	var wg sync.WaitGroup
	results := make([][]model.Finding, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = engine.Scan([]*ast.File{f})
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if len(got) != 1 {
			t.Fatalf("goroutine %d got %d findings, want 1", i, len(got))
		}
	}
}

// TestRuleVariants covers additional shapes of the same rules, so that a
// rule cannot pass on a single snippet.
func TestRuleVariants(t *testing.T) {
	cases := []struct {
		name    string
		rule    string
		src     string
		wantHit bool
	}{
		{"shell true via imported name", RuleSubprocessShellTrue,
			"from subprocess import run\nrun(cmd, shell=True)\n", true},
		{"shell true via check_output", RuleSubprocessShellTrue,
			"import subprocess\nsubprocess.check_output(cmd, shell=True)\n", true},
		{"shell false", RuleSubprocessShellTrue,
			"import subprocess\nsubprocess.run(cmd, shell=False)\n", false},
		{"exec on a variable", RuleEvalExecNonliteral,
			"def f():\n    exec(code)\n", true},
		{"compile on a variable", RuleEvalExecNonliteral,
			"def f():\n    compile(src, 'x', 'eval')\n", true},
		{"compile on a literal", RuleEvalExecNonliteral,
			"compile('1', 'x', 'eval')\n", false},
		{"yaml load without loader", RulePickleYamlUnsafeLoad,
			"import yaml\nyaml.load(data)\n", true},
		{"yaml safe_load", RulePickleYamlUnsafeLoad,
			"import yaml\nyaml.safe_load(data)\n", false},
		{"pickle load", RulePickleYamlUnsafeLoad,
			"import pickle\nwith open(p, 'rb') as fh:\n    pickle.load(fh)\n", true},
		{"hashlib usedforsecurity false", RuleWeakHashMD5SHA1,
			"import hashlib\nhashlib.md5(data, usedforsecurity=False)\n", false},
		{"sha1", RuleWeakHashMD5SHA1,
			"import hashlib\nhashlib.sha1(data)\n", true},
		{"debug flag assignment", RuleDebugTrueEnabled,
			"DEBUG = True\n", true},
		{"debug flag false", RuleDebugTrueEnabled,
			"DEBUG = False\n", false},
		{"app.debug attribute", RuleDebugTrueEnabled,
			"app.debug = True\n", true},
		{"assert without request source", RuleAssertUsedForValidation,
			"def f():\n    assert value == 1\n", false},
		{"assert with request source", RuleAssertUsedForValidation,
			"def f():\n    assert request.form.get('x')\n", true},
		{"httpx verify false", RuleTLSVerifyDisabled,
			"import httpx\nhttpx.get(url, verify=False)\n", true},
		{"urllib verify false", RuleTLSVerifyDisabled,
			"import urllib.request\nurllib.request.urlopen(url, verify=False)\n", true},
		{"verify true", RuleTLSVerifyDisabled,
			"import requests\nrequests.get(url, verify=True)\n", false},
		{"path from tainted variable", RulePathTraversal,
			"def h():\n    name = request.args['f']\n    return open(name)\n", true},
		{"path from constant", RulePathTraversal,
			"def h():\n    return open('/etc/passwd')\n", false},
		{"send_file with request parameter", RulePathTraversal,
			"def h():\n    return send_file(request.form['f'])\n", true},
		{"secret with low entropy", RuleHardcodedSecret,
			"password = \"aaaaaaaa\"\n", false},
		{"secret with short value", RuleHardcodedSecret,
			"password = \"abc\"\n", false},
		{"non secret name", RuleHardcodedSecret,
			"username = \"Xy9!kQ2mLp4vTz\"\n", false},
		{"secret name with entropy", RuleHardcodedSecret,
			"api_key = \"Xy9!kQ2mLp4vTz\"\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findingsFor(scan(t, tc.src), tc.rule)
			if tc.wantHit && len(got) == 0 {
				t.Errorf("rule %s did not fire", tc.rule)
			}
			if !tc.wantHit && len(got) != 0 {
				t.Errorf("rule %s fired unexpectedly: %+v", tc.rule, got)
			}
		})
	}
}
