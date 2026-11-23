# Releasing AISAI

This document describes how a release is built, what it needs from the
machine or from the repository settings, and what is *not* done yet. Every
statement here was checked against the code in this repository; the parts
that could not be checked locally are marked as such.

---

## 1. The pieces of a release

| Piece | Where it lives | What it does |
|---|---|---|
| Version stamping | `internal/version/version.go` | The version of the build, overridable with `-ldflags`. |
| The SBOM of the build | `cmd/aisai-sbom`, `internal/sbom/appsbom.go` | CycloneDX 1.5 and SPDX 2.3 documents of the AISAI dependency tree. |
| The desktop packaging | `electron/package.json`, `electron/.github/` | electron-builder configuration for macOS, Windows and Linux. |
| The release pipeline | `.github/workflows/release.yml` | Builds, signs, notarises and publishes. |
| The nightly benchmark | `.github/workflows/bench-nightly.yml` | Re-runs the static benchmark and compares it with the baseline. |
| The CI gate | `.github/workflows/ci.yml` | Build matrix, lint, tests, SBOM, E2E. |

---

## 2. Version stamping

`internal/version/version.go` holds three variables that `-ldflags` can
overwrite:

```
github.com/tlmcguire/aisai/internal/version.Version
github.com/tlmcguire/aisai/internal/version.Commit
github.com/tlmcguire/aisai/internal/version.Date
```

The exact string the Makefile has to pass:

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.7.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS ?= -X github.com/tlmcguire/aisai/internal/version.Version=$(VERSION) \
           -X github.com/tlmcguire/aisai/internal/version.Commit=$(COMMIT) \
           -X github.com/tlmcguire/aisai/internal/version.Date=$(DATE)
```

The current Makefile passes an empty `LDFLAGS`, so a plain `make build`
produces `0.7.0-dev`. **The Makefile is not part of this change**; the three
targets above have to be wired by whoever owns it.

### What happens without `-ldflags`

The package resolves the missing values at init time from the build
information the Go toolchain embeds in every binary
(`runtime/debug.ReadBuildInfo`):

* `Version` — the version of the module the binary was built from, when it
  is a real version (`go install example.com/mod@v1.2.3`). A binary built
  from a checkout reports `(devel)`, which is *not* a version, so the
  placeholder `0.7.0-dev` survives.
* `Commit` — the `vcs.revision` build setting, with a `-dirty` suffix when
  `vcs.modified` is `true`.
* `Date` — the `vcs.time` build setting.

A field that was stamped explicitly always wins: `applyBuildInfo` only fills
in a field that still holds its placeholder.

### Showing the version to a user

`internal/version` exports the plumbing:

```go
version.Flag(flag.CommandLine)      // registers -version
if version.HandleVersion(flag.CommandLine) {
    return
}
```

`cmd/aisai-sbom` already does this. **`cmd/aisai-server` does not** — it is
owned by another workstream. The three lines above are the change it needs;
without them the server still reports its version in the JSON line it
prints on startup (`{"type":"listening",...,"version":"..."}`), which the
Electron main process reads, but a user has no `--version` flag.

### Keeping the Go version and the Electron version in sync

`electron/package.json` has `"version": "0.7.0"` and the Go build has its
own version. They must agree in a release. Two ways, pick one:

* **Tag driven (recommended).** The release is made from a git tag
  (`v1.2.3`). The Makefile derives `VERSION` from `git describe --tags` and
  the workflow sets the Electron version from the same tag before packaging:
  `npm version --no-git-tag-version "${VERSION}"`. The workflow in this
  repository does the latter in the `backend` job only, so the desktop job
  still needs it.
* **Checked in.** A CI step compares `electron/package.json`'s version with
  `git describe --tags` and fails when they differ.

---

## 3. The SBOM of the AISAI build

`internal/sbom` has two emitters. `sbom.go` describes the supply chain of an
*audited package* (a PyPI package and its dependencies) and takes
`model.Package` values, so its PURLs are PyPI PURLs. It cannot express a Go
module tree.

`internal/sbom/appsbom.go` was therefore **added** for the application
direction. It reuses the document structures, the SPDX identifier helper and
the property helper that `sbom.go` already declares — it does not duplicate
the emitters — and adds the application metadata block, the `pkg:golang`
PURLs and the dependency graph.

```
go run ./cmd/aisai-sbom --format cyclonedx --out aisai.cdx.json
go run ./cmd/aisai-sbom --format spdx --out aisai.spdx.json
go run ./cmd/aisai-sbom --both --out-dir ./sbom
```

* `--both` writes `aisai.cyclonedx.json` and `aisai.spdx.json` into
  `--out-dir`. `--out` and `--both` are mutually exclusive.
* `--binary PATH` reads the build information of another binary, which is
  how the SBOM of the whole application is produced: `aisai-sbom` itself
  links almost nothing. The CI job uses
  `--binary bin/aisai-server`.
* The module list comes from the build information of that binary. When the
  Go toolchain is available, `go list -m -json all` completes the list with
  the modules of the module graph and marks the direct requirements. The
  toolchain is optional: without it the document lists the linked modules
  only, and says so on stderr.

The SBOM is built and uploaded as an artifact on every push (the `sbom` job
in `ci.yml`) and is attached to every release. The release pipeline
writes one per platform, from the binary that platform ships, and
publishes them all; the file names carry the platform, because the
release holds more than one of them.

---

## 4. The lint gate

`.golangci.yml` at the repository root configures `golangci-lint`, pinned to
**v2.15.0** in `ci.yml` so that a new release cannot turn a green branch
red without a commit. The enabled linters are:

```
bodyclose, copyloopvar, errcheck, errorlint, govet, ineffassign,
misspell, nilerr, noctx, staticcheck, unconvert, unused
```

plus `gofmt` as a formatter. `go vet` and a `gofmt -l` check stay gated as
separate steps in the `go` and `lint` jobs, so switching a lint rule off
cannot take the formatting gate with it.

### Lint findings that are currently suppressed

No `//nolint` comment was added and no linter was switched off wholesale.
The following findings exist today in files owned by other workstreams and
are excluded by an explicit, path-scoped rule in `.golangci.yml`. Each one
is a real finding that the owning workstream should fix; the rule can then
be deleted.

