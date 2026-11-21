package scan

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// normalizeEOL turns CRLF line endings into LF ones.
//
// The goldens are text, and a checkout on Windows rewrites them with CRLF.
// Comparing them byte for byte would fail there for a reason that has
// nothing to do with the prompts.
func normalizeEOL(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// This file holds the prompt regression goldens.
//
// The prompts are the part of the scanner that is hardest to review and
// easiest to change by accident: a word added to the detection prompt changes
// what the model reports, and nobody notices until the numbers move. The
// goldens make such a change a deliberate, visible act: the test fails and
// says what to do.
//
// Each golden holds the prompt byte for byte, with no trailing newline of
// its own. The JSON schemas are goldened the same way, in canonical form.
//
// Regenerate the goldens with:
//
//	go test ./internal/scan -update
//
// and read the diff before committing it.
//
// The goldens hold no model output, no timestamp and no identifier: every
// prompt is a pure function of its inputs, and the test asserts that by
// building each prompt twice and comparing.

// promptUpdate rewrites the prompt goldens instead of comparing against them.
var promptUpdate = flag.Bool("update", false,
	"rewrite the prompt goldens in internal/scan/testdata/prompts")

// promptDir holds the goldens.
const promptDir = "testdata/prompts"

// goldenChunk is the chunk every user-prompt golden is built from.
//
// It is fixed here so that the goldens do not depend on a real audit.
func goldenChunk() model.Chunk {
	return model.Chunk{
		ID:         "c1",
		AuditID:    "a1",
		File:       "pkgs/somepkg-1.0/mod.py",
		StartLine:  10,
		EndLine:    14,
		Type:       model.ChunkFunc,
		SymbolPath: "mod.run",
		Content:    "def run(cmd):\n    os.system(cmd)\n    return cmd\n",
	}
}

// goldenFinding is the finding every verification-prompt golden is built
// from.
func goldenFinding() model.Finding {
	return model.Finding{
		ID:         "f1",
		Rule:       "llm:cwe-78",
		Tool:       "llm",
		CWE:        "CWE-78",
		Severity:   model.SeverityHigh,
		Confidence: 0.9,
		File:       "pkgs/somepkg-1.0/mod.py",
		StartLine:  11,
		EndLine:    11,
		Code:       "os.system(cmd)",
		Message:    "Command injection through os.system",
		Evidence: model.Evidence{
			Source: "request.args",
			Sink:   "os.system",
			Flow:   []string{"cmd"},
		},
		Remediation: "Use subprocess.run with a list of arguments.",
		State:       model.StateOpen,
	}
}

// promptCase is one golden: a name, the text it holds and how to build it.
type promptCase struct {
	// name is the file name of the golden, without the directory.
	name string
	// build returns the text the golden must hold.
	build func() string
}

// promptCases are the goldens.
//
// The few-shot case deliberately supplies four examples: the prompt keeps at
// most three, and the golden pins that truncation.
func promptCases() []promptCase {
	detector := NewDetector(&fakeProvider{}, "test-model", 8192)
	detectorWithFewShot := NewDetector(&fakeProvider{}, "test-model", 8192).WithFewShot([]Example{
		{CWE: "CWE-78", Vulnerable: "os.system(cmd)", Patched: "subprocess.run(['ls'])"},
		{CWE: "CWE-22", Vulnerable: "open(request.args['f'])", Patched: "open(safe_path(request.args['f']))"},
		{CWE: "CWE-502", Vulnerable: "pickle.loads(data)", Patched: "json.loads(data)"},
		{CWE: "CWE-95", Vulnerable: "eval(expr)", Patched: "ast.literal_eval(expr)"},
	})

	return []promptCase{
		{
			name:  "detect_system.txt",
			build: func() string { return detector.detectSystemPrompt() },
		},
		{
			name:  "detect_system_fewshot.txt",
			build: func() string { return detectorWithFewShot.detectSystemPrompt() },
		},
		{
			name:  "detect_user.txt",
			build: func() string { return detector.detectUserPrompt(goldenChunk()) },
		},
		{
			name:  "verify_system.txt",
			build: func() string { return detector.verifySystemPrompt() },
		},
		{
			name:  "verify_user.txt",
			build: func() string { return detector.verifyUserPrompt(goldenChunk(), goldenFinding()) },
		},
		{
			name:  "fix_system.txt",
			build: func() string { return FixSystemPrompt() },
		},
		{
			name:  "fix_user.txt",
			build: func() string { return FixUserPrompt(goldenFinding(), "def run(cmd):\n    os.system(cmd)\n") },
		},
		{
			name: "detection_schema.json",
			build: func() string {
				return canonicalJSON(DetectionSchema)
			},
		},
		{
			name: "verification_schema.json",
			build: func() string {
				return canonicalJSON(VerificationSchema)
			},
		},
		{
			name: "fix_schema.json",
			build: func() string {
				return canonicalJSON(FixSchema)
			},
		},
	}
}

// canonicalJSON renders a schema as indented JSON.
//
// encoding/json sorts the keys of a map, so the rendering of a schema is
// stable; the golden therefore changes only when the schema changes.
func canonicalJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// The schemas are built from literals in this package; a failure here
		// means a literal was made unserializable, which is a bug in its own
		// right. The panic is turned into a test failure by the caller.
		panic(err)
	}
	return string(data)
}

// TestPromptGoldens compares every built prompt with its golden.
func TestPromptGoldens(t *testing.T) {
	for _, c := range promptCases() {
		t.Run(c.name, func(t *testing.T) {
			got := c.build()

			// The prompt must be a pure function of its inputs. A prompt that
			// embeds a timestamp, a random identifier or a map iteration order
			// would make the golden useless.
			if again := c.build(); again != got {
				t.Fatalf("the prompt is not deterministic:\nfirst:\n%s\nsecond:\n%s", got, again)
			}

			path := filepath.Join(promptDir, c.name)

			if *promptUpdate {
				if err := os.MkdirAll(promptDir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", promptDir, err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("rewrote %s", path)
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run: go test ./internal/scan -update)", path, err)
			}
			if normalizeEOL(string(want)) != normalizeEOL(got) {
				t.Errorf("the prompt changed.\n\nwant:\n%s\n\ngot:\n%s\n\nif the change is intended, regenerate the goldens with:\n\n\tgo test ./internal/scan -update\n",
					want, got)
			}
		})
	}
}

// TestPromptGoldensAreNotEmpty guards against a golden that was emptied.
//
// An empty golden is indistinguishable from a prompt that was accidentally
// deleted, and it would turn every later change into a no-op.
func TestPromptGoldensAreNotEmpty(t *testing.T) {
	for _, c := range promptCases() {
		if strings.TrimSpace(c.build()) == "" {
			t.Errorf("the golden %s would be empty", c.name)
		}
	}
}

// TestGoldensHoldNoModelOutput checks that the goldens hold no content that
// could change between two runs.
//
// A prompt golden that held a model answer, a timestamp or a request id
// would fail the next day for no reason, and the people running the tests
// would stop trusting it.
func TestGoldensHoldNoModelOutput(t *testing.T) {
	now := time.Now().UTC().Format("2006-01-02")
	for _, c := range promptCases() {
		body := c.build()
		if strings.Contains(body, now) {
			t.Errorf("the golden %s holds today's date; the prompt embeds a timestamp", c.name)
		}
		for _, marker := range []string{"<|im_start|>", "model output"} {
			if strings.Contains(body, marker) {
				t.Errorf("the golden %s holds %q, which looks like model output", c.name, marker)
			}
		}
	}
}
