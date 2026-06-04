// Package custom is a compilation target for user-provided custom middleware.
// At generate-time, the CLI copies custom .go files into this package directory.
// Each file uses init() to self-register via the gateway SDK.
//
// When no custom middleware exists, this package compiles to nothing.
package custom