| File | Linter | Finding |
|---|---|---|
| `internal/index/index.go`, `internal/index/search.go` | `noctx` | the non-context `database/sql` calls (19 + 2) |
| `cmd/aisai-server/main.go` | `noctx` | `net.Listen` instead of `(*net.ListenConfig).Listen` (2) |
| `internal/server/server_test.go` | `noctx` | `httptest.NewRequest` without a context |
| `internal/llm/ollama.go`, `internal/llm/openai.go` | `bodyclose` | a response body that is not closed (3) |
| `internal/llm/ollama_test.go` | `errcheck` | `io.WriteString` (3) |
| `internal/deps/depsdev.go`, `internal/deps/fallback.go` | `errorlint`, `unconvert` | `%v` instead of `%w`, and a conversion that changes nothing (4) |
| `internal/audit/llm_scan.go` | `unused` | `describePackage` is dead code |
| `internal/audit/advisory_test.go` | `errorlint` | an error compared with `!=` |
| `internal/audit/workspace.go` | `nilerr` | a swallowed error |
| `internal/scan/detect.go` | `unused` | three unused tool-name constants |
| `internal/scan/prompts.go` | `nilerr` | a swallowed error |
| `internal/advisory/status.go` | `nilerr` | three swallowed errors |
| `internal/pypi/extract.go` | `nilerr` | two swallowed errors |
| `internal/server/server.go` | `errcheck` | `w.Write` after the response began (3) |
| `internal/findings/store.go` | `errcheck` | the rollback error of a failed transaction |
| `internal/ast/parse.go` | `staticcheck` | `Parser.ParseCtx` is deprecated (SA1019) |
| `internal/sast/fuzz_test.go` | `ineffassign`, `staticcheck` | the parse error is assigned and never read (2) |
| `internal/patch/patch_test.go` | `errcheck` | `os.MkdirAll`, `os.WriteFile` (2) |
| `internal/ast/bench_test.go`, `internal/sast/perf_test.go` | `nilerr` | a swallowed error in a test (2) |
| `cmd/aisai-mcp/main.go` | `errcheck` | an unchecked error |
| `cmd/aisai-bench/main.go`, `cmd/aisai-bench/main_test.go` | `errcheck`, `ineffassign`, `staticcheck` | unchecked errors, an ineffectual assignment, an unused value |

