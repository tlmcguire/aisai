// This file exists so that the Go toolchain prunes the electron/ directory
// from the root module's package patterns.
//
// electron/node_modules contains third-party packages, one of which ships a
// stray Go package. Without this go.mod, `go build ./...` and `go test ./...`
// would descend into node_modules and try to build it.
//
// The electron/ tree is JavaScript and TypeScript; it has no Go code, and its
// dependencies are managed by npm.
module github.com/tlmcguire/aisai/electron

go 1.25
