#!/usr/bin/env bash
#
# End-to-end smoke test for the AISAI backend.
#
# It starts the server on a Unix socket, runs a real audit against PyPI and
# deps.dev, and checks every stage of the pipeline: the health endpoint, the
# dependency graph, the file tree, the code view, the static scan, the
# advisory lookup, and the apply → re-scan → revert patch loop.
#
# It needs network access (PyPI, deps.dev and OSV) but no LLM: the LLM
# features are disabled so the test is deterministic and does not need Ollama.
#
# Usage:
#   scripts/smoke.sh                  # audit flask 2.0.0
#   scripts/smoke.sh requests 2.31.0  # audit a different package
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="/usr/local/go/bin:$PATH"

# The JSON of the API is read with Python. The interpreter is resolved from
# the PATH rather than hardcoded: /usr/local/bin/python3 is where the macOS
# developer tools put it, and it does not exist on the Linux runners, where
# the interpreter is /usr/bin/python3.
PYTHON="$(command -v python3 || true)"
if [[ -z "$PYTHON" ]]; then
  echo "python3 is required by the smoke test" >&2
  exit 1
fi

PKG="${1:-flask}"
VERSION="${2:-2.0.0}"

WORKSPACE="$(mktemp -d "${TMPDIR:-/tmp}/aisai-smoke.XXXXXX")"
SOCKET="$WORKSPACE/aisai.sock"
SERVER_PID=""

RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
PASS=0
FAIL=0

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  # The workspace is disposable; it is removed with the recoverable deleter so
  # the script stays usable on a machine where plain rm is restricted.
  if command -v mavis-trash >/dev/null 2>&1; then
    mavis-trash -- "$WORKSPACE" >/dev/null 2>&1 || true
  else
    rm -rf "$WORKSPACE" 2>/dev/null || true
  fi
}
trap cleanup EXIT

say()  { printf '\n%s==> %s%s\n' "$BOLD" "$1" "$RESET"; }
ok()   { PASS=$((PASS+1)); printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  %s✗%s %s\n' "$RED" "$RESET" "$1"; }
note() { printf '  %s·%s %s\n' "$YELLOW" "$RESET" "$1"; }

# check <description> <expected> <actual>
check() {
  if [[ "$2" == "$3" ]]; then
    ok "$1"
  else
    bad "$1 — expected [$2], got [$3]"
  fi
}

# api <method> <path> [body] -> prints the response body
api() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -s --unix-socket "$SOCKET" -X "$method" -H 'Content-Type: application/json' -d "$body" "http://localhost$path"
  else
    curl -s --unix-socket "$SOCKET" -X "$method" "http://localhost$path"
  fi
}

# jsonq <json> <python expression over `d`> [extra python argv...]
jsonq() {
  local json="$1" expr="$2"; shift 2
  printf '%s' "$json" | "$PYTHON" -c "
import sys, json
d = json.load(sys.stdin)
v = ($expr)
print(v if v is not None else '')
" "$@" 2>/dev/null || echo ""
}

# first_py_path <file-tree json> -> the first Python file in the tree
first_py_path() {
  printf '%s' "$1" | "$PYTHON" -c '
import sys, json
d = json.load(sys.stdin)
def walk(n):
    if n.get("type") == "file" and n.get("path", "").endswith(".py"):
        return n["path"]
    for c in n.get("children") or []:
        r = walk(c)
        if r:
            return r
    return None
print(walk(d.get("tree") or {}) or "")
' 2>/dev/null || echo ""
}

# make_diff <file-content json> <target path> -> a unified diff that adds one
# comment line after the first non-blank line of the file.
make_diff() {
  printf '%s' "$1" | "$PYTHON" -c '
import sys, json
d = json.load(sys.stdin)
target = sys.argv[1]
lines = d.get("file", {}).get("lines") or []
for i, line in enumerate(lines):
    if line.strip():
        print("--- a/" + target)
        print("+++ b/" + target)
        print("@@ -%d,1 +%d,2 @@" % (i + 1, i + 1))
        print(" " + line)
        print("+# applied by the AISAI smoke test")
        break
' "$2" 2>/dev/null || echo ""
}

say "Building the backend"
go build -o "$WORKSPACE/aisai-server" ./cmd/aisai-server
go build -o "$WORKSPACE/aisai-mcp" ./cmd/aisai-mcp
ok "server and mcp binaries built"

say "Starting the server on $SOCKET"
DISABLE_LLM=1 "$WORKSPACE/aisai-server" --workspace "$WORKSPACE/audits" --socket "$SOCKET" \
  >"$WORKSPACE/out.log" 2>"$WORKSPACE/err.log" &