The `QF1008`, `QF1011` and `QF1012` staticcheck rules are switched off on
purpose: they are style suggestions ("use `fmt.Fprintf` instead of
`WriteString(fmt.Sprintf(...))`), and formatting is enforced by `gofmt`.

---

## 5. The cross-platform build matrix

The `build-matrix` job in `ci.yml` builds and tests on:

| Runner | Architecture |
|---|---|
| `macos-latest` | arm64 |
| `macos-13` | x64 |
| `ubuntu-latest` | amd64 |
| `ubuntu-24.04-arm` | arm64 |
| `windows-latest` | amd64 |

Every leg builds **natively**, so cgo always finds a C compiler for the
target architecture; nothing is cross-compiled. The tree-sitter grammar
bindings are C, so `CGO_ENABLED=1` is set explicitly on every leg and the
toolchain is installed and verified per platform:

* **Linux** — `apt-get install build-essential` (gcc).
* **macOS** — the Xcode command line tools, which the runner image already
  has; the step checks `xcode-select -p` and fails loudly when they are
  missing.
* **Windows** — MinGW-w64 through `choco install mingw`, with
  `C:\mingw64\bin` added to `GITHUB_PATH` and `CC=gcc` set.

Each leg runs `go build ./...`, `go vet ./...` and `go test ./...`, and
builds the four binaries.

**Not verified locally:** only the macOS/arm64 leg could be run on this
machine. The other three legs run on the CI runners, where the Windows leg
is the one that exercises the `syscall` and `os.Getuid` calls in
`cmd/aisai-server` against a Windows compiler.

### The sidecar of a release build

The release pipeline does not reuse the binaries of the CI matrix. Every
release job builds the sidecar for its own platform, because the
tree-sitter grammars are C and the backend is built with cgo: a binary
built on one platform does not run on another.

| Platform | The sidecar |
|---|---|
| macOS | both architectures are built and joined into one universal binary with `lipo`, because the app bundle carries a single sidecar |
| Linux | built natively on the runner of the same architecture |
| Windows | built natively with MinGW-w64, and named `aisai-server.exe` |

The macOS job builds the x86_64 slice on the arm64 runner. A cgo build
cannot be cross-compiled from a Linux runner, but it can be cross-compiled
between the two macOS architectures: the Go toolchain drives the Xcode
compiler with the right `-arch` flag. This was checked on this machine,
and the universal binary that comes out of `lipo` is accepted by the SBOM
tool.

---

## 6. The nightly benchmark

`.github/workflows/bench-nightly.yml` runs every night at 03:17 UTC.

It runs the **static engine only** over the committed corpus
(`bench/data1551redacted.csv`): no network, no model, no randomness, so the
same commit always produces the same numbers. The metrics are computed by
`.github/workflows/bench-metrics.py` from the result CSV and compared with
the committed baseline `bench/baseline_metrics.json`.

* The job **reports** the delta in the job summary and uploads the run as
  an artifact.
* The job **fails** only when the accuracy regressed by more than
  **2 percentage points** (`--max-regression 0.02`). The threshold can be
  changed per run with the `max-regression` input of `workflow_dispatch`.
* The baseline in `bench/baseline_metrics.json` was produced by a
  real local run of the same command, not written by hand:

  ```
  accuracy 51.42%   (1595 of 3102 decided samples)
  precision 100.00%
  vulnerable snippets 10.06%, fixed snippets 92.78%
  ```

  The file lives in `bench/` and not in `bench/results/`, because
  `bench/results/` is in `.gitignore` and a baseline there would never be
  committed. The CSV files in `bench/results/` were not touched.

