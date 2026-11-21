package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tlmcguire/aisai/internal/ast"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
)

// FixSchema is the JSON schema the remediation model must conform to.
var FixSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"patch_unified": map[string]any{
			"type":        "string",
			"description": "A unified diff that fixes the vulnerability, or an empty string if no safe patch is possible.",
		},
		"explanation": map[string]any{
			"type":        "string",
			"description": "One or two sentences explaining the change and why it is safe.",
		},
		"confidence": map[string]any{
			"type":        "number",
			"minimum":     0,
			"maximum":     1,
			"description": "How confident you are that the patch fixes the issue without breaking the code.",
		},
	},
	"required":             []string{"patch_unified", "explanation", "confidence"},
	"additionalProperties": false,
}

// Fix is a proposed remediation for one finding.
type Fix struct {
	// Unified is the unified diff. Empty when no safe patch is possible.
	Unified string
	// Explanation says what the change does and why it is safe.
	Explanation string
	// Confidence is the model's confidence in the patch.
	Confidence float64
}

// ProposeFix asks the model for a remediation patch for one finding.
//
// It is the shared implementation behind the app's propose-fix endpoint and
// the MCP propose_fix tool, so both produce the same kind of patch and both
// validate it the same way.
//
// The file content must be the current content of the file the finding is
// in: the model is asked for a diff that applies to it byte-for-byte.
func ProposeFix(ctx context.Context, provider llm.Provider, modelName string, numCtx int, f model.Finding, content string) (*Fix, error) {
	if provider == nil {
		return nil, fmt.Errorf("scan: no LLM provider configured")
	}

	completion, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:  modelName,
		System: FixSystemPromptFor(languageOfFile(f.File)),
		User:   FixUserPrompt(f, content),
		Schema: FixSchema,
		NumCtx: numCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("scan: the remediation request failed: %w", err)
	}
	if completion == nil {
		return nil, fmt.Errorf("scan: the remediation request returned nothing")
	}

	raw := completion.JSON
	if raw == nil {
		// Some providers return the schema-conforming JSON as text.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(completion.Text), &parsed); err != nil {
			return nil, fmt.Errorf("scan: could not parse the remediation response: %w", err)
		}
		raw = parsed
	}

	out := &Fix{}
	if v, ok := raw["patch_unified"].(string); ok {
		out.Unified = v
	}
	if v, ok := raw["explanation"].(string); ok {
		out.Explanation = v
	}
	if v, ok := raw["confidence"].(float64); ok {
		out.Confidence = v
	}
	if out.Unified == "" {
		return out, fmt.Errorf("scan: the model did not produce a patch")
	}
	return out, nil
}

// languageOfFile returns the identifier of the language of a file, judged by
// its extension. It is "python" when the extension is not a known one.
func languageOfFile(path string) string {
	if lang := ast.LanguageForPath(path); lang != nil {
		return lang.ID
	}
	return "python"
}

// FixSystemPrompt is the system prompt used for remediation requests on
// Python code.
func FixSystemPrompt() string {
	return FixSystemPromptFor("python")
}

// FixSystemPromptFor is the system prompt used for remediation requests on
// code in the given language.
//
// The emphasis on a *minimal* change is deliberate. A fix that rewrites more
// than it has to is a fix nobody can review, and a rewrite of a security
// function is a fix that probably breaks it.
func FixSystemPromptFor(language string) string {
	subject := "a Python package"
	if !isPythonID(language) {
		name, _ := promptLanguage(language)
		subject = "a " + name + " project"
	}
	return "You are a security engineer fixing a vulnerability in " + subject + ". " +
		"Produce a minimal unified diff that removes the vulnerability without changing the public behaviour of the code. " +
		"The diff must apply to the file exactly as shown: keep the context lines byte-for-byte and use the real line numbers of the file. " +
		"Return an empty patch if no safe fix is possible; never invent a fix that breaks the code."
}

// FixUserPrompt renders the user prompt of a remediation request.
func FixUserPrompt(f model.Finding, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n", f.File)
	fmt.Fprintf(&b, "CWE: %s\n", f.CWE)
	fmt.Fprintf(&b, "Severity: %s\n", f.Severity)
	fmt.Fprintf(&b, "Finding: %s\n", f.Message)
	if f.Code != "" {
		fmt.Fprintf(&b, "Offending code:\n%s\n", f.Code)
	}
	if f.Evidence.Source != "" || f.Evidence.Sink != "" {
		fmt.Fprintf(&b, "Data flow: %s -> %s\n", f.Evidence.Source, f.Evidence.Sink)
	}
	if f.Remediation != "" {
		fmt.Fprintf(&b, "Scanner remediation hint: %s\n", f.Remediation)
	}
	fmt.Fprintf(&b, "\nThe file starts at line 1; unified diff hunk headers must use these absolute line numbers.\n")
	b.WriteString("\n--- file ---\n")
	b.WriteString(content)
	return b.String()
}
