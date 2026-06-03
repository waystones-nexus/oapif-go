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

		// Numbers (including negative numbers)
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

// SpatialExpr represents a spatial predicate: S_INTERSECTS(prop, geom).
// GeomSQL is a DuckDB SQL fragment for the geometry literal (e.g. ST_GeomFromText('...') or ST_MakeEnvelope(...)).
type SpatialExpr struct {
	Op      string // "S_INTERSECTS", "S_WITHIN", "S_CONTAINS", "S_DISJOINT", "S_OVERLAPS", "S_TOUCHES", "S_CROSSES", "S_EQUALS"
	Prop    string // geometry property / column reference
	GeomSQL string // DuckDB SQL for the literal geometry
}

// TemporalExpr represents a temporal predicate: T_AFTER(prop, TIMESTAMP('...')).
type TemporalExpr struct {
	Op   string // "T_AFTER", "T_BEFORE", "T_DURING", "T_EQUALS"
	Prop string // datetime property reference
	Low  string // ISO 8601 timestamp string
	High string // ISO 8601 timestamp string (T_DURING only)
}

type Literal struct {
	Kind string // "string" | "number" | "bool" | "null"
	Str  string
	Num  float64
	Bool bool
}

func (*LogicalExpr) exprNode()  {}
func (*NotExpr) exprNode()      {}
func (*CompareExpr) exprNode()  {}
func (*LikeExpr) exprNode()     {}
func (*IsNullExpr) exprNode()   {}
func (*InExpr) exprNode()       {}
func (*BetweenExpr) exprNode()  {}
func (*SpatialExpr) exprNode()  {}
func (*TemporalExpr) exprNode() {}

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

