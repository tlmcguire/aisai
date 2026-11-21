// Application SBOM emitters.
//
// This file is the counterpart of sbom.go: sbom.go describes the supply
// chain of an *audited package* (a PyPI package and its dependencies), while
// this file describes the supply chain of *AISAI itself* — the Go module
// dependency tree that is compiled into the shipped binary.
//
// The CycloneDX and SPDX document structures are the ones already declared
// in sbom.go and are reused as-is, so both directions emit the same document
// shape. The audit-oriented WriteCycloneDX/WriteSPDX cannot be reused
// directly: they take model.Package values (a PyPI package with a PyPI
// PURL), so the application tree, its golang PURLs and its application
// metadata component would all be wrong.
package sbom

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GoModule is one module in the AISAI build's dependency tree.
type GoModule struct {
	// Path is the module path, e.g. "github.com/tree-sitter/go-tree-sitter".
	Path string
	// Version is the module version, e.g. "v0.25.0".
	Version string
	// Sum is the go.sum checksum, when it is known.
	Sum string
	// Direct reports whether the module is a direct requirement of the
	// AISAI module.
	Direct bool
	// Replace is the module this one was replaced by, if any.
	Replace *GoModule
}

// AppOptions configures an application SBOM document.
type AppOptions struct {
	// ToolName is the name recorded as the producer. Defaults to "aisai-sbom".
	ToolName string
	// ToolVersion is stamped into the document metadata.
	ToolVersion string
	// Timestamp is the document creation time. Zero means "now".
	Timestamp time.Time
	// AppName is the main module path. Defaults to "github.com/tlmcguire/aisai".
	AppName string
	// AppVersion is the version of the build.
	AppVersion string
	// AppCommit is the commit the build was made from.
	AppCommit string
}

// DefaultAppName is the main module of the AISAI build.
const DefaultAppName = "github.com/tlmcguire/aisai"

// goPURL builds the Package URL of a Go module.
func goPURL(path, version string) string {
	if version == "" {
		return "pkg:golang/" + path
	}
	return "pkg:golang/" + path + "@" + version
}

// moduleRef returns the PURL that identifies a module, following a
// replacement when there is one.
func moduleRef(m GoModule) string {
	if m.Replace != nil && m.Replace.Path != "" {
		return goPURL(m.Replace.Path, m.Replace.Version)
	}
	return goPURL(m.Path, m.Version)
}

// appPURL returns the PURL of the AISAI application itself.
func appPURL(opts AppOptions) string {
	name := opts.AppName
	if name == "" {
		name = DefaultAppName
	}
	return goPURL(name, opts.AppVersion)
}