SERVER_PID=$!

# Wait for the single JSON line the Electron main process parses.
for _ in $(seq 1 60); do
  if [[ -s "$WORKSPACE/out.log" ]]; then break; fi
  sleep 0.25
done
LISTENING="$(cat "$WORKSPACE/out.log" 2>/dev/null || true)"
if [[ -z "$LISTENING" ]]; then
  bad "the server did not report a listen address"
  cat "$WORKSPACE/err.log" || true
  exit 1
fi
if [[ "$(jsonq "$LISTENING" "d.get('type')")" == "listening" ]]; then
  ok "listening line: $LISTENING"
else
  bad "unexpected listening line: $LISTENING"
fi

say "Health"
HEALTH="$(api GET /api/health)"
check "health ok" "True" "$(jsonq "$HEALTH" "d.get('ok')")"
check "health reports a version" "True" "$(jsonq "$HEALTH" "bool(d.get('version'))")"

say "Creating the audit for $PKG $VERSION"
CREATE="$(api POST /api/audits "{\"package\":\"$PKG\",\"version\":\"$VERSION\"}")"
AID="$(jsonq "$CREATE" "d.get('audit',{}).get('id')")"
if [[ -z "$AID" ]]; then
  bad "the audit was not created: $CREATE"
  exit 1
fi
ok "audit id $AID"

say "Waiting for the audit to become ready"
READY=""
for _ in $(seq 1 240); do
  SNAP="$(api GET "/api/audits/$AID")"
  STATUS="$(jsonq "$SNAP" "d.get('audit',{}).get('status')")"
  if [[ "$STATUS" == "ready" || "$STATUS" == "failed" || "$STATUS" == "error" ]]; then
    READY="$STATUS"
    break
  fi
  sleep 1
done
check "audit status" "ready" "$READY"
if [[ "$READY" != "ready" ]]; then
  tail -20 "$WORKSPACE/err.log" || true
  exit 1
fi

SNAP="$(api GET "/api/audits/$AID")"
FILES="$(jsonq "$SNAP" "d.get('audit',{}).get('fileCount')")"
CHUNKS="$(jsonq "$SNAP" "d.get('audit',{}).get('chunkCount')")"
note "parsed $FILES files into $CHUNKS chunks"
if [[ "${FILES:-0}" -gt 0 ]]; then ok "the audit parsed source files"; else bad "no files were parsed"; fi

say "Dependency graph"
GRAPH="$(api GET "/api/audits/$AID/graph")"
NODES="$(jsonq "$GRAPH" "len(d.get('graph',{}).get('nodes') or [])")"
EDGES="$(jsonq "$GRAPH" "len(d.get('graph',{}).get('edges') or [])")"
note "$NODES nodes, $EDGES edges"
if [[ "${NODES:-0}" -gt 1 ]]; then ok "the graph has the resolved tree"; else bad "the graph has no dependency edges"; fi
ROOTNODE="$(jsonq "$GRAPH" "next((n for n in d['graph']['nodes'] if n['name'].lower()=='$PKG'.lower()), None)")"
if [[ -n "$ROOTNODE" ]]; then ok "the root package is $PKG"; else bad "the root package is missing from the graph"; fi

say "File tree and the code view"
TREE="$(api GET "/api/audits/$AID/files")"
PYPATH="$(first_py_path "$TREE")"
if [[ -z "$PYPATH" ]]; then
  bad "the file tree contains no Python file"