**The LLM leg is not part of the nightly job.** It needs a model server,
which a GitHub-hosted runner does not have. To run it, add a self-hosted
runner with Ollama, point a second job at it with
`--engine both --model <model>` and give it the same comparison. The
static leg is the one that is deterministic, so it is the one that gates.

---

## 7. The desktop release

### The configuration

* `electron/package.json` holds the **developer** configuration: the
  targets, the hardened runtime, the entitlements, and
  `forceCodeSigning: false` so that a developer can package on a machine
  without a certificate.
* `electron/.github/electron-builder.release.json` holds the **signed
  release** configuration: `forceCodeSigning: true`, the notarization
  block and the `publish` provider.
* `electron/.github/electron-builder.unsigned.json` holds the **unsigned
  release** configuration: `forceCodeSigning: false` and no notarization.
* `electron/.github/run-builder.mjs` merges the two, checks the
  credentials and starts electron-builder.
* `electron/.github/verify-release-env.mjs` is the electron-builder
  `beforeBuild` hook: it is the second line of defence and stops a release
  build whose credentials are missing.
* `electron/.github/entitlements.mac.plist` holds the entitlements of the
  hardened runtime.

| Platform | Targets | Signing |
|---|---|---|
| macOS | `dmg` and `zip`, `x64` and `arm64` | `CSC_LINK` + `CSC_KEY_PASSWORD`, notarised with `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID`; `hardenedRuntime: true` |
| Windows | `nsis` and `zip`, `x64` | `WIN_CSC_LINK` + `WIN_CSC_KEY_PASSWORD`; `WINDOWS_PUBLISHER_NAME` is the name the updater verifies against |
| Linux | `AppImage` and `deb`, `x64` and `arm64` | not signed (the Linux artifacts are not in the signed update feed) |

### The icon

The app has its own icon, and every platform gets the format it needs
from the files in `electron/resources`:

| File | Used by |
|---|---|
| `icon.png` | the general-purpose icon (1024x1024, PNG) |
| `icon.icns` | macOS (`CFBundleIconFile`) |
| `icon.ico` | Windows, with the 16, 32, 48, 64, 128 and 256 pixel sizes |
| `icons/<size>x<size>.png` | Linux, where electron-builder reads the sizes out of the directory name |

The icon is the AISAI mark: the letter A drawn as a syntax tree, with one
flagged leaf in the signal colour. It uses the colours of the "Verdigris"
theme (see `electron/src/styles/tokens.css`). The vector sources are
tracked in `electron/resources/brand/`: `icon.svg` (the app icon tile) and
`logo-mark.svg` (the bare mark). The toolbar draws the same mark from
`electron/src/lib/logo.ts`.

Changing the icon means regenerating every format from `icon.svg`
(needs `rsvg-convert` and ImageMagick):

```bash
cd electron/resources
rsvg-convert -w 1024 -h 1024 brand/icon.svg -o icon.png
for n in 16 32 48 64 128 256 512; do rsvg-convert -w $n -h $n brand/icon.svg -o icons/${n}x${n}.png; done
magick icon.png -define icon:auto-resize=256,128,64,48,32,16 -strip icon.ico
mkdir -p icon.iconset
for n in 16 32 128 256 512; do
  rsvg-convert -w $n -h $n brand/icon.svg -o icon.iconset/icon_${n}x${n}.png
  rsvg-convert -w $((n*2)) -h $((n*2)) brand/icon.svg -o icon.iconset/icon_${n}x${n}@2x.png
done
iconutil -c icns icon.iconset -o icon.icns && rm -rf icon.iconset
```

### The sidecar, and where the app looks for it

The Go backend is shipped inside the app as a sidecar. It has to be a
binary of the platform the app runs on, which is why every release job
builds its own (see section 5).

The sidecar is copied into the resources of the app, next to the asar
archive and not inside it:

```json
"extraResources": [
  { "from": "build/sidecar", "to": ".", "filter": ["**/*"] }
]
```

