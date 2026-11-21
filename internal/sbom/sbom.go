// Package sbom writes software bill of materials documents.
//
// A supply-chain tool that cannot emit an SBOM is a little odd: the whole
// point is that a consumer can take the output somewhere else. This package
// produces CycloneDX 1.5 and SPDX 2.3 JSON documents from an audit's
// dependency graph and its findings.
package sbom

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/model"
)

// Options configures an SBOM document.
type Options struct {
	// ToolVersion is stamped into the document metadata.
	ToolVersion string
	// ToolName is the name recorded as the producer. Defaults to "AISAI".
	ToolName string
	// Timestamp is the document creation time. Zero means "now".
	Timestamp time.Time
	// AuditID identifies the audit the document was produced from.
	AuditID string
	// PackageName and PackageVersion identify the audited package.
	PackageName    string
	PackageVersion string
}

// component is one package in the SBOM.
type component struct {
	Type       string            `json:"type"`
	BOMRef     string            `json:"bom-ref"`
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	PURL       string            `json:"purl,omitempty"`
	Scope      string            `json:"scope,omitempty"`
	Properties []property        `json:"properties,omitempty"`
	Advisories []string          `json:"advisories,omitempty"`
	Extra      map[string]string `json:"-"`
}

// property is a name/value pair.
type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// dependency records the direct dependencies of one component.
type dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// rating is a severity rating.
type rating struct {
	Source   string  `json:"source"`
	Score    float64 `json:"score"`
	Severity string  `json:"severity"`
	Method   string  `json:"method"`
	Vector   string  `json:"vector,omitempty"`
}

// vulnerability is one known vulnerability affecting a component.
type vulnerability struct {
	BOMRef      string     `json:"bom-ref"`
	ID          string     `json:"id"`
	Source      source     `json:"source"`
	Ratings     []rating   `json:"ratings,omitempty"`
	CWEs        []int      `json:"cwes,omitempty"`
	Description string     `json:"description,omitempty"`
	Advisories  []advisory `json:"advisories,omitempty"`
}

