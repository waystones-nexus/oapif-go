package cql2

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/waystones/oapif-go/internal/config"
)

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type tokenKind int

const (
	tokEOF    tokenKind = iota
	tokIdent             // unquoted identifier or keyword
	tokQIdent            // double-quoted identifier "name"
	tokString            // single-quoted 'value'
	tokNumber
	tokLParen
	tokRParen
	tokComma
	tokOp // = <> < > <= >=
)

type token struct {
	kind  tokenKind
	value string
}

func tokenize(input string) ([]token, error) {
	var tokens []token
	i := 0
	runes := []rune(input)
	n := len(runes)

	for i < n {
		// Skip whitespace
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}

		ch := runes[i]

		// Single-quoted string
		if ch == '\'' {
			i++
			var sb strings.Builder
			for i < n {
				if runes[i] == '\'' {
					// Check for escaped quote ''
					if i+1 < n && runes[i+1] == '\'' {
						sb.WriteRune('\'')
						i += 2
					} else {
						i++
						break
					}
				} else {
					sb.WriteRune(runes[i])
					i++
				}
			}
			tokens = append(tokens, token{kind: tokString, value: sb.String()})
			continue
		}

		// Double-quoted identifier
		if ch == '"' {
			i++
			var sb strings.Builder
			for i < n && runes[i] != '"' {
				sb.WriteRune(runes[i])
				i++
			}
			if i < n {
				i++ // consume closing "
			}
			tokens = append(tokens, token{kind: tokQIdent, value: sb.String()})
			continue
		}

		// Numbers
		if unicode.IsDigit(ch) || (ch == '-' && i+1 < n && unicode.IsDigit(runes[i+1])) {
			j := i
			if runes[j] == '-' {
				j++
			}
			for j < n && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			tokens = append(tokens, token{kind: tokNumber, value: string(runes[i:j])})
			i = j
			continue
		}

		// Two-char operators
		if i+1 < n {
			two := string(runes[i : i+2])
			if two == "<=" || two == ">=" || two == "<>" {
				tokens = append(tokens, token{kind: tokOp, value: two})
				i += 2
				continue
			}
		}

		// Single-char operators / punctuation
		switch ch {
		case '<', '>', '=':
			tokens = append(tokens, token{kind: tokOp, value: string(ch)})
			i++
			continue
		case '(':
			tokens = append(tokens, token{kind: tokLParen, value: "("})
			i++
			continue
		case ')':
			tokens = append(tokens, token{kind: tokRParen, value: ")"})
			i++
			continue
		case ',':
			tokens = append(tokens, token{kind: tokComma, value: ","})
			i++
			continue
		}

		// Identifiers / keywords
		if unicode.IsLetter(ch) || ch == '_' {
			j := i
			for j < n && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			tokens = append(tokens, token{kind: tokIdent, value: string(runes[i:j])})
			i = j
			continue
		}

		return nil, fmt.Errorf("unexpected character %q at position %d", ch, i)
	}

	tokens = append(tokens, token{kind: tokEOF})
	return tokens, nil
}

// ---------------------------------------------------------------------------
// AST
// ---------------------------------------------------------------------------

type Expr interface{ exprNode() }

type LogicalExpr struct {
	Op          string // "AND" | "OR"
	Left, Right Expr
}

type NotExpr struct{ Expr Expr }

type CompareExpr struct {
	Prop string
	Op   string
	Val  Literal
}

type LikeExpr struct {
	Prop    string
	Pattern string
	Not     bool
}

type IsNullExpr struct {
	Prop string
	Not  bool
}

type InExpr struct {
	Prop string
	Vals []Literal
	Not  bool
}

type BetweenExpr struct {
	Prop string
	Low  Literal
	High Literal
	Not  bool
}

type Literal struct {
	Kind string // "string" | "number" | "bool" | "null"
	Str  string
	Num  float64
	Bool bool
}

func (*LogicalExpr) exprNode() {}
func (*NotExpr) exprNode()     {}
func (*CompareExpr) exprNode() {}
func (*LikeExpr) exprNode()    {}
func (*IsNullExpr) exprNode()  {}
func (*InExpr) exprNode()      {}
func (*BetweenExpr) exprNode() {}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) next() token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) expectIdent(word string) error {
	t := p.next()
	if t.kind != tokIdent || !strings.EqualFold(t.value, word) {
		return fmt.Errorf("expected %q, got %q", word, t.value)
	}
	return nil
}

func isKeyword(t token, word string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.value, word)
}

// Parse tokenizes the input and returns the root expression.
func Parse(input string) (Expr, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q after expression", p.peek().value)
	}
	return expr, nil
}