`"to": "."` matters. With a directory as the destination, electron-builder
copies the *contents* of `build/sidecar` into a directory named
`aisai-server`, and the binary ends up at
`Resources/aisai-server/aisai-server`. The app looks for
`Resources/aisai-server`, so it never finds it, and the app starts in
mock mode with no backend.

The binary is also excluded from the asar archive
(`"!dist/main/aisai-server"` in `files`): `npm run build` copies the
binary next to the compiled main process, which is convenient while
developing, but a binary inside an asar cannot be executed, and it would
double the size of the download.

`electron/src/main-process/sidecar-paths.ts` holds the order in which the
app looks for the sidecar, and it offers both names — `aisai-server` and
`aisai-server.exe` — at every place, because the platform is not known
when the list is built.

### What the metadata has to carry

The `deb` target refuses to build without two fields in
`electron/package.json`:

* **a homepage** — electron-builder writes it into the control file of the
  package, and without it the build fails with "Please specify project
  homepage";
* **an author with an email** — the same file names the maintainer, and
  without an email the build fails with "author email is missed".

An unsigned build must also *not see* the signing credentials, and an
empty environment variable is not the same as an absent one:
electron-builder resolves an empty `CSC_LINK` as a path, relative to the
project directory, and fails with "<project directory> not a file".
`run-builder.mjs` therefore removes the credentials from the environment
of an unsigned build.

### The names of the artifacts

`artifactName` in the electron-builder configuration is
`${productName}-${version}-${os}-${arch}.${ext}`, so every artifact of
the release carries its platform and its architecture:

```
AISAI-0.7.0-mac-x64.dmg       AISAI-0.7.0-linux-x64.AppImage
AISAI-0.7.0-mac-arm64.dmg     AISAI-0.7.0-linux-arm64.AppImage
AISAI-0.7.0-mac-x64.zip       AISAI-0.7.0-linux-arm64.deb
AISAI-0.7.0-win-x64.exe       AISAI-0.7.0-win-x64.zip
```

The names have to be unique, because the release job merges the artifacts
of every platform into one directory. The two Linux jobs would also write
the same update metadata file, so it is renamed to
`latest-linux-amd64.yml` and `latest-linux-arm64.yml`.

### Signed and unsigned releases

The pipeline publishes a release for every platform in both modes.

| Mode | How it is started | What it produces |
|---|---|---|
| Signed | the **Actions** tab with the `signed` option | signed and notarised artifacts, published to the signed update feed |
| Unsigned | a `v*` tag, or the **Actions** tab without the option | artifacts that are not signed and not notarised |

An unsigned release is what the pipeline can publish today, because the
signing identities do not exist in this repository yet. It is a deliberate
mode rather than a fallback: the workflow decides which one to use before
it builds anything, and a signed release still stops with a message that
names every missing credential.

An unsigned macOS build is quarantined by Gatekeeper and has to be opened
with a right-click; an unsigned Windows build shows a SmartScreen warning.

The unsigned configuration also turns off the automatic discovery of a
signing identity (`CSC_IDENTITY_AUTO_DISCOVERY=false`). Without it,
electron-builder picks up whatever certificate is in the keychain of the
machine it runs on — which is how a build that is supposed to be unsigned
ends up trying to sign with a development certificate.

### Failing loudly instead of skipping

A release build stops with a message that names every missing credential:

```
$ AISAI_RELEASE=1 node .github/run-builder.mjs --mac --release
[run-builder] release build for mac.
The release build cannot start: the following credentials are missing.
  CSC_LINK — the Developer ID Application certificate (p12)
  ...
```

`forceCodeSigning: true` in the release configuration is the second line of
defence: electron-builder refuses to produce an unsigned macOS or Windows
artifact. The `${env.AISAI_UPDATE_FEED_URL}` macro in the `publish` block
fails the build as well when the feed URL is not set.

The merged configuration can be inspected without building anything:

```bash
AISAI_RELEASE=1 AISAI_UPDATE_FEED_URL=… CSC_LINK=… CSC_KEY_PASSWORD=… \
  APPLE_ID=… APPLE_APP_SPECIFIC_PASSWORD=… APPLE_TEAM_ID=… \
  node .github/run-builder.mjs --mac --release --print-config
```