// source names the origin of a vulnerability record.
type source struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// advisory links a vulnerability to the advisory that describes it.
type advisory struct {
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

// cyclonedxDocument is a CycloneDX 1.5 BOM.
type cyclonedxDocument struct {
	BOMFormat    string          `json:"bomFormat"`
	SpecVersion  string          `json:"specVersion"`
	SerialNumber string          `json:"serialNumber,omitempty"`
	Version      int             `json:"version"`
	Metadata     cdxMetadata     `json:"metadata"`
	Components   []component     `json:"components"`
	Dependencies []dependency    `json:"dependencies,omitempty"`
	Vulns        []vulnerability `json:"vulnerabilities"`
}

// cdxMetadata is the CycloneDX metadata block.
type cdxMetadata struct {
	Timestamp string        `json:"timestamp"`
	Tools     []cdxTool     `json:"tools,omitempty"`
	Component *cdxComponent `json:"component,omitempty"`
}

// cdxTool names a tool that produced the document.
type cdxTool struct {
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// cdxComponent is the subject of the document.
type cdxComponent struct {
	Type    string `json:"type"`
	BOMRef  string `json:"bom-ref"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl,omitempty"`
}

// WriteCycloneDX writes a CycloneDX 1.5 document.
//
// The components are the audit's resolved dependency tree and the
// vulnerabilities are the advisories that affect them. The findings are
// carried as properties, because CycloneDX has no field for "a static
// analyser thinks this file is dangerous".
func WriteCycloneDX(packages []model.Package, advisories []model.Advisory, findings []model.Finding, opts Options) ([]byte, error) {
	tool := opts.ToolName
	if tool == "" {
		tool = "AISAI"
	}
	ts := opts.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	doc := cyclonedxDocument{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
		Metadata: cdxMetadata{
			Timestamp: ts.UTC().Format(time.RFC3339),
			Tools:     []cdxTool{{Vendor: "AISAI", Name: tool, Version: opts.ToolVersion}},
		},
	}

	if opts.PackageName != "" {
		doc.Metadata.Component = &cdxComponent{
			Type:    "application",
			BOMRef:  purlOf(opts.PackageName, opts.PackageVersion),
			Name:    opts.PackageName,
			Version: opts.PackageVersion,
			PURL:    purlOf(opts.PackageName, opts.PackageVersion),
		}
	}

	// Group the advisories by the package they affect.
	byPkg := map[string][]model.Advisory{}
	for _, a := range advisories {
		byPkg[string(a.Package)] = append(byPkg[string(a.Package)], a)
	}
	// Group the findings by the package they were reported in.
	findingsByPkg := map[string][]model.Finding{}
	for _, f := range findings {
		key := string(f.Package)
		findingsByPkg[key] = append(findingsByPkg[key], f)
	}

	sorted := append([]model.Package(nil), packages...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Version < sorted[j].Version
	})

	seenVuln := map[string]bool{}
	for _, p := range sorted {
		ref := string(p.PURL)
		if ref == "" {
			ref = purlOf(p.Name, p.Version)
		}
		c := component{
			Type:    "library",
			BOMRef:  ref,
			Name:    p.Name,
			Version: p.Version,
			PURL:    ref,
		}
		if p.Direct {
			c.Scope = "required"
		}
		for _, a := range byPkg[ref] {
			c.Advisories = append(c.Advisories, a.ID)
		}
		for _, f := range findingsByPkg[ref] {
			c.Properties = append(c.Properties, property{
				Name:  "aisai:finding:" + f.CWE,
				Value: fmt.Sprintf("%s:%d %s", f.File, f.StartLine, f.Message),
			})
		}
		if len(c.Properties) > 0 {
			sort.Slice(c.Properties, func(i, j int) bool {
				return c.Properties[i].Name < c.Properties[j].Name
			})
		}
		doc.Components = append(doc.Components, c)

		for _, a := range byPkg[ref] {
			if seenVuln[a.ID] {
				continue
			}
			seenVuln[a.ID] = true
			doc.Vulns = append(doc.Vulns, cyclonedxVuln(a, ref))
		}
	}

	if len(doc.Components) == 0 {
		doc.Components = []component{}
	}
	if doc.Vulns == nil {
		doc.Vulns = []vulnerability{}
	}

	return json.MarshalIndent(doc, "", "  ")
}

// cyclonedxVuln maps one advisory onto a CycloneDX vulnerability.
func cyclonedxVuln(a model.Advisory, affects string) vulnerability {
	v := vulnerability{
		BOMRef:      a.ID,
		ID:          a.ID,
		Source:      source{Name: "OSV", URL: "https://osv.dev"},
		Description: a.Summary,
	}
	if a.URL != "" {
		v.Advisories = append(v.Advisories, advisory{Title: a.ID, URL: a.URL})
	}
	if a.Severity != "" {
		v.Ratings = append(v.Ratings, rating{
			Source:   "OSV",
			Severity: string(a.Severity),
			Method:   "other",
		})
	}
	for _, cwe := range a.CWE {
		if n, ok := cweNumber(cwe); ok {
			v.CWEs = append(v.CWEs, n)
		}
	}
	sort.Ints(v.CWEs)
	return v
}

// cweNumber extracts the numeric part of a CWE identifier.
func cweNumber(cwe string) (int, bool) {
	s := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(cwe)), "CWE-")
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// purlOf builds a PyPI PURL.
func purlOf(name, version string) string {
	if version == "" {
		return "pkg:pypi/" + name
	}
	return "pkg:pypi/" + name + "@" + version
}

// ---------------------------------------------------------------- SPDX

// spdxDocument is an SPDX 2.3 document.
type spdxDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships"`
	DocumentDescribes []string           `json:"documentDescribes"`
}

// spdxCreationInfo records who made the document and when.
type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

