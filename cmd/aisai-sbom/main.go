// Command aisai-sbom writes a software bill of materials for the AISAI
// build itself.
//
// The rest of AISAI emits SBOMs for the packages it audits; this command
// emits the SBOM of AISAI: the Go module dependency tree that is compiled
// into the shipped binary. That is the supply chain of the tool that an
// auditor trusts, so it deserves the same treatment.
//
//	aisai-sbom --format cyclonedx --out aisai.cdx.json
//	aisai-sbom --format spdx --out aisai.spdx.json
//	aisai-sbom --both --out-dir ./sbom
//
// The module list comes from the build information the Go toolchain embeds
// in the binary (runtime/debug.ReadBuildInfo). When the Go toolchain is
// available, it is also asked for the module graph, which marks the direct
// requirements and (with --graph) adds the modules that are in the graph but
// are not linked into this particular binary.
package main

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tlmcguire/aisai/internal/sbom"
	"github.com/tlmcguire/aisai/internal/version"
)

func main() {
	var (
		format    = flag.String("format", "cyclonedx", "SBOM format: cyclonedx | spdx")
		out       = flag.String("out", "", "path of the SBOM file (default: stdout)")
		outDir    = flag.String("out-dir", "", "directory the SBOM files are written to (used with --both)")
		both      = flag.Bool("both", false, "write both the CycloneDX and the SPDX document")
		binary    = flag.String("binary", "", "AISAI binary whose build information describes the build (default: this executable)")
		linked    = flag.Bool("linked-only", false, "use only the modules linked into the binary, without the Go toolchain")
		appVer    = flag.String("app-version", "", "version of the AISAI build (default: the stamped version)")
		appCommit = flag.String("app-commit", "", "commit of the AISAI build (default: the stamped commit)")
	)
	version.Flag(flag.CommandLine)
	flag.Parse()

	if version.HandleVersion(flag.CommandLine) {
		return
	}

	if err := run(*format, *out, *outDir, *both, *binary, *linked, *appVer, *appCommit); err != nil {
		fmt.Fprintf(os.Stderr, "aisai-sbom: %v\n", err)
		os.Exit(1)
	}
}

func run(format, out, outDir string, both bool, binary string, linkedOnly bool, appVersion, appCommit string) error {
	if both {
		if out != "" {
			return fmt.Errorf("--out and --both are mutually exclusive: use --out-dir to write both documents")
		}
		if outDir == "" {
			return fmt.Errorf("--both needs --out-dir")
		}
	} else {
		switch format {
		case "cyclonedx", "spdx":
		case "":
			return fmt.Errorf("--format is required (cyclonedx or spdx)")
		default:
			return fmt.Errorf("unknown --format %q: use cyclonedx or spdx", format)
		}
	}

	mods, mainModule, err := collectModules(binary, linkedOnly)
	if err != nil {
		return err
	}

	if appVersion == "" {
		appVersion = version.Short()
	}
	if appCommit == "" {
		appCommit = version.Commit
	}

	opts := sbom.AppOptions{
		ToolName:    "aisai-sbom",
		ToolVersion: version.Short(),
		AppName:     sbom.DefaultAppName,
		AppVersion:  appVersion,
		AppCommit:   appCommit,
	}
	if mainModule != "" {
		opts.AppName = mainModule
	}

	emit := func(kind string) ([]byte, error) {
		switch kind {
		case "cyclonedx":
			return sbom.WriteAppCycloneDX(mods, opts)
		case "spdx":
			return sbom.WriteAppSPDX(mods, opts)
		}
		return nil, fmt.Errorf("unknown format %q", kind)
	}

	if both {
		for _, kind := range []string{"cyclonedx", "spdx"} {
			data, err := emit(kind)
			if err != nil {
				return err
			}
			path := filepath.Join(outDir, "aisai."+kind+".json")
			if err := writeFile(path, data); err != nil {
				return err
			}
		}
		return nil
	}

	data, err := emit(format)
	if err != nil {
		return err
	}
	if out == "" {
		_, err := os.Stdout.Write(data)
		if err != nil {
			return err
		}
		report("\n")
		return nil
	}
	return writeFile(out, data)
}