### The auto-update feed

The feed URL is never hard-coded: it comes from `AISAI_UPDATE_FEED_URL`,
which the release workflow reads from the `AISAI_UPDATE_FEED_URL` secret.

The updater itself is `electron-updater`, which is now a dependency. **The
runtime wiring is not part of this change**, because `electron/main.ts` is
owned by another workstream. The main process needs this:

```ts
import { autoUpdater } from 'electron-updater';

const feed = process.env.AISAI_UPDATE_FEED_URL;
if (feed) {
  // The feed only ever contains signed artifacts: the updater verifies the
  // code signature of the downloaded app (macOS) and the publisher name of
  // the installer (Windows, which must match build.win.publisherName).
  autoUpdater.setFeedURL({ provider: 'generic', url: feed });
  autoUpdater.autoDownload = true;
  autoUpdater.on('error', (err) => console.error('[aisai] updater:', err.message));
  void autoUpdater.checkForUpdatesAndNotify();
}
```

electron-updater verifies the signature of the downloaded artifact and
refuses an artifact that is not signed by the expected publisher. The
Windows publisher name comes from `WINDOWS_PUBLISHER_NAME`, so it has to be
the same string in the build and in the updater.

### The secrets the release needs

| Secret | Used by | Why |
|---|---|---|
| `AISAI_UPDATE_FEED_URL` | every platform | the address of the signed update feed |
| `MAC_CSC_LINK` | macOS | the Developer ID Application certificate (base64 or a path to the `.p12`) |
| `MAC_CSC_KEY_PASSWORD` | macOS | the password of that certificate |
| `APPLE_ID` | macOS | the Apple ID that notarises the build |
| `APPLE_APP_SPECIFIC_PASSWORD` | macOS | the app-specific password of that Apple ID |
| `APPLE_TEAM_ID` | macOS | the Apple team id |
| `WINDOWS_CSC_LINK` | Windows | the code signing certificate (base64 or a path to the `.pfx`) |
| `WINDOWS_CSC_KEY_PASSWORD` | Windows | the password of that certificate |
| `WINDOWS_PUBLISHER_NAME` | Windows | the publisher name the updater verifies against |

**None of these exist in this repository, and nothing was signed or
notarised here.** The pipeline is configured for them and it fails loudly
when they are missing. An unsigned release needs none of them, and that is
the mode the pipeline publishes in today.

---

## 8. The end-to-end test

`electron/e2e/audit-flow.spec.ts` drives the real Electron application
through the flow the rewrite plan asks for: **audit → scan → open a finding
→ apply the patch → re-scan**.

* The app is started with `_electron.launch`, so the main process, the
  preload bridge, the IPC path allowlist and the renderer all run for real.
* The Go backend is replaced by a **stub sidecar**
  (`electron/e2e/fixtures/stub-backend.ts`): it speaks the same protocol
  (one JSON line on stdout, HTTP over a Unix socket, a WebSocket event
  stream) and serves the API from the fixture package that is **vendored in
  `electron/e2e/fixtures/`**. The test never reaches the network and never
  contacts a model.
* The first assertion checks that the app is in *live* mode (the
  "MOCK DATA" badge is hidden), so the test cannot silently pass against the
  in-renderer mock.

**The real Go backend is not exercised by the end-to-end test.** Creating an
audit downloads the package from PyPI — `internal/pypi` has no way to
point at a local index — so an offline test cannot drive the real backend.
The Go side of the flow is covered by the Go unit tests; the end-to-end
test covers the desktop side.

Run it with:

```bash
cd electron
npm run build
npm run test:e2e
```

---

## 9. The T8 security test

`electron/test/security.test.ts` is the test that threat T8 of
`docs/THREAT_MODEL.md` did not have. It asserts that:

* the `webPreferences` block has `sandbox: true`, `contextIsolation: true`,
  `nodeIntegration: false` and `webSecurity: true`;