else
  ok "tree path: $PYPATH"
  # The path the tree hands out must be usable as-is. This is the check
  # that catches an absolute-path leak.
  CODE="$(api GET "/api/audits/$AID/file?path=$PYPATH")"
  LINES="$(jsonq "$CODE" "len(d.get('file',{}).get('lines') or [])")"
  if [[ "${LINES:-0}" -gt 0 ]]; then ok "the file view returns $LINES lines"; else bad "the file view returned nothing for a tree path"; fi
  if [[ "$PYPATH" == /* ]]; then bad "the tree path is absolute"; else ok "the tree path is workspace-relative"; fi
  AST="$(api GET "/api/audits/$AID/ast?path=$PYPATH")"
  NODES_AST="$(jsonq "$AST" "len(d.get('ast',{}).get('nodes') or [])")"
  if [[ "${NODES_AST:-0}" -gt 0 ]]; then ok "the AST view returns $NODES_AST nodes"; else bad "the AST view returned nothing"; fi
  CHUNKSR="$(api GET "/api/audits/$AID/chunks?path=$PYPATH")"
  NCHUNKS="$(jsonq "$CHUNKSR" "len(d.get('chunks') or [])")"
  if [[ "${NCHUNKS:-0}" -gt 0 ]]; then ok "the file has $NCHUNKS semantic chunks"; else bad "no chunks for the file"; fi
fi

say "Static scan"
api POST "/api/audits/$AID/scan" '{"engine":"sast"}' >/dev/null
sleep 2
FINDINGS="$(api GET "/api/audits/$AID/findings")"
NFIND="$(jsonq "$FINDINGS" "len(d.get('findings') or [])")"
note "$NFIND findings"
if [[ "${NFIND:-0}" -gt 0 ]]; then ok "the static engine reported findings"; else bad "the static engine reported nothing"; fi
# Finding paths must be workspace-relative too.
ABS_FIND="$(jsonq "$FINDINGS" "sum(1 for f in (d.get('findings') or []) if f.get('file','').startswith('/'))")"
check "findings with absolute paths" "0" "$ABS_FIND"

say "Advisories"
api POST "/api/audits/$AID/advisories" '{"offline":false}' >/dev/null
ADV="$(api GET "/api/audits/$AID/advisories")"
MODE="$(jsonq "$ADV" "d.get('mode')")"
NADV="$(jsonq "$ADV" "len(d.get('advisories') or [])")"
note "mode=$MODE, $NADV advisories"
if [[ "$MODE" == "online" || "$MODE" == "offline" ]]; then ok "the advisory mode is $MODE"; else bad "unexpected advisory mode [$MODE]"; fi
STATUS="$(api GET /api/advisories/status)"
check "advisory status reports a mode" "True" "$(jsonq "$STATUS" "bool(d.get('mode'))")"

say "Patch loop: apply, re-scan, revert"
# Find a file with a finding we can fix, or fall back to a file with content.
TARGET=""
TARGET="$(jsonq "$FINDINGS" "' '.join(sorted(set(f.get('file') for f in (d.get('findings') or []) if f.get('file'))))")"
TARGET="${TARGET%% *}"
if [[ -z "$TARGET" ]]; then
  note "no findings to patch; exercising the loop on the first Python file instead"
  TARGET="$PYPATH"
fi
if [[ -n "$TARGET" ]]; then
  CONTENT="$(api GET "/api/audits/$AID/file?path=$TARGET")"
  DIFF="$(make_diff "$CONTENT" "$TARGET")"
  if [[ -z "$DIFF" ]]; then
    bad "could not build a diff for $TARGET"
  else
    APPLY="$(api POST "/api/audits/$AID/apply-patch" "$("$PYTHON" -c '
import json,sys
print(json.dumps({"path": sys.argv[1], "unified": sys.argv[2]}))
' "$TARGET" "$DIFF")")"
    APPLIED="$(jsonq "$APPLY" "d.get('applied')")"
    check "the patch applied" "True" "$APPLIED"
    if [[ "$APPLIED" != "True" ]]; then
      note "apply response: $APPLY"
    fi
    RESOLVED="$(jsonq "$APPLY" "len((d.get('rescan') or {}).get('resolved') or [])")"
    INTRODUCED="$(jsonq "$APPLY" "len((d.get('rescan') or {}).get('introduced') or [])")"
    UNCHANGED="$(jsonq "$APPLY" "str((d.get('rescan') or {}).get('unchanged'))")"
    note "rescan: $RESOLVED resolved, $INTRODUCED introduced, $UNCHANGED unchanged"
    AFTER="$(api GET "/api/audits/$AID/file?path=$TARGET")"
    if [[ "$(jsonq "$AFTER" "'applied by the AISAI smoke test' in ''.join(d.get('file',{}).get('lines') or [])")" == "True" ]]; then
      ok "the patched content is on disk"
    else
      bad "the patched content is not on disk"
    fi
    CHANGES="$(api GET "/api/audits/$AID/changes")"
    NCH="$(jsonq "$CHANGES" "len(d.get('changes') or [])")"
    if [[ "${NCH:-0}" -gt 0 ]]; then ok "the change was recorded ($NCH change(s))"; else bad "the change was not recorded"; fi
    PATCHFILE="$(api GET "/api/audits/$AID/changes.patch")"
    if [[ "$PATCHFILE" == *"+++"* ]]; then ok "the .patch export contains a diff"; else bad "the .patch export is empty"; fi
    REVERT="$(api POST "/api/audits/$AID/revert-patch" "{\"path\":\"$TARGET\"}")"
    REVERTED="$(jsonq "$REVERT" "d.get('reverted')")"
    check "the patch reverted" "True" "$REVERTED"
    BACK="$(api GET "/api/audits/$AID/file?path=$TARGET")"
    if [[ "$(jsonq "$BACK" "'applied by the AISAI smoke test' in ''.join(d.get('file',{}).get('lines') or [])")" == "False" ]]; then
      ok "the original content was restored"
    else
      bad "the original content was not restored"
    fi
  fi
fi

say "SBOM export"
CDX="$(api GET "/api/audits/$AID/export?format=cyclonedx")"
check "the CycloneDX document has the right format" "CycloneDX" "$(jsonq "$CDX" "d.get('bomFormat')")"
CDXCOMP="$(jsonq "$CDX" "len(d.get('components') or [])")"
note "$CDXCOMP components in the CycloneDX document"
if [[ "${CDXCOMP:-0}" -gt 0 ]]; then ok "the CycloneDX document lists the dependency tree"; else bad "the CycloneDX document has no components"; fi
SPDX="$(api GET "/api/audits/$AID/export?format=spdx")"
check "the SPDX document has the right version" "SPDX-2.3" "$(jsonq "$SPDX" "d.get('spdxVersion')")"
SPDXPKG="$(jsonq "$SPDX" "len(d.get('packages') or [])")"
if [[ "${SPDXPKG:-0}" -gt 0 ]]; then ok "the SPDX document lists $SPDXPKG packages"; else bad "the SPDX document has no packages"; fi
BADFMT="$(api GET "/api/audits/$AID/export?format=xml")"
check "an unknown export format is refused" "bad_request" "$(jsonq "$BADFMT" "d.get('error',{}).get('code')")"

say "Path traversal is refused"
ESC="$(api GET "/api/audits/$AID/file?path=../../../etc/passwd")"
CODE="$(jsonq "$ESC" "d.get('error',{}).get('code')")"
if [[ "$CODE" == "path_escape" || "$CODE" == "not_found" ]]; then
  ok "an escaping path is refused ($CODE)"
else
  bad "an escaping path was not refused: $ESC"
fi

say "Event stream"
# The event stream is the channel the progress events, the finding events and
# the streaming chat travel through. It used to be torn down the instant the
# handshake completed, because the hub read with the context of the HTTP
# request and net/http cancels that context as soon as the handler returns.
# The check below opens a real connection, waits, and then makes sure it is
# still usable.
WSSTATE="$("$PYTHON" - "$SOCKET" <<'PYEOF'
import base64, os, socket, sys, time

sock_path = sys.argv[1]
key = base64.b64encode(os.urandom(16)).decode()
request = (
    "GET /ws HTTP/1.1\r\n"
    "Host: localhost\r\n"
    "Upgrade: websocket\r\n"
    "Connection: Upgrade\r\n"
    f"Sec-WebSocket-Key: {key}\r\n"
    "Sec-WebSocket-Version: 13\r\n\r\n"
)
conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
conn.settimeout(5)
conn.connect(sock_path)
conn.sendall(request.encode())
status = conn.recv(4096).decode(errors="replace").split("\r\n")[0]
if " 101 " not in status:
    print("handshake_failed")
    sys.exit(0)
try:
    data = conn.recv(4096)
    print("closed" if data == b"" else "unexpected_data")
except socket.timeout:
    print("open")
finally:
    conn.close()
PYEOF
)"
case "$WSSTATE" in
  open) ok "the event stream stays open after the handshake" ;;
  *)    bad "the event stream did not survive the handshake ($WSSTATE)" ;;
esac

say "MCP server"
INIT="$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | "$WORKSPACE/aisai-mcp" --workspace "$WORKSPACE/audits" 2>/dev/null)"
PROTO="$(jsonq "$INIT" "d.get('result',{}).get('protocolVersion')")"
if [[ -n "$PROTO" ]]; then ok "initialize returned protocol $PROTO"; else bad "the MCP server did not answer initialize"; fi
TOOLS="$(printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | "$WORKSPACE/aisai-mcp" --workspace "$WORKSPACE/audits" 2>/dev/null)"
NTOOLS="$(jsonq "$TOOLS" "len(d.get('result',{}).get('tools') or [])")"
note "$NTOOLS tools registered"
if [[ "${NTOOLS:-0}" -ge 14 ]]; then ok "the MCP tool set is complete"; else bad "only $NTOOLS tools are registered"; fi

say "Result"
printf '  %d passed, %d failed\n' "$PASS" "$FAIL"
if [[ "$FAIL" -gt 0 ]]; then
  printf '\n%sThe smoke test FAILED.%s\n' "$RED" "$RESET"
  exit 1
fi
printf '\n%sThe smoke test passed.%s\n' "$GREEN" "$RESET"
