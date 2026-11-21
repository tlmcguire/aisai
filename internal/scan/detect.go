// Package scan implements the LLM-backed vulnerability detection pipeline.
//
// The pipeline is deliberately two-stage:
//
//	Stage 1 — DETECT:  read a chunk and propose candidate findings.
//	Stage 2 — VERIFY:  try to disprove each candidate.
//
// A candidate that cannot be verified is dropped rather than downgraded.
// This is the single highest-value design decision in the scanner: the
// published research consistently finds that LLM scanners produce far more
// false positives than true positives on safe code, and that "try to
// disprove it" is the mechanism that separates the two.
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/injection"
	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
)

// ErrQuarantined is returned when a chunk was withheld from the model
// because its content looks like an attempt to steer the model.
//
// Use errors.Is to detect it.
var ErrQuarantined = errors.New("scan: chunk quarantined")

// QuarantineError reports that a chunk was withheld from the model.
//
// The audited package is attacker-controlled, so a chunk whose content tries
// to steer the model is never sent to it. This is the same quarantine the
// chat path applies: the detection prompt already tells the model to treat
// code as data, but a prompt instruction is a soft defence and a withheld
// chunk is a hard one.
type QuarantineError struct {
	// Chunk is the chunk that was withheld.
	Chunk model.Chunk
	// Pattern is the name of the detector that matched.
	Pattern string
}

// Error implements the error interface.
func (e *QuarantineError) Error() string {
	return fmt.Sprintf("scan: %s withheld from the model (%s)", e.Chunk.File, e.Pattern)
}

// Unwrap makes errors.Is(err, ErrQuarantined) work.
func (e *QuarantineError) Unwrap() error { return ErrQuarantined }

// quarantineChunk reports whether a chunk must be withheld from the model.
func quarantineChunk(chunk model.Chunk) (string, bool) {
	return injection.Detect(chunk.Content + "\n" + chunk.Docstring)
}

// PromptVersion identifies the prompt set. It is recorded on every finding,
// so a change in prompting is visible in the results and a re-scan can be
// compared against an older one.
const PromptVersion = "2026.11.1"

// Tool names used for the detection and verification prompts.
const (
	toolNameDetect  = "report_findings"
	toolNameVerify  = "verify_finding"
	toolNameAbstain = "abstain"
)

// DetectionSchema is the JSON schema the detection model must conform to.
//
// Every field here exists because the product needs it: CWE makes findings
// joinable, severity and confidence make them triageable, and the patch
// makes them actionable.
var DetectionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"abstained": map[string]any{
			"type":        "boolean",
			"description": "True when the code contains no genuine vulnerability worth reporting.",
		},
		"findings": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwe_id": map[string]any{
						"type":        "string",
						"description": "The CWE identifier, e.g. CWE-798. Use the most specific CWE that applies.",
					},
					"severity": map[string]any{
						"type":        "string",
						"enum":        []string{"critical", "high", "medium", "low"},
						"description": "How serious the issue is if exploited.",
					},
					"confidence": map[string]any{
						"type":        "number",
						"minimum":     0,
						"maximum":     1,
						"description": "Your confidence that this is a real, exploitable issue. Use a low value if you are unsure.",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "1-based line where the problem starts.",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "1-based line where the problem ends.",
					},
					"code_snippet": map[string]any{
						"type":        "string",
						"description": "The offending source lines, verbatim.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "One sentence describing the vulnerability.",
					},
					"evidence": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"source": map[string]any{"type": "string", "description": "Where untrusted data enters."},
							"sink":   map[string]any{"type": "string", "description": "Where untrusted data is used dangerously."},
							"flow":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
					"remediation": map[string]any{
						"type":        "string",
						"description": "How to fix it, in one or two sentences.",
					},
					"patch_unified": map[string]any{
						"type":        "string",
						"description": "A unified diff that fixes the issue. Empty string if you cannot produce a safe patch.",
					},
				},
				"required": []string{
					"cwe_id", "severity", "confidence", "start_line", "end_line",
					"code_snippet", "message", "evidence", "remediation", "patch_unified",
				},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"abstained", "findings"},
	"additionalProperties": false,
}

// VerificationSchema is the JSON schema the verification model must conform to.
var VerificationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"verdict": map[string]any{
			"type":        "string",
			"enum":        []string{"confirmed", "plausible", "false_positive"},
			"description": "confirmed: you can show the flaw is real. plausible: it may be real but you cannot prove it here. false_positive: the claim is wrong.",
		},
		"reason": map[string]any{
			"type":        "string",
			"description": "Why you reached this verdict, referring to specific lines.",
		},
		"confidence": map[string]any{
			"type":    "number",
			"minimum": 0,
			"maximum": 1,
		},
	},
	"required":             []string{"verdict", "reason", "confidence"},
	"additionalProperties": false,
}

