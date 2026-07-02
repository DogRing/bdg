package expr

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Parse compiles §6 formula text into an immutable Program.
// All static validation (identifier existence, type correctness, result kind) happens here.
// Returns a descriptive error — never a partial Program — on any violation.
func Parse(text string, want Kind, knownStats StatSet, knownPreds []KnownPred) (*Program, error) {
	// Build name→KnownPred lookup (O(1) later).
	predByName := make(map[string]KnownPred, len(knownPreds))
	for _, kp := range knownPreds {
		predByName[kp.Name] = kp
	}

	tokens, err := lex(text)
	if err != nil {
		return nil, err
	}

	p := &parser{tokens: tokens}
	root, err := p.parseExpr(knownStats, predByName)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		tok := p.peek()
		return nil, fmt.Errorf("expr: unexpected token %q at pos %d", tok.text, tok.pos)
	}

	// Static type inference pass.
	if err := typecheck(root); err != nil {
		return nil, err
	}

	// Assert the whole-program result kind matches the call-site expectation.
	if root.inferred != want {
		return nil, fmt.Errorf("expr: formula yields %s but site requires %s",
			kindStr(root.inferred), kindStr(want))
	}

	reads, readsAttrs, readsPreds := collectReads(root)
	return &Program{
		root:       root,
		resultKind: root.inferred,
		reads:      reads,
		readsAttrs: readsAttrs,
		readsPreds: readsPreds,
	}, nil
}

func kindStr(k Kind) string {
	if k == KindNum {
		return "KindNum"
	}
	return "KindBool"
}

// ── Recursive-descent parser ─────────────────────────────────────────────────
// Precedence (low→high): | < & < comparisons < + - < * / < unary! < primary

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }
func (p *parser) consume() token {
	t := p.tokens[p.pos]
	p.pos++
	return t
}

func (p *parser) parseExpr(ks StatSet, preds map[string]KnownPred) (*node, error) {
	return p.parseOr(ks, preds)
}

// parseOr: left | right (lowest precedence)
func (p *parser) parseOr(ks StatSet, preds map[string]KnownPred) (*node, error) {
	left, err := p.parseAnd(ks, preds)
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokPipe {
		p.consume()
		right, err := p.parseAnd(ks, preds)
		if err != nil {
			return nil, err
		}
		left = &node{kind: nOr, left: left, right: right}
	}
	return left, nil
}

// parseAnd: left & right
func (p *parser) parseAnd(ks StatSet, preds map[string]KnownPred) (*node, error) {
	left, err := p.parseCmp(ks, preds)
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokAmp {
		p.consume()
		right, err := p.parseCmp(ks, preds)
		if err != nil {
			return nil, err
		}
		left = &node{kind: nAnd, left: left, right: right}
	}
	return left, nil
}

// parseCmp: left > < >= <= == != right
func (p *parser) parseCmp(ks StatSet, preds map[string]KnownPred) (*node, error) {
	left, err := p.parseAddSub(ks, preds)
	if err != nil {
		return nil, err
	}
	for {
		var nk nodeKind
		switch p.peek().kind {
		case tokGT:
			nk = nGT
		case tokLT:
			nk = nLT
		case tokGTE:
			nk = nGTE
		case tokLTE:
			nk = nLTE
		case tokEQ:
			nk = nEQ
		case tokNEQ:
			nk = nNEQ
		default:
			return left, nil
		}
		p.consume()
		right, err := p.parseAddSub(ks, preds)
		if err != nil {
			return nil, err
		}
		left = &node{kind: nk, left: left, right: right}
	}
}

// parseAddSub: left + right, left - right
func (p *parser) parseAddSub(ks StatSet, preds map[string]KnownPred) (*node, error) {
	left, err := p.parseMulDiv(ks, preds)
	if err != nil {
		return nil, err
	}
	for {
		var nk nodeKind
		switch p.peek().kind {
		case tokPlus:
			nk = nAdd
		case tokMinus:
			nk = nSub
		default:
			return left, nil
		}
		p.consume()
		right, err := p.parseMulDiv(ks, preds)
		if err != nil {
			return nil, err
		}
		left = &node{kind: nk, left: left, right: right}
	}
}