// report prints a progress message. A broken stdout must not stop the SBOM
// from being written, so the error is dropped on purpose.
func report(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...)
}

// writeFile writes the document and reports the path.
func writeFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	report("wrote %s\n", path)
	return nil
}

// collectModules returns the modules that make up the AISAI build.
//
// The build information is the authority: it lists the modules that actually
// contributed code to the binary. The module graph, when the Go toolchain is
// available, completes the picture — the build information of a single
// binary only covers the packages that binary links, while the AISAI
// application ships several commands — and it knows which requirements are
// direct.
//
// The main module is not returned: it is the subject of the document, not one
// of its components.
func collectModules(binary string, linkedOnly bool) ([]sbom.GoModule, string, error) {
	info, err := readBuildInfo(binary)
	if err != nil {
		return nil, "", err
	}

	mainModule := info.Main.Path
	linked := map[string]bool{}
	var mods []sbom.GoModule

	for _, dep := range info.Deps {
		if dep == nil || dep.Path == "" {
			continue
		}
		m := sbom.GoModule{Path: dep.Path, Version: dep.Version, Sum: dep.Sum}
		if dep.Replace != nil {
			m.Replace = &sbom.GoModule{
				Path:    dep.Replace.Path,
				Version: dep.Replace.Version,
				Sum:     dep.Replace.Sum,
			}
		}
		mods = append(mods, m)
		linked[moduleKey(m.Path, m.Version)] = true
	}

	if linkedOnly {
		return mods, mainModule, nil
	}

	// The Go toolchain knows which requirements are direct and which modules
	// are in the graph. It is a best-effort completion: without it the
	// document still lists the linked modules, it just cannot mark the direct
	// requirements.
	graphMods, graphErr := listModuleGraph()
	if graphErr != nil {
		fmt.Fprintf(os.Stderr, "aisai-sbom: the module graph is not available (%v); the document lists the linked modules only\n", graphErr)
		return mods, mainModule, nil
	}
	for _, gm := range graphMods {
		key := moduleKey(gm.Path, gm.Version)
		found := false
		for i := range mods {
			if moduleKey(mods[i].Path, mods[i].Version) == key {
				mods[i].Direct = gm.Direct
				found = true
			}
		}
		if !found {
			mods = append(mods, gm)
		}
	}

	return mods, mainModule, nil
}

// readBuildInfo returns the build information of the AISAI build.
//
// With no path it reads the build information of the running binary; with a
// path it reads the build information of that binary, which is how the SBOM
// of the whole application is produced from a command that links only a
// fraction of it.
func readBuildInfo(binary string) (*debug.BuildInfo, error) {
	if binary == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return nil, fmt.Errorf("this binary carries no build information")
		}
		return info, nil
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return nil, fmt.Errorf("reading the build information of %s: %w", binary, err)
	}
	return info, nil
}

// moduleKey identifies a module by path and version.
func moduleKey(path, version string) string {
	return path + "@" + version
}

// listedModule is one entry of the `go list -m -json all` output.
type listedModule struct {
	Path     string
	Version  string
	Sum      string
	Main     bool
	Indirect bool
	Replace  *listedModule
}

// listModuleGraph asks the Go toolchain for the module graph.
//
// It returns an error when the toolchain is not available or when the
// working directory is not inside a Go module; the caller treats that as
// "no graph information" rather than as a failure.
func listModuleGraph() ([]sbom.GoModule, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("the go toolchain is not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "list", "-m", "-json", "all")
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m -json all: %w", err)
	}

	var mods []sbom.GoModule
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var lm listedModule
		if err := dec.Decode(&lm); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding the module graph: %w", err)
		}
		if lm.Path == "" || lm.Main {
			continue
		}
		m := sbom.GoModule{
			Path:    lm.Path,
			Version: lm.Version,
			Sum:     lm.Sum,
			Direct:  !lm.Indirect,
		}
		if lm.Replace != nil {
			m.Replace = &sbom.GoModule{
				Path:    lm.Replace.Path,
				Version: lm.Replace.Version,
				Sum:     lm.Replace.Sum,
			}
		}
		mods = append(mods, m)
	}
	return mods, nil
}