// Detector runs detection and verification against an LLM.
type Detector struct {
	provider llm.Provider
	// model resolves the model name on every request, so the model picker
	// can switch models while the server is running.
	model  func() string
	numCtx int
	think  bool
	// FewShot holds optional example pairs used to prime the model.
	FewShot []Example
	// VerifyFindings enables the second stage. It should be on except in
	// benchmarks that specifically measure a single-pass detector.
	VerifyFindings bool
}

// Example is a labelled code sample used for few-shot prompting.
type Example struct {
	// CWE is the classification of the example.
	CWE string
	// Vulnerable is the vulnerable variant.
	Vulnerable string
	// Patched is the fixed variant.
	Patched string
}

// NewDetector creates a detector that always uses the given model.
func NewDetector(provider llm.Provider, model string, numCtx int) *Detector {
	return newDetector(provider, func() string { return model }, numCtx)
}

// NewDetectorWithModels creates a detector that resolves its model from the
// model set on every request.
//
// The model set is shared with the rest of the server, so picking a
// different detection model in the desktop app takes effect on the next
// chunk without a restart.
func NewDetectorWithModels(provider llm.Provider, models *llm.Models, numCtx int) *Detector {
	if models == nil {
		return newDetector(provider, func() string { return "" }, numCtx)
	}
	return newDetector(provider, models.Scan, numCtx)
}

// newDetector builds a detector around a model resolver.
func newDetector(provider llm.Provider, model func() string, numCtx int) *Detector {
	if numCtx <= 0 {
		numCtx = llm.DefaultNumCtx
	}
	return &Detector{
		provider:       provider,
		model:          model,
		numCtx:         numCtx,
		VerifyFindings: true,
	}
}

// modelName returns the model the detector uses right now.
func (d *Detector) modelName() string {
	if d.model == nil {
		return ""
	}
	return d.model()
}

// WithFewShot attaches few-shot examples.
func (d *Detector) WithFewShot(examples []Example) *Detector {
	d.FewShot = examples
	return d
}

// Detect analyses one chunk and returns candidate findings.
//
// It returns an empty slice when the model abstains, when the chunk is
// empty, or when the model returns something that does not conform to the
// schema.
func (d *Detector) Detect(ctx context.Context, chunk model.Chunk) ([]model.Finding, error) {
	if d.provider == nil {
		return nil, fmt.Errorf("scan: no LLM provider configured")
	}
	if strings.TrimSpace(chunk.Content) == "" {
		return nil, nil
	}

	// A chunk that tries to steer the model never reaches it. The detection
	// prompt tells the model to treat code as data, but withholding the
	// chunk is a guarantee the prompt cannot give.
	if pattern, hit := quarantineChunk(chunk); hit {
		return nil, &QuarantineError{Chunk: chunk, Pattern: pattern}
	}

	req := llm.CompletionRequest{
		Model:       d.modelName(),
		System:      d.detectSystemPrompt(),
		User:        d.detectUserPrompt(chunk),
		Schema:      DetectionSchema,
		NumCtx:      d.numCtx,
		Temperature: 0,
		Seed:        llm.DefaultSeed,
		Think:       d.think,
	}

	completion, err := d.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("scan: detection failed: %w", err)
	}
	if completion == nil {
		return nil, fmt.Errorf("scan: detection returned nothing")
	}

	raw := completion.JSON
	if raw == nil {
		// Some providers return the schema-conforming JSON as text.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(completion.Text), &parsed); err != nil {
			return nil, fmt.Errorf("scan: could not parse the detection response: %w", err)
		}
		raw = parsed
	}

	findings, err := parseDetection(raw, chunk)
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// Verify checks a candidate finding.
//
// The model is asked to try to disprove it. A finding that is judged a false
// positive is dropped; a finding that cannot be disproved is kept, but its
// confidence is reduced and it is marked as unverified.
func (d *Detector) Verify(ctx context.Context, chunk model.Chunk, f model.Finding) (model.Finding, bool, error) {
	if d.provider == nil {
		return f, false, fmt.Errorf("scan: no LLM provider configured")
	}

	// The verification stage also sends the chunk, so it needs the same
	// quarantine the detection stage has.
	if pattern, hit := quarantineChunk(chunk); hit {
		return f, false, &QuarantineError{Chunk: chunk, Pattern: pattern}
	}

	req := llm.CompletionRequest{
		Model:       d.modelName(),
		System:      d.verifySystemPrompt(),
		User:        d.verifyUserPrompt(chunk, f),
		Schema:      VerificationSchema,
		NumCtx:      d.numCtx,
		Temperature: 0,
		Seed:        llm.DefaultSeed,
		Think:       d.think,
	}

	completion, err := d.provider.Complete(ctx, req)
	if err != nil {
		return f, false, fmt.Errorf("scan: verification failed: %w", err)
	}
	if completion == nil {
		return f, false, fmt.Errorf("scan: verification returned nothing")
	}

	raw := completion.JSON
	if raw == nil {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(completion.Text), &parsed); err != nil {
			return f, false, fmt.Errorf("scan: could not parse the verification response: %w", err)
		}
		raw = parsed
	}

	verdict, _ := raw["verdict"].(string)
	reason, _ := raw["reason"].(string)
	conf, _ := raw["confidence"].(float64)

	switch strings.ToLower(verdict) {
	case "false_positive":
		f.VerifierNote = reason
		f.Verified = false
		return f, false, nil
	case "confirmed":
		f.Verified = true
		f.VerifierNote = reason
		if conf > f.Confidence {
			f.Confidence = conf
		}
		return f, true, nil
	default:
		// "plausible" — keep it, but lower the confidence to reflect that it
		// could not be proven.
		f.Verified = false
		f.VerifierNote = reason
		f.Confidence = f.Confidence * 0.6
		return f, true, nil
	}
}