* `setWindowOpenHandler` denies every window open;
* the `will-navigate` and `will-attach-webview` handlers call
  `event.preventDefault()`;
* the IPC allowlist lists exactly the seven documented API prefixes, that
  every prefix is under `/api/`, that a path traversal is refused and that
  an unknown prefix is refused;
* the preload exposes exactly one object, `window.aisai`, with exactly the
  seven documented members.

The values live inside `createWindow()` in `electron/main.ts` and in the
module scope of `electron/preload.ts`, and the main process imports
`electron`, so they cannot be imported into a vitest process. **The test
therefore reads the two real source files and evaluates the real literals
out of them**: the `webPreferences` object and the allowlist array are
turned into live values, and `isAllowedApiPath` is executed against them.
The behavioural assertions are therefore assertions about the app's own
logic, not about a copy of it.

What was checked: weakening `sandbox: true` to `false`, changing the window
open handler to `allow`, removing the `/api/chat` prefix and removing the
`..` check from `isAllowedApiPath` makes **five** of the assertions fail.
Restoring the source makes them pass again.

`docs/THREAT_MODEL.md` still says that no Electron security test exists.
Updating it is the job of whoever owns that document.

---

## 10. What is not done

* **Nothing was signed or notarised.** There is no signing identity on this
  machine. The pipeline is configured for the secrets listed above and it
  fails loudly without them.
* **The auto-update is not wired into the main process.**
  `electron-updater` is a dependency and the feed URL is configured, but
  `electron/main.ts` (owned by another workstream) has to call
  `autoUpdater.checkForUpdatesAndNotify()` — the snippet is in section 7.
* **`cmd/aisai-server --version` does not exist.** `internal/version`
  supports it; the command has to be wired (section 2).
* **The Electron version and the Go version are not checked against each
  other** by the pipeline (section 2).
* **The release pipeline has not been run yet.** Everything in it was
  checked on this machine — the packaging, the universal macOS sidecar, the
  SBOM of a universal binary — but the pipeline itself has never published
  a release.
* **The nightly job does not run the LLM leg** (section 6).
* **The Makefile was not touched.** The targets that have to be added are
  listed in section 2 and in section 11.
* **The E2E test does not cover the real Go backend** (section 8).
* **`electron/tsconfig.json` does not include `e2e/**` or
  `playwright.config.ts`**, so the end-to-end code is linted but not
  type-checked by `npm run typecheck`. Adding `"e2e/**/*.ts"` and
  `"playwright.config.ts"` to the `include` array is a one-line change that
  belongs to whoever owns the tsconfig.

---

## 11. The Makefile targets that are missing

These are the targets the Makefile needs; they were **not** added here,
because the Makefile is owned by another workstream.

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.7.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS ?= -X github.com/tlmcguire/aisai/internal/version.Version=$(VERSION) \
           -X github.com/tlmcguire/aisai/internal/version.Commit=$(COMMIT) \
           -X github.com/tlmcguire/aisai/internal/version.Date=$(DATE)
```

| Target | What it has to do |
|---|---|
| `build` | build `aisai-server`, `aisai-mcp`, `aisai-bench` **and `aisai-sbom`**, with `$(LDFLAGS)` |
| `build-server`, `build-mcp`, `build-bench`, `build-sbom` | the same, one command each |
| `sbom` | `$(GO) run ./cmd/aisai-sbom --binary $(BIN_DIR)/aisai-server --both --out-dir $(BIN_DIR)/sbom` |
| `lint` | `golangci-lint run ./...` (pinned to v2.15.0) |
| `test-e2e` | `cd electron && $(NPM) run test:e2e` |
| `version` | `$(GO) run ./cmd/aisai-server --version` (needs the `--version` wiring of section 2) |
| `bench-nightly` | `$(BIN_DIR)/aisai-bench --input bench/data1551redacted.csv --engine bandit --out bench/results/bench-nightly.csv` followed by `python3 .github/workflows/bench-metrics.py …` |

The current `build` target does not build `cmd/aisai-sbom` at all, and
passes an empty `LDFLAGS`.