func (p *parser) expectKind(kind tokenKind, name string) error {
	t := p.next()
	if t.kind != kind {
		return fmt.Errorf("expected %s, got %q", name, t.value)
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

// nextTokenIs returns true if the token at pos+offset is of the given kind.
func (p *parser) tokenAt(offset int) token {
	idx := p.pos + offset
	if idx < len(p.tokens) {
		return p.tokens[idx]
	}
	return token{kind: tokEOF}
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

	// Spatial / temporal function calls: KEYWORD followed by '('
	if p.peek().kind == tokIdent && p.tokenAt(1).kind == tokLParen {
		upper := strings.ToUpper(p.peek().value)
		switch upper {
		case "S_INTERSECTS", "S_WITHIN", "S_CONTAINS", "S_DISJOINT",
			"S_OVERLAPS", "S_TOUCHES", "S_CROSSES", "S_EQUALS":
			return p.parseSpatialExpr()
		case "T_AFTER", "T_BEFORE", "T_DURING", "T_EQUALS":
			return p.parseTemporalExpr()
		}
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
// Spatial predicate parsing
// ---------------------------------------------------------------------------

// parseSpatialExpr parses: S_INTERSECTS(prop, geom_literal)
func (p *parser) parseSpatialExpr() (Expr, error) {
	op := strings.ToUpper(p.next().value) // consume function name
	if err := p.expectKind(tokLParen, "'('"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	// First arg: property / column reference
	propTok := p.next()
	if propTok.kind != tokIdent && propTok.kind != tokQIdent {
		return nil, fmt.Errorf("%s arg1: expected property name, got %q", op, propTok.value)
	}
	prop := propTok.value

	if err := p.expectKind(tokComma, "','"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	// Second arg: geometry literal
	geomSQL, err := p.parseGeomLiteral()
	if err != nil {
		return nil, fmt.Errorf("%s arg2: %w", op, err)
	}

	if err := p.expectKind(tokRParen, "')'"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return &SpatialExpr{Op: op, Prop: prop, GeomSQL: geomSQL}, nil
}

// parseGeomLiteral parses a geometry literal: BBOX(...) or WKT geometry.
// Returns a DuckDB SQL fragment.
func (p *parser) parseGeomLiteral() (string, error) {
	t := p.next()
	if t.kind != tokIdent {
		return "", fmt.Errorf("expected geometry type or BBOX, got %q", t.value)
	}
	upper := strings.ToUpper(t.value)

	switch upper {
	case "BBOX":
		return p.parseBboxGeom()
	case "POINT", "LINESTRING", "POLYGON",
		"MULTIPOINT", "MULTILINESTRING", "MULTIPOLYGON",
		"GEOMETRYCOLLECTION":
		return p.parseWKTGeom(upper)
	default:
		return "", fmt.Errorf("unknown geometry type %q", t.value)
	}
}

// parseBboxGeom parses BBOX(minx, miny, maxx, maxy) and returns ST_MakeEnvelope(...).
func (p *parser) parseBboxGeom() (string, error) {
	if err := p.expectKind(tokLParen, "'('"); err != nil {
		return "", fmt.Errorf("BBOX: %w", err)
	}
	nums := make([]string, 4)
	for i := 0; i < 4; i++ {
		if p.peek().kind != tokNumber {
			return "", fmt.Errorf("BBOX coordinate %d: expected number, got %q", i+1, p.peek().value)
		}
		nums[i] = p.next().value
		if i < 3 {
			if err := p.expectKind(tokComma, "','"); err != nil {
				return "", fmt.Errorf("BBOX: %w", err)
			}
		}
	}
	if err := p.expectKind(tokRParen, "')'"); err != nil {
		return "", fmt.Errorf("BBOX: %w", err)
	}
	return fmt.Sprintf("ST_MakeEnvelope(%s, %s, %s, %s)", nums[0], nums[1], nums[2], nums[3]), nil
}

// parseWKTGeom parses a WKT geometry body (e.g. POINT(1 2)) and returns ST_GeomFromText('...').
func (p *parser) parseWKTGeom(typeName string) (string, error) {
	if p.peek().kind != tokLParen {
		return "", fmt.Errorf("expected '(' after %s", typeName)
	}

	var sb strings.Builder
	sb.WriteString(typeName)
	depth := 0
	prevWasNum := false

	for {
		t := p.peek()
		if t.kind == tokEOF {
			return "", fmt.Errorf("unexpected end of input in %s geometry", typeName)
		}
		p.next()
		switch t.kind {
		case tokLParen:
			depth++
			sb.WriteString("(")
			prevWasNum = false
		case tokRParen:
			depth--
			sb.WriteString(")")
			prevWasNum = false
			if depth == 0 {
				wkt := sb.String()
				escaped := strings.ReplaceAll(wkt, "'", "''")
				return fmt.Sprintf("ST_GeomFromText('%s')", escaped), nil
			}
		case tokComma:
			sb.WriteString(",")
			prevWasNum = false
		case tokNumber:
			if prevWasNum {
				sb.WriteString(" ")
			}
			sb.WriteString(t.value)
			prevWasNum = true
		default:
			return "", fmt.Errorf("unexpected token %q in %s geometry", t.value, typeName)
		}
	}
}

// ---------------------------------------------------------------------------
// Temporal predicate parsing
// ---------------------------------------------------------------------------

// parseTemporalExpr parses: T_AFTER(prop, TIMESTAMP('...')) or T_DURING(prop, INTERVAL('...','...'))
func (p *parser) parseTemporalExpr() (Expr, error) {
	op := strings.ToUpper(p.next().value) // consume T_AFTER etc.
	if err := p.expectKind(tokLParen, "'('"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	propTok := p.next()
	if propTok.kind != tokIdent && propTok.kind != tokQIdent {
		return nil, fmt.Errorf("%s arg1: expected property name, got %q", op, propTok.value)
	}
	prop := propTok.value

	if err := p.expectKind(tokComma, "','"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	// Second arg: TIMESTAMP or INTERVAL
	kwTok := p.next()
	if kwTok.kind != tokIdent {
		return nil, fmt.Errorf("%s arg2: expected TIMESTAMP or INTERVAL, got %q", op, kwTok.value)
	}

	var e *TemporalExpr
	switch strings.ToUpper(kwTok.value) {
	case "TIMESTAMP":
		ts, err := p.parseTimestampArg()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		e = &TemporalExpr{Op: op, Prop: prop, Low: ts}
	case "INTERVAL":
		if op != "T_DURING" {
			return nil, fmt.Errorf("INTERVAL literal is only valid for T_DURING, not %s", op)
		}
		low, high, err := p.parseIntervalArgs()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		e = &TemporalExpr{Op: op, Prop: prop, Low: low, High: high}
	default:
		return nil, fmt.Errorf("%s arg2: expected TIMESTAMP or INTERVAL, got %q", op, kwTok.value)
	}

	if err := p.expectKind(tokRParen, "')'"); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return e, nil
}

func (p *parser) parseTimestampArg() (string, error) {
	if err := p.expectKind(tokLParen, "'('"); err != nil {
		return "", fmt.Errorf("TIMESTAMP: %w", err)
	}
	if p.peek().kind != tokString {
		return "", fmt.Errorf("TIMESTAMP: expected string, got %q", p.peek().value)
	}
	ts := p.next().value
	if err := p.expectKind(tokRParen, "')'"); err != nil {
		return "", fmt.Errorf("TIMESTAMP: %w", err)
	}
	return ts, nil
}

func (p *parser) parseIntervalArgs() (string, string, error) {
	if err := p.expectKind(tokLParen, "'('"); err != nil {
		return "", "", fmt.Errorf("INTERVAL: %w", err)
	}
	if p.peek().kind != tokString {
		return "", "", fmt.Errorf("INTERVAL start: expected string, got %q", p.peek().value)
	}
	low := p.next().value
	if err := p.expectKind(tokComma, "','"); err != nil {
		return "", "", fmt.Errorf("INTERVAL: %w", err)
	}
	if p.peek().kind != tokString {
		return "", "", fmt.Errorf("INTERVAL end: expected string, got %q", p.peek().value)
	}
	high := p.next().value
	if err := p.expectKind(tokRParen, "')'"); err != nil {
		return "", "", fmt.Errorf("INTERVAL: %w", err)
	}
	return low, high, nil
}

// ---------------------------------------------------------------------------
// Translation
// ---------------------------------------------------------------------------

// TranslateContext provides collection metadata needed to generate correct SQL.
type TranslateContext struct {
	Queryables     map[string]config.QueryableField
	GeomColumn     string
	GeomIsNative   bool
	DatetimeColumn string
}

// Translate converts a parsed CQL2 expression tree into a parameterized SQL
// WHERE fragment and its argument list.
func Translate(expr Expr, ctx TranslateContext) (string, []interface{}, error) {
	var args []interface{}
	sql, err := translateExpr(expr, ctx, &args)
	if err != nil {
		return "", nil, err
	}
	return sql, args, nil
}

func translateExpr(expr Expr, ctx TranslateContext, args *[]interface{}) (string, error) {
	switch e := expr.(type) {
	case *LogicalExpr:
		left, err := translateExpr(e.Left, ctx, args)
		if err != nil {
			return "", err
		}
		right, err := translateExpr(e.Right, ctx, args)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + e.Op + " " + right + ")", nil

	case *NotExpr:
		inner, err := translateExpr(e.Expr, ctx, args)
		if err != nil {
			return "", err
		}
		return "NOT (" + inner + ")", nil

	case *CompareExpr:
		field, ok := ctx.Queryables[e.Prop]
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
		if _, ok := ctx.Queryables[e.Prop]; !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		*args = append(*args, e.Pattern)
		if e.Not {
			return fmt.Sprintf(`"%s" NOT LIKE ?`, e.Prop), nil
		}
		return fmt.Sprintf(`"%s" LIKE ?`, e.Prop), nil

	case *IsNullExpr:
		if _, ok := ctx.Queryables[e.Prop]; !ok {
			return "", fmt.Errorf("unknown queryable property %q", e.Prop)
		}
		if e.Not {
			return fmt.Sprintf(`"%s" IS NOT NULL`, e.Prop), nil
		}
		return fmt.Sprintf(`"%s" IS NULL`, e.Prop), nil

	case *InExpr:
		field, ok := ctx.Queryables[e.Prop]
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
		field, ok := ctx.Queryables[e.Prop]
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

	case *SpatialExpr:
		// Build column expression, wrapping WKB blobs if necessary.
		colExpr := fmt.Sprintf(`"%s"`, e.Prop)
		if e.Prop == ctx.GeomColumn && !ctx.GeomIsNative {
			colExpr = fmt.Sprintf(`ST_GeomFromWKB("%s")`, e.Prop)
		}
		duckFn := spatialOpToSQL(e.Op)
		return fmt.Sprintf("%s(%s, %s)", duckFn, colExpr, e.GeomSQL), nil

	case *TemporalExpr:
		if ctx.DatetimeColumn == "" {
			return "", fmt.Errorf("temporal predicate requires a datetime column, but collection has none")
		}
		col := fmt.Sprintf(`"%s"`, e.Prop)
		switch e.Op {
		case "T_AFTER":
			*args = append(*args, e.Low)
			return fmt.Sprintf(`%s > ?`, col), nil
		case "T_BEFORE":
			*args = append(*args, e.Low)
			return fmt.Sprintf(`%s < ?`, col), nil
		case "T_EQUALS":
			*args = append(*args, e.Low)
			return fmt.Sprintf(`%s = ?`, col), nil
		case "T_DURING":
			*args = append(*args, e.Low, e.High)
			return fmt.Sprintf(`(%s >= ? AND %s <= ?)`, col, col), nil
		default:
			return "", fmt.Errorf("unsupported temporal op %q", e.Op)
		}

	default:
		return "", fmt.Errorf("unsupported expression type %T", expr)
	}
}

func spatialOpToSQL(op string) string {
	switch op {
	case "S_INTERSECTS":
		return "ST_Intersects"
	case "S_WITHIN":
		return "ST_Within"
	case "S_CONTAINS":
		return "ST_Contains"
	case "S_DISJOINT":
		return "ST_Disjoint"
	case "S_OVERLAPS":
		return "ST_Overlaps"
	case "S_TOUCHES":
		return "ST_Touches"
	case "S_CROSSES":
		return "ST_Crosses"
	case "S_EQUALS":
		return "ST_Equals"
	default:
		return "ST_Intersects"
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
