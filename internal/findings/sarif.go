package findings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tlmcguire/aisai/internal/model"
)

// SARIF 2.1.0 types. Only the fields AISAI populates are modelled; the
// document is valid SARIF because unset fields are simply absent.

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name,omitempty"`
	ShortDescription     sarifText              `json:"shortDescription"`
	FullDescription      sarifText              `json:"fullDescription"`
	HelpURI              string                 `json:"helpUri,omitempty"`
	Properties           map[string]interface{} `json:"properties,omitempty"`
	DefaultConfiguration sarifRuleConfig        `json:"defaultConfiguration"`
}

type sarifRuleConfig struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID              string                 `json:"ruleId"`
	RuleIndex           int                    `json:"ruleIndex"`
	Level               string                 `json:"level"`
	Message             sarifText              `json:"message"`
	Locations           []sarifLocation        `json:"locations,omitempty"`
	PartialFingerprints map[string]string      `json:"partialFingerprints,omitempty"`
	Properties          map[string]interface{} `json:"properties,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine,omitempty"`
}

// SARIFOptions configures SARIF generation.
type SARIFOptions struct {
	// ToolName and ToolVersion identify the producer.
	ToolName    string
	ToolVersion string
	// InformationURI points at the tool's documentation.
	InformationURI string
}

// WriteSARIF renders findings as a SARIF 2.1.0 document.
//
// SARIF is the lingua franca for code-scanning output: GitHub code scanning,
// GitLab, DefectDojo and most dashboards consume it. Emitting it is the
// cheapest way to make AISAI's results usable outside the app.
func WriteSARIF(fs []model.Finding, opts SARIFOptions) ([]byte, error) {
	if opts.ToolName == "" {
		opts.ToolName = "AISAI"
	}
	if opts.ToolVersion == "" {
		opts.ToolVersion = "0.4.0"
	}
	if opts.InformationURI == "" {
		opts.InformationURI = "https://github.com/tlmcguire/aisai"
	}

	// Group rules so each distinct rule appears once in tool.driver.rules.
	ruleIndex := map[string]int{}
	var rules []sarifRule
	seenRule := map[string]bool{}

	results := make([]sarifResult, 0, len(fs))

	// Deterministic ordering: severity, then file, then line.
	sorted := append([]model.Finding(nil), fs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Severity.Rank() != b.Severity.Rank() {
			return a.Severity.Rank() > b.Severity.Rank()
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.Rule < b.Rule
	})

	for _, f := range sorted {
		ruleKey := f.Rule
		if ruleKey == "" {
			ruleKey = "unknown"
		}
		idx, ok := ruleIndex[ruleKey]
		if !ok {
			idx = len(rules)
			ruleIndex[ruleKey] = idx
			r := sarifRule{
				ID:                   ruleKey,
				Name:                 ruleKey,
				ShortDescription:     sarifText{Text: firstNonEmpty(f.Message, ruleKey)},
				FullDescription:      sarifText{Text: firstNonEmpty(f.Remediation, f.Message, ruleKey)},
				HelpURI:              opts.InformationURI,
				DefaultConfiguration: sarifRuleConfig{Level: sarifLevel(f.Severity)},
				Properties: map[string]interface{}{
					"tags": []string{"security"},
				},
			}
			if f.CWE != "" {
				r.Properties["cwe"] = f.CWE
			}
			if f.Tool != "" {
				r.Properties["tool"] = f.Tool
			}
			rules = append(rules, r)
			seenRule[ruleKey] = true
		}

		res := sarifResult{
			RuleID:    ruleKey,
			RuleIndex: idx,
			Level:     sarifLevel(f.Severity),
			Message:   sarifText{Text: firstNonEmpty(f.Message, ruleKey)},
			PartialFingerprints: map[string]string{
				"aisaiFindingId/v1": f.ID,
			},
			Properties: map[string]interface{}{
				"confidence": f.Confidence,
				"severity":   string(f.Severity),
				"state":      f.State,
				"verified":   f.Verified,
			},
		}
		if f.CWE != "" {
			res.Properties["cwe"] = f.CWE
		}
		if f.Reachability != "" {
			res.Properties["reachability"] = f.Reachability
		}
		if f.Patch != "" {
			res.Properties["patch"] = f.Patch
		}
		if f.Remediation != "" {
			res.Properties["remediation"] = f.Remediation
		}
		if f.Package != "" {
			res.Properties["package"] = string(f.Package)
		}
		if f.StartLine > 0 {
			res.Locations = []sarifLocation{{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: model.RelPath(f.File)},
					Region:           &sarifRegion{StartLine: f.StartLine, EndLine: f.EndLine},
				},
			}}
		}
		results = append(results, res)
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool:    sarifTool{Driver: sarifDriver{Name: opts.ToolName, Version: opts.ToolVersion, InformationURI: opts.InformationURI, Rules: rules}},
			Results: results,
		}},
	}
	if log.Runs[0].Results == nil {
		log.Runs[0].Results = []sarifResult{}
	}
	return json.MarshalIndent(log, "", "  ")
}

// sarifLevel maps AISAI severity onto the four SARIF levels.
func sarifLevel(s model.Severity) string {
	switch s {
	case model.SeverityCritical, model.SeverityHigh:
		return "error"
	case model.SeverityMedium:
		return "warning"
	case model.SeverityLow, model.SeverityInfo:
		return "note"
	default:
		return "none"
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// WriteJSON renders findings as a plain JSON array, for debugging and for
// tools that do not speak SARIF.
func WriteJSON(fs []model.Finding) ([]byte, error) {
	if fs == nil {
		fs = []model.Finding{}
	}
	return json.MarshalIndent(fs, "", "  ")
}

// ValidateSARIF performs a minimal structural check of a SARIF document.
// It is intentionally not a full schema validation; it catches the mistakes
// this package could plausibly make.
func ValidateSARIF(data []byte) error {
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return fmt.Errorf("sarif is not valid JSON: %w", err)
	}
	if log.Version != "2.1.0" {
		return fmt.Errorf("sarif version = %q, want 2.1.0", log.Version)
	}
	if len(log.Runs) == 0 {
		return fmt.Errorf("sarif has no runs")
	}
	driver := log.Runs[0].Tool.Driver
	if driver.Name == "" {
		return fmt.Errorf("sarif tool.driver.name is empty")
	}
	for i, r := range log.Runs[0].Results {
		if r.RuleID == "" {
			return fmt.Errorf("sarif result %d has an empty ruleId", i)
		}
		if r.RuleIndex < 0 || r.RuleIndex >= len(driver.Rules) {
			return fmt.Errorf("sarif result %d has ruleIndex %d out of range", i, r.RuleIndex)
		}
		switch r.Level {
		case "error", "warning", "note", "none":
		default:
			return fmt.Errorf("sarif result %d has invalid level %q", i, r.Level)
		}
	}
	return nil
}