// parseMulDiv: left * right, left / right
func (p *parser) parseMulDiv(ks StatSet, preds map[string]KnownPred) (*node, error) {
	left, err := p.parseUnary(ks, preds)
	if err != nil {
		return nil, err
	}
	for {
		var nk nodeKind
		switch p.peek().kind {
		case tokStar:
			nk = nMul
		case tokSlash:
			nk = nDiv
		default:
			return left, nil
		}
		p.consume()
		right, err := p.parseUnary(ks, preds)
		if err != nil {
			return nil, err
		}
		left = &node{kind: nk, left: left, right: right}
	}
}

// parseUnary: !operand (only unary op; no unary minus — OQ-C)
func (p *parser) parseUnary(ks StatSet, preds map[string]KnownPred) (*node, error) {
	if p.peek().kind == tokBang {
		p.consume()
		operand, err := p.parseUnary(ks, preds)
		if err != nil {
			return nil, err
		}
		return &node{kind: nNot, left: operand}, nil
	}
	return p.parsePrimary(ks, preds)
}

// parsePrimary: NUM | IDENT | IDENT(arg) | ( expr )
func (p *parser) parsePrimary(ks StatSet, preds map[string]KnownPred) (*node, error) {
	tok := p.peek()
	switch tok.kind {
	case tokNum:
		p.consume()
		return &node{kind: nLit, num: tok.num}, nil

	case tokLParen:
		p.consume()
		inner, err := p.parseExpr(ks, preds)
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("expr: expected ')' at pos %d", p.peek().pos)
		}
		p.consume()
		return inner, nil

	case tokIdent:
		name := tok.text
		p.consume()
		if p.peek().kind == tokLParen {
			return p.parsePredCall(name, preds, tok.pos)
		}
		return classifyBare(name, tok.pos, ks, preds)

	default:
		if tok.kind == tokEOF {
			return nil, fmt.Errorf("expr: unexpected end of formula at pos %d", tok.pos)
		}
		return nil, fmt.Errorf("expr: unexpected token %q at pos %d", tok.text, tok.pos)
	}
}

// parsePredCall handles name(arg) for arity-1 predicates.
func (p *parser) parsePredCall(name string, preds map[string]KnownPred, namePos int) (*node, error) {
	p.consume() // consume '('
	kp, ok := preds[name]
	if !ok {
		return nil, fmt.Errorf("expr: undefined predicate %q", name)
	}
	if kp.Arity != 1 {
		return nil, fmt.Errorf("expr: predicate %q has arity %d but is called with 1 argument", name, kp.Arity)
	}
	argTok := p.peek()
	if argTok.kind != tokIdent {
		return nil, fmt.Errorf("expr: expected identifier argument for predicate %q at pos %d", name, argTok.pos)
	}
	p.consume()
	if p.peek().kind != tokRParen {
		return nil, fmt.Errorf("expr: expected ')' after argument of predicate %q at pos %d", name, p.peek().pos)
	}
	p.consume()
	return &node{kind: nPred, predName: name, predArg: core.Tag(argTok.text)}, nil
}

// classifyBare classifies a bare (non-call) identifier per the OQ-A case rule.
//
//   - dotted or colon token (terrain.depth, tool:cutting.quality) → Attr
//   - known arity-0 predicate (isOwner) → Pred
//   - uppercase-initial → Stat (validated; undefined → LOAD failure)
//   - lowercase-initial → Attr (caller namespace, not validated)
func classifyBare(name string, pos int, ks StatSet, preds map[string]KnownPred) (*node, error) {
	if strings.ContainsAny(name, ".:") {
		return &node{kind: nAttr, attrName: core.Tag(name)}, nil
	}
	if kp, ok := preds[name]; ok && kp.Arity == 0 {
		return &node{kind: nPred, predName: name, predArg: ""}, nil
	}
	r, _ := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(r) {
		if !ks.Has(core.StatID(name)) {
			return nil, fmt.Errorf("expr: undefined stat %q", name)
		}
		return &node{kind: nStat, statID: core.StatID(name)}, nil
	}
	// lowercase-initial → Attr (unvalidated caller namespace)
	return &node{kind: nAttr, attrName: core.Tag(name)}, nil
}
