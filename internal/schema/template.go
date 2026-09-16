package schema

import (
	"fmt"
	"strings"
)

// RefKind is the kind of a ${...} reference.
type RefKind int

const (
	RefHost      RefKind = iota // ${<component>.host}
	RefPort                     // ${<component>.port}
	RefNamedPort                // ${<component>.ports.<name>}
	RefSecret                   // ${secrets.<name>}
)

// Ref is a parsed ${...} reference.
type Ref struct {
	Raw       string // the text between ${ and }
	Kind      RefKind
	Component string // RefHost, RefPort, RefNamedPort
	Port      string // RefNamedPort
	Secret    string // RefSecret
}

// Token is one piece of a template string: either literal text or a reference.
type Token struct {
	Literal string
	Ref     *Ref
}

// ParseTemplate splits a string from env, command or args into literals and references.
// The syntax follows docker compose: ${...} is a reference, $$ is a literal $, and any other
// $ is literal as well. Literal tokens hold the unescaped text.
func ParseTemplate(s string) ([]Token, error) {
	var tokens []Token
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, Token{Literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 == len(s) {
			lit.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			lit.WriteByte('$')
			i++
		case '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unterminated reference %q (write $$ for a literal $)", s[i:])
			}
			ref, err := parseRef(s[i+2 : i+2+end])
			if err != nil {
				return nil, err
			}
			flush()
			tokens = append(tokens, Token{Ref: ref})
			i += 2 + end
		default:
			lit.WriteByte('$')
		}
	}
	flush()
	return tokens, nil
}

func parseRef(raw string) (*Ref, error) {
	parts := strings.Split(raw, ".")
	switch {
	case len(parts) == 2 && parts[0] == "secrets" && parts[1] != "":
		return &Ref{Raw: raw, Kind: RefSecret, Secret: parts[1]}, nil
	case len(parts) == 2 && parts[0] != "" && parts[1] == "host":
		return &Ref{Raw: raw, Kind: RefHost, Component: parts[0]}, nil
	case len(parts) == 2 && parts[0] != "" && parts[1] == "port":
		return &Ref{Raw: raw, Kind: RefPort, Component: parts[0]}, nil
	case len(parts) == 3 && parts[0] != "" && parts[1] == "ports" && parts[2] != "":
		return &Ref{Raw: raw, Kind: RefNamedPort, Component: parts[0], Port: parts[2]}, nil
	}
	return nil, fmt.Errorf("unknown reference ${%s} (supported: ${<component>.host}, "+
		"${<component>.port}, ${<component>.ports.<name>}, ${secrets.<name>}; write $$ for a literal $)", raw)
}
