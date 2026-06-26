package expr

import (
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tokNum    tokKind = iota
	tokIdent          // bare or dotted/colon identifier
	tokLParen         // (
	tokRParen         // )
	tokPlus           // +
	tokMinus          // -
	tokStar           // *
	tokSlash          // /
	tokGT             // >
	tokLT             // <
	tokGTE            // >=
	tokLTE            // <=
	tokEQ             // ==
	tokNEQ            // !=
	tokAmp            // &
	tokPipe           // |
	tokBang           // !
	tokEOF
)

type token struct {
	kind tokKind
	text string  // original text (idents and nums)
	num  float64 // parsed value for tokNum
	pos  int     // byte offset for error messages
}

// lex tokenises src into a flat token slice ending in tokEOF.
// Numeric literals are non-negative (no unary minus — OQ-C).
// Identifiers may contain letters, digits, '_', '.', ':' (dotted/colon attrs).
func lex(src string) ([]token, error) {
	var tokens []token
	i := 0
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		pos := i
		switch {
		case r >= '0' && r <= '9':
			j := i + 1
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j < len(src) && src[j] == '.' {
				j++ // consume '.'
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			text := src[i:j]
			val, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("expr: invalid numeric literal %q at pos %d", text, pos)
			}
			tokens = append(tokens, token{kind: tokNum, text: text, num: val, pos: pos})
			i = j

		case r == '_' || unicode.IsLetter(r):
			j := i + size
			for j < len(src) {
				c, sz := utf8.DecodeRuneInString(src[j:])
				if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.' || c == ':' {
					j += sz
				} else {
					break
				}
			}
			tokens = append(tokens, token{kind: tokIdent, text: src[i:j], pos: pos})
			i = j

		case r == '(':
			tokens = append(tokens, token{kind: tokLParen, text: "(", pos: pos})
			i++
		case r == ')':
			tokens = append(tokens, token{kind: tokRParen, text: ")", pos: pos})
			i++
		case r == '+':
			tokens = append(tokens, token{kind: tokPlus, text: "+", pos: pos})
			i++
		case r == '-':
			tokens = append(tokens, token{kind: tokMinus, text: "-", pos: pos})
			i++
		case r == '*':
			tokens = append(tokens, token{kind: tokStar, text: "*", pos: pos})
			i++
		case r == '/':
			tokens = append(tokens, token{kind: tokSlash, text: "/", pos: pos})
			i++

		case r == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				tokens = append(tokens, token{kind: tokGTE, text: ">=", pos: pos})
				i += 2
			} else {
				tokens = append(tokens, token{kind: tokGT, text: ">", pos: pos})
				i++
			}
		case r == '<':
			if i+1 < len(src) && src[i+1] == '=' {
				tokens = append(tokens, token{kind: tokLTE, text: "<=", pos: pos})
				i += 2
			} else {
				tokens = append(tokens, token{kind: tokLT, text: "<", pos: pos})
				i++
			}
		case r == '=':
			if i+1 < len(src) && src[i+1] == '=' {
				tokens = append(tokens, token{kind: tokEQ, text: "==", pos: pos})
				i += 2
			} else {
				return nil, fmt.Errorf("expr: unexpected '=' at pos %d (use '==')", pos)
			}
		case r == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				tokens = append(tokens, token{kind: tokNEQ, text: "!=", pos: pos})
				i += 2
			} else {
				tokens = append(tokens, token{kind: tokBang, text: "!", pos: pos})
				i++
			}
		case r == '&':
			tokens = append(tokens, token{kind: tokAmp, text: "&", pos: pos})
			i++
		case r == '|':
			tokens = append(tokens, token{kind: tokPipe, text: "|", pos: pos})
			i++

		default:
			return nil, fmt.Errorf("expr: unexpected character %q at pos %d", r, pos)
		}
	}
	tokens = append(tokens, token{kind: tokEOF, pos: len(src)})
	return tokens, nil
}
