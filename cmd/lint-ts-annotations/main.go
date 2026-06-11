// Command lint-ts-annotations checks that all .Set() calls on JS-exposed objects
// in the gateway jsruntime package have a corresponding @ts-method or @ts-prop annotation.
//
// Usage:
//
//	go run ./cmd/lint-ts-annotations
//
// Exit code 1 if any .Set() calls are missing annotations.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sourceDir = "core/gateway/internal/jsruntime"

	// Match lines like: _ = requestObj.Set("method", ...)  or  _ = cryptoObj.Set("sha256", ...)
	reSetCall = regexp.MustCompile(`_\s*=\s*\w+Obj\.Set\("(\w+)"`)

	// Match annotation comments
	reAnnotation = regexp.MustCompile(`//\s*@ts-(method|prop|skip)\s*`)
)

type violation struct {
	File    string
	Line    int
	SetName string
}

func main() {
	violations, err := lint(sourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(violations) == 0 {
		fmt.Println("ok: all .Set() calls have @ts annotations")
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "FAIL: %d .Set() calls missing @ts-method or @ts-prop annotation:\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s:%d — .Set(\"%s\", ...) has no preceding @ts annotation\n", v.File, v.Line, v.SetName)
	}
	fmt.Fprintf(os.Stderr, "\nAdd a comment like:\n  // @ts-method gw.crypto.%s(args): returnType\n  // @ts-prop ctx.request.%s: readonly name: type\n", "example", "example")
	os.Exit(1)
}

func lint(dir string) ([]violation, error) {
	var violations []violation

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fileViolations, err := lintFile(path)
		if err != nil {
			return nil, fmt.Errorf("lint %s: %w", path, err)
		}
		violations = append(violations, fileViolations...)
	}

	return violations, nil
}

func lintFile(path string) ([]violation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	var violations []violation
	scanner := bufio.NewScanner(f)
	lineNum := 0
	prevLineHadAnnotation := false

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if reAnnotation.MatchString(line) {
			prevLineHadAnnotation = true
			continue
		}

		if m := reSetCall.FindStringSubmatch(line); m != nil {
			if !prevLineHadAnnotation {
				violations = append(violations, violation{
					File:    path,
					Line:    lineNum,
					SetName: m[1],
				})
			}
		}

		prevLineHadAnnotation = false
	}

	return violations, scanner.Err()
}