// DetectAndVerify runs the full two-stage pipeline over one chunk.
func (d *Detector) DetectAndVerify(ctx context.Context, chunk model.Chunk) ([]model.Finding, error) {
	candidates, err := d.Detect(ctx, chunk)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 || !d.VerifyFindings {
		return candidates, nil
	}

	out := make([]model.Finding, 0, len(candidates))
	for _, c := range candidates {
		verified, keep, err := d.Verify(ctx, chunk, c)
		if err != nil {
			// A verification failure must not silently drop the finding;
			// keep it, unverified, so the user can still see it.
			verified.Verified = false
			verified.VerifierNote = "verification failed: " + err.Error()
			out = append(out, verified)
			continue
		}
		if !keep {
			continue
		}
		out = append(out, verified)
	}
	return out, nil
}

// parseDetection converts the model's JSON into findings.
func parseDetection(raw map[string]any, chunk model.Chunk) ([]model.Finding, error) {
	if abstained, ok := raw["abstained"].(bool); ok && abstained {
		return nil, nil
	}
	list, _ := raw["findings"].([]any)
	out := make([]model.Finding, 0, len(list))

	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		cwe, _ := obj["cwe_id"].(string)
		severity, _ := obj["severity"].(string)
		confidence, _ := obj["confidence"].(float64)
		startLine := intFrom(obj["start_line"])
		endLine := intFrom(obj["end_line"])
		snippet, _ := obj["code_snippet"].(string)
		message, _ := obj["message"].(string)
		remediation, _ := obj["remediation"].(string)
		patch, _ := obj["patch_unified"].(string)

		if strings.TrimSpace(message) == "" {
			continue
		}
		if severity == "" || !model.Severity(severity).Valid() {
			severity = string(model.SeverityMedium)
		}
		if confidence <= 0 || confidence > 1 {
			confidence = 0.5
		}
		if endLine < startLine {
			endLine = startLine
		}

		// Keep the model's line numbers but clamp them into the chunk: a
		// hallucinated line number must not point outside the file.
		if startLine > 0 && chunk.EndLine > 0 {
			if startLine < chunk.StartLine {
				startLine = chunk.StartLine
			}
			if startLine > chunk.EndLine {
				startLine = chunk.EndLine
			}
			if endLine > chunk.EndLine {
				endLine = chunk.EndLine
			}
			if endLine < startLine {
				endLine = startLine
			}
		}

		evidence := model.Evidence{}
		if ev, ok := obj["evidence"].(map[string]any); ok {
			evidence.Source, _ = ev["source"].(string)
			evidence.Sink, _ = ev["sink"].(string)
			if flow, ok := ev["flow"].([]any); ok {
				for _, f := range flow {
					if s, ok := f.(string); ok {
						evidence.Flow = append(evidence.Flow, s)
					}
				}
			}
		}

		f := model.Finding{
			Rule:        "llm:" + strings.ToLower(cwe),
			Tool:        "llm",
			CWE:         strings.ToUpper(cwe),
			Severity:    model.Severity(severity),
			Confidence:  confidence,
			File:        chunk.File,
			StartLine:   startLine,
			EndLine:     endLine,
			Code:        snippet,
			Message:     message,
			Evidence:    evidence,
			Remediation: remediation,
			Patch:       patch,
			State:       model.StateOpen,
		}
		f.ID = model.FindingID(f.File, f.StartLine, f.CWE, f.Rule)
		out = append(out, f)
	}
	return out, nil
}

func intFrom(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// Timeout returns a sensible per-request timeout for a scan step.
func Timeout() time.Duration { return 180 * time.Second }
