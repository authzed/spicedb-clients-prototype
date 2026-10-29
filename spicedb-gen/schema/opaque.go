package schema

import (
	"fmt"
	"strings"

	"github.com/authzed/spicedb/pkg/schemadsl/input"
	"github.com/authzed/spicedb/pkg/schemadsl/lexer"
)

// opaqueCaveatBodies replaces every caveat's CEL expression with `true`,
// keeping the original line structure so compiler error positions still point
// at the right line.
//
// Generators need only a caveat's name and parameter types, never its
// expression. SpiceDB's compiler type-checks expressions against its default
// CEL environment, so a production schema whose caveats call functions that
// environment does not declare (custom functions registered by the server
// that runs it) would otherwise fail generation with "undeclared reference".
// Expressions are still validated where they matter: when the schema is
// written to SpiceDB.
//
// Bodies are found with SpiceDB's own schema lexer, so braces inside strings,
// map literals and comments never end a body early.
func opaqueCaveatBodies(schemaText string) (string, error) {
	lex := lexer.NewFlaggableLexer(lexer.Lex(input.Source("schema"), schemaText))
	defer lex.Close()

	type span struct{ start, end int }
	var bodies []span
	const (
		scanning = iota
		inParams
		awaitingBody
		inBody
	)
	state, depth, start := scanning, 0, 0
	for {
		tok := lex.NextToken()
		switch tok.Kind {
		case lexer.TokenTypeEOF:
			if state != scanning {
				return "", fmt.Errorf("unterminated caveat definition")
			}
			out := schemaText
			for i := len(bodies) - 1; i >= 0; i-- {
				b := bodies[i]
				body := out[b.start:b.end]
				out = out[:b.start] + " true " + strings.Repeat("\n", strings.Count(body, "\n")) + out[b.end:]
			}
			return out, nil
		case lexer.TokenTypeError:
			// Leave lexical errors to the compiler, which reports them with
			// positions and context.
			return schemaText, nil
		}
		switch state {
		case scanning:
			if tok.Kind == lexer.TokenTypeKeyword && tok.Value == "caveat" {
				state, depth = inParams, 0
			}
		case inParams:
			switch tok.Kind {
			case lexer.TokenTypeLeftParen:
				depth++
			case lexer.TokenTypeRightParen:
				depth--
				if depth == 0 {
					state = awaitingBody
				}
			}
		case awaitingBody:
			if tok.Kind == lexer.TokenTypeLeftBrace {
				state, depth, start = inBody, 1, int(tok.Position)+1
			}
		case inBody:
			switch tok.Kind {
			case lexer.TokenTypeLeftBrace:
				depth++
			case lexer.TokenTypeRightBrace:
				depth--
				if depth == 0 {
					bodies = append(bodies, span{start, int(tok.Position)})
					state = scanning
				}
			}
		}
	}
}