// sortedModules returns the modules in a stable order.
func sortedModules(mods []GoModule) []GoModule {
	out := append([]GoModule(nil), mods...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// WriteAppCycloneDX writes a CycloneDX 1.5 document for the AISAI build.
//
// The subject of the document is the AISAI application; the components are
// the modules that were compiled into it.
func WriteAppCycloneDX(mods []GoModule, opts AppOptions) ([]byte, error) {
	tool := opts.ToolName
	if tool == "" {
		tool = "aisai-sbom"
	}
	ts := opts.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	doc := cyclonedxDocument{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.5",
		SerialNumber: serialNumber(),
		Version:      1,
		Metadata: cdxMetadata{
			Timestamp: ts.UTC().Format(time.RFC3339),
			Tools:     []cdxTool{{Vendor: "AISAI", Name: tool, Version: opts.ToolVersion}},
			Component: &cdxComponent{
				Type:    "application",
				BOMRef:  appPURL(opts),
				Name:    opts.AppName,
				Version: opts.AppVersion,
				PURL:    appPURL(opts),
			},
		},
		Components:   []component{},
		Dependencies: []dependency{},
	}

	for _, m := range sortedModules(mods) {
		if m.Path == "" {
			continue
		}
		ref := moduleRef(m)
		c := component{
			Type:    "library",
			BOMRef:  ref,
			Name:    m.Path,
			Version: m.Version,
			PURL:    ref,
		}
		if m.Direct {
			c.Scope = "required"
		}
		if m.Sum != "" {
			c.Properties = append(c.Properties, property{
				Name:  "aisai:go:sum",
				Value: m.Sum,
			})
		}
		if m.Replace != nil && m.Replace.Path != "" {
			c.Properties = append(c.Properties, property{
				Name:  "aisai:go:replaced",
				Value: m.Path + "@" + m.Version,
			})
			c.Name = m.Replace.Path
			c.Version = m.Replace.Version
		}
		doc.Components = append(doc.Components, c)
	}

	// The application depends on its direct requirements; the modules depend
	// on nothing that this build knows about.
	direct := []string{}
	for _, m := range sortedModules(mods) {
		if m.Direct && m.Path != "" {
			direct = append(direct, moduleRef(m))
		}
	}
	sort.Strings(direct)
	doc.Dependencies = append(doc.Dependencies, dependency{Ref: appPURL(opts), DependsOn: direct})
	for _, c := range doc.Components {
		doc.Dependencies = append(doc.Dependencies, dependency{Ref: c.BOMRef})
	}

	return json.MarshalIndent(doc, "", "  ")
}

// WriteAppSPDX writes an SPDX 2.3 document for the AISAI build.
//
// SPDX has no application metadata block, so the AISAI build itself is a
// package that every other package is a DEPENDENCY_OF.
func WriteAppSPDX(mods []GoModule, opts AppOptions) ([]byte, error) {
	tool := opts.ToolName
	if tool == "" {
		tool = "aisai-sbom"
	}
	ts := opts.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	name := opts.AppName
	if name == "" {
		name = DefaultAppName
	}

	rootRef := appPURL(opts)
	rootID := spdxID(rootRef)

	doc := spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              name + "-" + opts.AppVersion,
		DocumentNamespace: fmt.Sprintf("https://spdx.org/spdxdocs/aisai-%s-%d", opts.AppCommit, ts.Unix()),
		CreationInfo: spdxCreationInfo{
			Created:  ts.UTC().Format("2006-01-02T15:04:05Z"),
			Creators: []string{"Tool: " + tool + "-" + opts.ToolVersion},
		},
		Packages:          []spdxPackage{},
		Relationships:     []spdxRelationship{},
		DocumentDescribes: []string{rootID},
	}

	root := spdxPackage{
		SPDXID:           rootID,
		Name:             name,
		VersionInfo:      opts.AppVersion,
		DownloadLocation: "NOASSERTION",
		FilesAnalyzed:    false,
		LicenseConcluded: "NOASSERTION",
		LicenseDeclared:  "NOASSERTION",
		CopyrightText:    "NOASSERTION",
		ExternalRefs: []spdxExternalRef{{
			ReferenceCategory: "PACKAGE-MANAGER",
			ReferenceType:     "purl",
			ReferenceLocator:  rootRef,
		}},
	}
	if opts.AppCommit != "" {
		root.Comment = "commit " + opts.AppCommit
	}
	doc.Packages = append(doc.Packages, root)
	doc.Relationships = append(doc.Relationships, spdxRelationship{
		SPDXElementID:      "SPDXRef-DOCUMENT",
		RelationshipType:   "DESCRIBES",
		RelatedSPDXElement: rootID,
	})

	for _, m := range sortedModules(mods) {
		if m.Path == "" {
			continue
		}
		ref := moduleRef(m)
		pkg := spdxPackage{
			SPDXID:           spdxID(ref),
			Name:             m.Path,
			VersionInfo:      m.Version,
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
		if m.Sum != "" {
			notes = append(notes, "go.sum "+m.Sum)
		}
		if m.Replace != nil && m.Replace.Path != "" {
			notes = append(notes, fmt.Sprintf("replaces %s@%s", m.Path, m.Version))
			pkg.Name = m.Replace.Path
			pkg.VersionInfo = m.Replace.Version
		}
		if !m.Direct {
			notes = append(notes, "indirect dependency")
		}
		if len(notes) > 0 {
			pkg.Comment = strings.Join(notes, "; ")
		}
		doc.Packages = append(doc.Packages, pkg)

		doc.Relationships = append(doc.Relationships, spdxRelationship{
			SPDXElementID:      pkg.SPDXID,
			RelationshipType:   "DEPENDENCY_OF",
			RelatedSPDXElement: rootID,
		})
	}

	return json.MarshalIndent(doc, "", "  ")
}

// serialNumber returns a random CycloneDX serial number.
//
// The UUID comes from crypto/rand, so two documents for the same build never
// claim the same identity.
func serialNumber() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A serial number is optional; a broken entropy source must not
		// stop the SBOM from being written.
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