// spdxPackage is one package in the SPDX document.
type spdxPackage struct {
	SPDXID           string            `json:"SPDXID"`
	Name             string            `json:"name"`
	VersionInfo      string            `json:"versionInfo,omitempty"`
	DownloadLocation string            `json:"downloadLocation,omitempty"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	LicenseConcluded string            `json:"licenseConcluded"`
	LicenseDeclared  string            `json:"licenseDeclared"`
	CopyrightText    string            `json:"copyrightText"`
	ExternalRefs     []spdxExternalRef `json:"externalRefs,omitempty"`
	Comment          string            `json:"comment,omitempty"`
}

// spdxExternalRef points at the package's PURL.
type spdxExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

// spdxRelationship links two elements.
type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

// WriteSPDX writes an SPDX 2.3 document.
//
// SPDX has no vulnerability vocabulary, so the advisories are recorded as
// comments on the affected packages. That is a real limitation of the format,
// not of this implementation.
func WriteSPDX(packages []model.Package, advisories []model.Advisory, findings []model.Finding, opts Options) ([]byte, error) {
	tool := opts.ToolName
	if tool == "" {
		tool = "AISAI"
	}
	ts := opts.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	name := opts.PackageName
	if name == "" {
		name = "aisai-audit"
	}
	version := opts.PackageVersion
	if version == "" {
		version = "0"
	}

	doc := spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              name + "-" + version,
		DocumentNamespace: fmt.Sprintf("https://spdx.org/spdxdocs/aisai-%s-%d", opts.AuditID, ts.Unix()),
		CreationInfo: spdxCreationInfo{
			Created:  ts.UTC().Format("2006-01-02T15:04:05Z"),
			Creators: []string{"Tool: " + tool + "-" + opts.ToolVersion},
		},
		Packages:          []spdxPackage{},
		Relationships:     []spdxRelationship{},
		DocumentDescribes: []string{},
	}

	byPkg := map[string][]model.Advisory{}
	for _, a := range advisories {
		byPkg[string(a.Package)] = append(byPkg[string(a.Package)], a)
	}
	findingsByPkg := map[string][]model.Finding{}
	for _, f := range findings {
		findingsByPkg[string(f.Package)] = append(findingsByPkg[string(f.Package)], f)
	}

	sorted := append([]model.Package(nil), packages...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Version < sorted[j].Version
	})

	rootID := ""
	for _, p := range sorted {
		ref := string(p.PURL)
		if ref == "" {
			ref = purlOf(p.Name, p.Version)
		}
		id := spdxID(ref)
		pkg := spdxPackage{
			SPDXID:           id,
			Name:             p.Name,
			VersionInfo:      p.Version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			CopyrightText:    "NOASSERTION",
			ExternalRefs: []spdxExternalRef{{
				ReferenceCategory: "PACKAGE-MANAGER",
				ReferenceType:     "purl",
				ReferenceLocator:  ref,
			}},
		}
		var notes []string
		for _, a := range byPkg[ref] {
			notes = append(notes, fmt.Sprintf("advisory %s: %s", a.ID, a.Summary))
		}
		for _, f := range findingsByPkg[ref] {
			notes = append(notes, fmt.Sprintf("finding %s %s:%d %s", f.CWE, f.File, f.StartLine, f.Message))
		}
		if len(notes) > 0 {
			pkg.Comment = strings.Join(notes, "; ")
		}
		doc.Packages = append(doc.Packages, pkg)

		if p.Direct {
			doc.Relationships = append(doc.Relationships, spdxRelationship{
				SPDXElementID:      "SPDXRef-DOCUMENT",
				RelationshipType:   "DESCRIBES",
				RelatedSPDXElement: id,
			})
			doc.DocumentDescribes = append(doc.DocumentDescribes, id)
			if rootID == "" {
				rootID = id
			}
		}
	}

	if len(doc.DocumentDescribes) == 0 && len(doc.Packages) > 0 {
		doc.DocumentDescribes = []string{doc.Packages[0].SPDXID}
	}
	_ = rootID

	return json.MarshalIndent(doc, "", "  ")
}

// spdxID turns a PURL into an SPDX element identifier.
func spdxID(purl string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '-'
		}
	}, purl)
	return "SPDXRef-Package-" + s
}
