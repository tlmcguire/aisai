package sast

import (
	"testing"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/model"
)

// scanSource scans a snippet and returns the findings.
func scanSource(t *testing.T, src string) []model.Finding {
	t.Helper()
	f, err := ast.ParseString("pkg/x.py", src)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	return NewEngine().Scan([]*ast.File{f})
}

// findingFor returns the first finding of the given rule.
func findingFor(t *testing.T, findings []model.Finding, rule string) model.Finding {
	t.Helper()
	for _, f := range findings {
		if f.Rule == rule {
			return f
		}
	}
	t.Fatalf("no finding for rule %q in %d findings", rule, len(findings))
	return model.Finding{}
}

// TestReachabilityIsReachableWhenTheSinkSeesRequestData is the positive
// case: the sink is inside a function that assigns request data to a local
// name before reaching the sink.
func TestReachabilityIsReachableWhenTheSinkSeesRequestData(t *testing.T) {
	src := `import subprocess

def handler(request):
    cmd = request.args.get("cmd")
    subprocess.run(cmd, shell=True)
`
	f := findingFor(t, scanSource(t, src), RuleSubprocessShellTrue)
	if f.Reachability != model.ReachReachable {
		t.Errorf("reachability = %q, want %q", f.Reachability, model.ReachReachable)
	}
}

// TestReachabilityIsUnknownWithoutRequestData checks the negative case. The
// engine must not claim a finding is unreachable: a helper with no request
// parameter may still be called from one that has.
func TestReachabilityIsUnknownWithoutRequestData(t *testing.T) {
	src := `import subprocess

def helper(cmd):
    subprocess.run(cmd, shell=True)
`
	f := findingFor(t, scanSource(t, src), RuleSubprocessShellTrue)
	if f.Reachability != model.ReachUnknown {
		t.Errorf("reachability = %q, want %q", f.Reachability, model.ReachUnknown)
	}
}

// TestReachabilityIsUnknownAtModuleLevel covers a sink outside any function.
func TestReachabilityIsUnknownAtModuleLevel(t *testing.T) {
	src := `import subprocess

subprocess.run(cmd, shell=True)
`
	f := findingFor(t, scanSource(t, src), RuleSubprocessShellTrue)
	if f.Reachability != model.ReachUnknown {
		t.Errorf("reachability = %q, want %q", f.Reachability, model.ReachUnknown)
	}
}

// TestReachabilityIgnoresATaintAfterTheSink checks the line precision: a
// request-derived value assigned after the sink does not make the sink
// reachable.
func TestReachabilityIgnoresATaintAfterTheSink(t *testing.T) {
	src := `import subprocess

def handler(request):
    subprocess.run(cmd, shell=True)
    cmd = request.args.get("cmd")
`
	f := findingFor(t, scanSource(t, src), RuleSubprocessShellTrue)
	if f.Reachability != model.ReachUnknown {
		t.Errorf("reachability = %q, want %q for a taint that appears after the sink",
			f.Reachability, model.ReachUnknown)
	}
}

// TestReachabilityFollowsTheScopeChain checks that a taint in an enclosing
// scope is seen from a nested function.
func TestReachabilityFollowsTheScopeChain(t *testing.T) {
	src := `import subprocess

def handler(request):
    cmd = request.args.get("cmd")

    def inner():
        subprocess.run(cmd, shell=True)

    return inner
`
	f := findingFor(t, scanSource(t, src), RuleSubprocessShellTrue)
	if f.Reachability != model.ReachReachable {
		t.Errorf("reachability = %q, want %q", f.Reachability, model.ReachReachable)
	}
}

// TestReachabilityIsAlwaysSet checks that every finding carries a
// reachability value, so the UI never has to guess.
func TestReachabilityIsAlwaysSet(t *testing.T) {
	src := `import subprocess, pickle, yaml

def handler(request):
    data = request.args.get("x")
    subprocess.run(data, shell=True)
    pickle.loads(data)
    yaml.load(data)

module_level = 1
`
	findings := scanSource(t, src)
	if len(findings) == 0 {
		t.Fatal("no findings")
	}
	for _, f := range findings {
		switch f.Reachability {
		case model.ReachReachable, model.ReachUnreachable, model.ReachUnknown:
		default:
			t.Errorf("finding %s has reachability %q, which is not a known value", f.ID, f.Reachability)
		}
	}
}
