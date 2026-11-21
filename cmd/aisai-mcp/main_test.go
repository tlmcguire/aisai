package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tlmcguire/aisai/internal/mcp"
)

// expectedTools is the contractual tool list of the AISAI MCP server.
//
// The names are what external agents are configured against, so a change here
// is a breaking change and must fail the build rather than be discovered by
// an agent at run time.
var expectedTools = []string{
	"apply_patch",
	"ask_codebase",
	"export_report",
	"get_ast",
	"get_dependency_graph",
	"get_finding",
	"get_source",
	"list_files",
	"list_findings",
	"propose_fix",
	"run_llm_scan",
	"run_sast",
	"scan_package",
	"search_code",
}

// runMainWithIO runs main() with stdin, stdout and stderr replaced by pipes
// and returns everything that was written to stdout and stderr.
//
// The real entry point is driven rather than a copy of its wiring: the point
// of the test is that the protocol discipline of the command holds, and that
// can only be checked on the command itself.
func runMainWithIO(t *testing.T, stdin string, args []string) (stdout, stderr string) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}

	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	defer func() { os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr }()

	origArgs := os.Args
	os.Args = args
	defer func() { os.Args = origArgs }()

	// main() registers its flags on the global flag set, so the set has to
	// be fresh for every invocation: a second call would panic with
	// "flag redefined".
	origFlags := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ExitOnError)
	defer func() { flag.CommandLine = origFlags }()

	if _, err := inW.WriteString(stdin); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = inW.Close()

	outCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(outR)
		outCh <- string(data)
	}()
	errCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(errR)
		errCh <- string(data)
	}()

	main()

	_ = outW.Close()
	_ = errW.Close()
	stdout = <-outCh
	stderr = <-errCh
	_ = inR.Close()
	_ = outR.Close()
	_ = errR.Close()
	return stdout, stderr
}

// isolateEnv clears the environment variables the command reads, so the test
// never depends on the developer's shell.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AISAI_ADVISORY_OFFLINE",
		"AISAI_BYOK_API_KEY",
		"AISAI_BYOK_BASE_URL",
		"AISAI_BYOK_MODEL",
		"AISAI_CHAT_MODEL",
		"AISAI_CONFIG",
		"AISAI_EMBED_MODEL",
		"AISAI_NUM_CTX",
		"AISAI_OLLAMA_URL",
		"AISAI_OLLAMA_URLS",
		"AISAI_OSV_DB",
		"AISAI_SCAN_MODEL",
		"AISAI_SCAN_WORKERS",
		"AISAI_SOCKET",
		"AISAI_WORKSPACE",
		"DISABLE_LLM",
		"OLLAMA_BASE_URL",
		"OLLAMA_PRIMARY_MODEL",
		"OLLAMA_URL",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("DISABLE_LLM", "1")
}

// response is one decoded JSON-RPC response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// decodeLines parses every non-empty line of the output as JSON.
//
// A line that is not JSON is a protocol violation: the MCP client drops the
// connection when stdout carries anything but protocol JSON.
func decodeLines(t *testing.T, out string) []response {
	t.Helper()
	var resps []response
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var resp response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout line %d is not JSON: %v (%q)", i+1, err, line)
		}
		if resp.JSONRPC != "2.0" {
			t.Fatalf("stdout line %d has jsonrpc = %q, want %q", i+1, resp.JSONRPC, "2.0")
		}
		resps = append(resps, resp)
	}
	return resps
}

// TestMainSpeaksJSONRPCOnStdout drives the command end to end and checks the
// two calls an MCP client makes first: initialize and tools/list.
func TestMainSpeaksJSONRPCOnStdout(t *testing.T) {
	isolateEnv(t)

	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	args := []string{
		"aisai-mcp",
		"--workspace", workspace,
		"--db", filepath.Join(workspace, "findings.sqlite"),
		"--index", filepath.Join(workspace, "index.sqlite"),
		"--config", filepath.Join(dir, "no-such-config.json"),
	}

	stdout, _ := runMainWithIO(t, requests, args)

	resps := decodeLines(t, stdout)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2:\n%s", len(resps), stdout)
	}

	// initialize
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resps[0].Result, &init); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if init.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", init.ProtocolVersion, mcp.ProtocolVersion)
	}
	if init.ServerInfo.Name != mcp.ServerName {
		t.Errorf("serverInfo.name = %q, want %q", init.ServerInfo.Name, mcp.ServerName)
	}
	if init.ServerInfo.Version == "" {
		t.Error("serverInfo.version must not be empty")
	}
	if init.Capabilities.Tools == nil {
		t.Error("the server must advertise the tools capability")
	}

	// tools/list
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resps[1].Result, &list); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("the tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("the tool %q has no input schema", tool.Name)
		}
	}
	sort.Strings(names)
	want := append([]string(nil), expectedTools...)
	sort.Strings(want)
	if len(names) != len(want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools = %v, want %v", names, want)
		}
	}
}

// TestStdoutCarriesOnlyProtocolJSON is the protocol discipline test.
//
// Stdout is the MCP transport: a stray log line, a banner or a warning on
// stdout corrupts the JSON-RPC stream and makes the client drop the
// connection. Every non-empty line of stdout must therefore parse as JSON.
func TestStdoutCarriesOnlyProtocolJSON(t *testing.T) {
	isolateEnv(t)

	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	requests := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"

	args := []string{
		"aisai-mcp",
		"--workspace", workspace,
		"--db", filepath.Join(workspace, "findings.sqlite"),
		"--index", filepath.Join(workspace, "index.sqlite"),
		"--config", filepath.Join(dir, "no-such-config.json"),
	}

	stdout, _ := runMainWithIO(t, requests, args)

	for i, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var any map[string]any
		if err := json.Unmarshal([]byte(line), &any); err != nil {
			t.Fatalf("stdout line %d is not JSON: %v (%q)", i+1, err, line)
		}
		if _, ok := any["jsonrpc"]; !ok {
			t.Fatalf("stdout line %d is not a JSON-RPC message: %q", i+1, line)
		}
	}
}

// TestLogsGoToStderrNotToStdout checks that the diagnostics of the command
// stay off the protocol channel.
func TestLogsGoToStderrNotToStdout(t *testing.T) {
	isolateEnv(t)

	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	requests := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"

	args := []string{
		"aisai-mcp",
		"--workspace", workspace,
		"--db", filepath.Join(workspace, "findings.sqlite"),
		"--index", filepath.Join(workspace, "index.sqlite"),
		"--config", filepath.Join(dir, "no-such-config.json"),
	}

	stdout, stderr := runMainWithIO(t, requests, args)

	if !strings.Contains(stderr, "aisai-mcp ready") {
		t.Fatalf("the banner must be written to stderr, got %q", stderr)
	}
	if strings.Contains(stdout, "aisai-mcp ready") {
		t.Fatalf("the banner leaked onto stdout: %q", stdout)
	}
	// The log output must not be protocol JSON either.
	for i, line := range strings.Split(stderr, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			t.Errorf("the stderr line %d looks like protocol output: %q", i+1, line)
		}
	}
}