func (p *parser) parseExpr() (Expr, error) {
	return p.parseAnd()
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	for isKeyword(p.peek(), "AND") {
		p.next()
		right, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		left = &LogicalExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for isKeyword(p.peek(), "OR") {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &LogicalExpr{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if isKeyword(p.peek(), "NOT") {
		p.next()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &NotExpr{Expr: inner}, nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (Expr, error) {
	if p.peek().kind == tokLParen {
		p.next()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, fmt.Errorf("expected ')', got %q", p.peek().value)
		}
		p.next()
		return expr, nil
	}
	return p.parsePredicate()
}

func (p *parser) parsePredicate() (Expr, error) {
	t := p.next()
	if t.kind != tokIdent && t.kind != tokQIdent {
		return nil, fmt.Errorf("expected property name, got %q", t.value)
	}
	return p.parseCompTail(t.value)
}

func (p *parser) parseCompTail(prop string) (Expr, error) {
	pk := p.peek()

	// Comparison operator
	if pk.kind == tokOp {
		op := p.next().value
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		return &CompareExpr{Prop: prop, Op: op, Val: lit}, nil
	}

	upper := strings.ToUpper(pk.value)

	if pk.kind == tokIdent && upper == "LIKE" {
		p.next()
		pat, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return &LikeExpr{Prop: prop, Pattern: pat, Not: false}, nil
	}

	if pk.kind == tokIdent && upper == "IS" {
		p.next()
		if isKeyword(p.peek(), "NOT") {
			p.next()
			if err := p.expectIdent("NULL"); err != nil {
				return nil, err
			}
			return &IsNullExpr{Prop: prop, Not: true}, nil
		}
		if err := p.expectIdent("NULL"); err != nil {
			return nil, err
		}
		return &IsNullExpr{Prop: prop, Not: false}, nil
	}

	if pk.kind == tokIdent && upper == "IN" {
		p.next()
		vals, err := p.parseInList()
		if err != nil {
			return nil, err
		}
		return &InExpr{Prop: prop, Vals: vals, Not: false}, nil
	}

	if pk.kind == tokIdent && upper == "BETWEEN" {
		p.next()
		low, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		if err := p.expectIdent("AND"); err != nil {
			return nil, err
		}
		high, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		return &BetweenExpr{Prop: prop, Low: low, High: high, Not: false}, nil
	}

	if pk.kind == tokIdent && upper == "NOT" {
		p.next()
		next := strings.ToUpper(p.peek().value)
		switch next {
		case "LIKE":
			p.next()
			pat, err := p.parseString()
			if err != nil {
				return nil, err
			}
			return &LikeExpr{Prop: prop, Pattern: pat, Not: true}, nil
		case "IN":
			p.next()
			vals, err := p.parseInList()
			if err != nil {
				return nil, err
			}
			return &InExpr{Prop: prop, Vals: vals, Not: true}, nil
		case "BETWEEN":
			p.next()
			low, err := p.parseLiteral()
			if err != nil {
				return nil, err
			}
			if err := p.expectIdent("AND"); err != nil {
				return nil, err
			}
			high, err := p.parseLiteral()
			if err != nil {
				return nil, err
			}
			return &BetweenExpr{Prop: prop, Low: low, High: high, Not: true}, nil
		default:
			return nil, fmt.Errorf("unexpected NOT modifier before %q", p.peek().value)
		}
	}

	return nil, fmt.Errorf("expected operator or predicate keyword after property %q, got %q", prop, pk.value)
}

func (p *parser) parseInList() ([]Literal, error) {
	if p.peek().kind != tokLParen {
		return nil, fmt.Errorf("expected '(' for IN list")
	}
	p.next()

	var vals []Literal
	for p.peek().kind != tokRParen && p.peek().kind != tokEOF {
		lit, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		vals = append(vals, lit)
		if p.peek().kind == tokComma {
			p.next()
		} else {
			break
		}
	}
	if p.peek().kind != tokRParen {
		return nil, fmt.Errorf("expected ')' to close IN list")
	}
	p.next()
	return vals, nil
}

func (p *parser) parseString() (string, error) {
	t := p.next()
	if t.kind != tokString {
		return "", fmt.Errorf("expected string literal, got %q", t.value)
	}
	return t.value, nil
}

func (p *parser) parseLiteral() (Literal, error) {
	t := p.peek()
	switch t.kind {
	case tokString:
		p.next()
		return Literal{Kind: "string", Str: t.value}, nil
	case tokNumber:
		p.next()
		f, err := strconv.ParseFloat(t.value, 64)
		if err != nil {
			return Literal{}, fmt.Errorf("invalid number %q", t.value)
		}
		return Literal{Kind: "number", Num: f}, nil
	case tokIdent:
		upper := strings.ToUpper(t.value)
		switch upper {
		case "TRUE":
			p.next()
			return Literal{Kind: "bool", Bool: true}, nil
		case "FALSE":
			p.next()
			return Literal{Kind: "bool", Bool: false}, nil
		case "NULL":
			p.next()
			return Literal{Kind: "null"}, nil
		}
	}
	return Literal{}, fmt.Errorf("expected literal value, got %q", t.value)
}

// ---------------------------------------------------------------------------
// Translation
// ---------------------------------------------------------------------------

// Translate converts a parsed CQL2 expression tree into a parameterized SQL
// WHERE fragment and its argument list.
func Translate(expr Expr, queryables map[string]config.QueryableField) (string, []interface{}, error) {
	var args []interface{}
	sql, err := translateExpr(expr, queryables, &args)
	if err != nil {
		return "", nil, err
	}
	return sql, args, nil
}

func translateExpr(expr Expr, queryables map[string]config.QueryableField, args *[]interface{}) (string, error) {
	switch e := expr.(type) {
	case *LogicalExpr:
		left, err := translateExpr(e.Left, queryables, args)
		if err != nil {
			return "", err
		}
		right, err := translateExpr(e.Right, queryables, args)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + e.Op + " " + right + ")", nil

	case *NotExpr:
		inner, err := translateExpr(e.Expr, queryables, args)
		if err != nil {
			return "", err
		}
		return "NOT (" + inner + ")", nil

	case *CompareExpr:
		field, ok := queryables[e.Prop]
		if !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		arg, err := coerceArg(e.Val, field)
		if err != nil {
			return "", fmt.Errorf("property %q: %w", e.Prop, err)
		}
		*args = append(*args, arg)
		return fmt.Sprintf(`"%s" %s ?`, e.Prop, e.Op), nil

	case *LikeExpr:
		if _, ok := queryables[e.Prop]; !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		*args = append(*args, e.Pattern)
		if e.Not {
			return fmt.Sprintf(`"%s" NOT LIKE ?`, e.Prop), nil
		}
		return fmt.Sprintf(`"%s" LIKE ?`, e.Prop), nil

	case *IsNullExpr:
		if _, ok := queryables[e.Prop]; !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		if e.Not {
			return fmt.Sprintf(`"%s" IS NOT NULL`, e.Prop), nil
		}
		return fmt.Sprintf(`"%s" IS NULL`, e.Prop), nil

	case *InExpr:
		field, ok := queryables[e.Prop]
		if !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		placeholders := make([]string, len(e.Vals))
		for i, lit := range e.Vals {
			arg, err := coerceArg(lit, field)
			if err != nil {
				return "", fmt.Errorf("property %q IN list[%d]: %w", e.Prop, i, err)
			}
			*args = append(*args, arg)
			placeholders[i] = "?"
		}
		if e.Not {
			return fmt.Sprintf(`"%s" NOT IN (%s)`, e.Prop, strings.Join(placeholders, ",")), nil
		}
		return fmt.Sprintf(`"%s" IN (%s)`, e.Prop, strings.Join(placeholders, ",")), nil

	case *BetweenExpr:
		field, ok := queryables[e.Prop]
		if !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		lowArg, err := coerceArg(e.Low, field)
		if err != nil {
			return "", fmt.Errorf("property %q BETWEEN low: %w", e.Prop, err)
		}
		highArg, err := coerceArg(e.High, field)
		if err != nil {
			return "", fmt.Errorf("property %q BETWEEN high: %w", e.Prop, err)
		}
		*args = append(*args, lowArg, highArg)
		if e.Not {
			return fmt.Sprintf(`"%s" NOT BETWEEN ? AND ?`, e.Prop), nil
		}
		return fmt.Sprintf(`"%s" BETWEEN ? AND ?`, e.Prop), nil

	default:
		return "", fmt.Errorf("unsupported expression type %T", expr)
	}
}

func coerceArg(lit Literal, field config.QueryableField) (interface{}, error) {
	switch field.Type {
	case "integer":
		if lit.Kind == "number" {
			return int64(lit.Num), nil
		}
		return nil, fmt.Errorf("expected integer")
	case "number":
		if lit.Kind == "number" {
			return lit.Num, nil
		}
		return nil, fmt.Errorf("expected number")
	case "boolean":
		if lit.Kind == "bool" {
			return lit.Bool, nil
		}
		return nil, fmt.Errorf("expected TRUE or FALSE")
	default: // "string" and date-time
		if lit.Kind == "null" {
			return nil, nil
		}
		return lit.Str, nil
	}
}
