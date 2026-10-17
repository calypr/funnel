// Package shellwords provides POSIX shell-style word splitting, replacing the
// kballard/go-shellquote and google/shlex dependencies with mvdan.cc/sh/v3.
package shellwords

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Split splits the given command line string into words according to POSIX
// shell word-splitting rules, removing quotes and resolving backslash escapes.
//
// No variable or parameter expansion is performed: expansions survive as
// literal text for the execution environment (typically a container shell) to
// interpret. Command and process substitution are rejected because they would
// require running a shell locally.
func Split(s string) ([]string, error) {
	// A syntax.Parser is not safe for concurrent use, and Split runs on
	// concurrent worker goroutines.
	f, err := syntax.NewParser().Parse(strings.NewReader(s), "")
	if err != nil {
		return nil, err
	}
	var words []*syntax.Word
	for _, stmt := range f.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok {
			return nil, fmt.Errorf("expected a simple command, got %T", stmt.Cmd)
		}
		for _, arg := range call.Args {
			lit, err := literalWord(s, arg)
			if err != nil {
				return nil, err
			}
			words = append(words, lit)
		}
	}
	return expand.Fields(&expand.Config{}, words...)
}

// literalWord rewrites a parsed word so every expansion becomes single-quoted
// literal text. expand.Fields then performs only word splitting on unquoted
// whitespace, quote removal, and escape resolution.
func literalWord(src string, w *syntax.Word) (*syntax.Word, error) {
	parts, err := literalParts(src, w.Parts)
	if err != nil {
		return nil, err
	}
	return &syntax.Word{Parts: parts}, nil
}

func literalParts(src string, parts []syntax.WordPart) ([]syntax.WordPart, error) {
	var out []syntax.WordPart
	for _, p := range parts {
		switch pt := p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
			out = append(out, p)
		case *syntax.DblQuoted:
			inner, err := literalParts(src, pt.Parts)
			if err != nil {
				return nil, err
			}
			out = append(out, &syntax.DblQuoted{Parts: inner})
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return nil, fmt.Errorf("command substitution is not supported: %s", src[p.Pos().Offset():p.End().Offset()])
		default:
			// ParamExp, ArithmExp, and anything else expansion-shaped stays
			// as its original source text.
			out = append(out, &syntax.SglQuoted{Value: src[p.Pos().Offset():p.End().Offset()]})
		}
	}
	return out, nil
}
