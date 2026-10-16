package upgradecheck

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A reader of the Python an Odoo database keeps in its records: domains, contexts, view modifiers,
// QWeb expressions and server-action code. It is neither an interpreter nor a full Python parser. It
// builds the trees an upgrade check walks (names, attributes, subscripts, calls, literals,
// comparisons, lambdas) and, for code, every statement's expressions and the targets of its for
// loops. Text it cannot read is an error, which the check reports as unparsable rather than guess.

var errSyntax = errors.New("python: cannot read this")

type nodeKind int

const (
	nName nodeKind = iota
	nConst
	nAttr
	nSubscript
	nCall
	nList
	nTuple
	nSet
	nDict
	nCompare
	nLambda
	// nOther is any other expression (an operator, a conditional, a comprehension, a slice, a
	// starred or formatted value): only its operands are kept, to be walked.
	nOther
)

// bytesValue is a bytes literal: a constant, never a field name.
type bytesValue string

type node struct {
	kind  nodeKind
	ident string      // nName: the name; nAttr: the attribute
	value interface{} // nConst: string, bytesValue, int64, float64, bool or nil
	left  *node       // nAttr, nSubscript: the value; nCall: the function; nCompare: the left operand; nLambda: the body
	index *node       // nSubscript
	// items: nList, nTuple, nSet: the elements; nCall: the positional arguments; nCompare: the
	// comparators; nOther: the operands.
	items    []*node
	keys     []*node // nDict; nil for a **mapping
	values   []*node // nDict
	keywords []keyword
	ops      []string // nCompare
	params   []string // nLambda
}

type keyword struct {
	name  string // "" for **kwargs
	value *node
}

// walk visits n and every expression inside it.
func walk(n *node, visit func(*node)) {
	if n == nil {
		return
	}
	visit(n)
	walk(n.left, visit)
	walk(n.index, visit)
	for _, item := range n.items {
		walk(item, visit)
	}
	for _, key := range n.keys {
		walk(key, visit)
	}
	for _, value := range n.values {
		walk(value, visit)
	}
	for _, kw := range n.keywords {
		walk(kw.value, visit)
	}
}

// --- tokens -------------------------------------------------------------------------------------

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokName
	tokNumber
	tokString
	tokOp
	tokNewline
)

type token struct {
	kind  tokenKind
	text  string // a name, a number, an operator
	value string // a string literal's value
	bytes bool
	fmt   bool
}

var operators = []string{
	"**=", "//=", ">>=", "<<=", "...", "->", ":=", "**", "//", "<<", ">>", "<=", ">=", "==", "!=",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "@=",
	"+", "-", "*", "/", "%", "@", "&", "|", "^", "~", "<", ">", "(", ")", "[", "]", "{", "}", ",", ":", ".", ";", "=", "!",
}

func isNameStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isNamePart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// tokenize splits source into tokens. A line break outside brackets ends a logical line.
func tokenize(source string) ([]token, error) {
	var out []token
	depth := 0
	newline := func() {
		if depth == 0 && len(out) > 0 && out[len(out)-1].kind != tokNewline {
			out = append(out, token{kind: tokNewline})
		}
	}
	for i := 0; i < len(source); {
		c := source[i]
		switch {
		case c == '\\' && i+1 < len(source) && (source[i+1] == '\n' || source[i+1] == '\r'):
			i += 2
			if source[i-1] == '\r' && i < len(source) && source[i] == '\n' {
				i++
			}
			continue
		case c == '#':
			for i < len(source) && source[i] != '\n' && source[i] != '\r' {
				i++
			}
			continue
		case c == '\n' || c == '\r':
			newline()
			i++
			continue
		case c == ' ' || c == '\t' || c == '\f' || c == '\v':
			i++
			continue
		case c == '"' || c == '\'':
			tok, next, err := readString(source, i, "")
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
			i = next
			continue
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(source) && source[i+1] >= '0' && source[i+1] <= '9':
			start := i
			for i < len(source) {
				d := source[i]
				if d >= '0' && d <= '9' || d >= 'a' && d <= 'z' || d >= 'A' && d <= 'Z' || d == '_' || d == '.' {
					if (d == 'e' || d == 'E') && i+1 < len(source) && (source[i+1] == '+' || source[i+1] == '-') &&
						!strings.HasPrefix(strings.ToLower(source[start:]), "0x") {
						i += 2
						continue
					}
					i++
					continue
				}
				break
			}
			out = append(out, token{kind: tokNumber, text: source[start:i]})
			continue
		}
		r, size := utf8.DecodeRuneInString(source[i:])
		if isNameStart(r) {
			start := i
			i += size
			for i < len(source) {
				r, size = utf8.DecodeRuneInString(source[i:])
				if !isNamePart(r) {
					break
				}
				i += size
			}
			name := source[start:i]
			if i < len(source) && (source[i] == '"' || source[i] == '\'') && isStringPrefix(name) {
				tok, next, err := readString(source, i, strings.ToLower(name))
				if err != nil {
					return nil, err
				}
				out = append(out, tok)
				i = next
				continue
			}
			out = append(out, token{kind: tokName, text: name})
			continue
		}
		matched := ""
		for _, op := range operators {
			if strings.HasPrefix(source[i:], op) {
				matched = op
				break
			}
		}
		if matched == "" {
			return nil, errSyntax
		}
		switch matched {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			if depth == 0 {
				return nil, errSyntax
			}
			depth--
		}
		out = append(out, token{kind: tokOp, text: matched})
		i += len(matched)
	}
	if depth != 0 {
		return nil, errSyntax
	}
	return out, nil
}

func isStringPrefix(name string) bool {
	switch strings.ToLower(name) {
	case "r", "u", "b", "f", "br", "rb", "fr", "rf":
		return true
	}
	return false
}

// readString reads the string literal whose quote is at source[start].
func readString(source string, start int, prefix string) (token, int, error) {
	raw := strings.Contains(prefix, "r")
	quote := source[start : start+1]
	if strings.HasPrefix(source[start:], strings.Repeat(quote, 3)) {
		quote = strings.Repeat(quote, 3)
	}
	var value strings.Builder
	i := start + len(quote)
	for {
		if i >= len(source) {
			return token{}, 0, errSyntax
		}
		if strings.HasPrefix(source[i:], quote) {
			i += len(quote)
			break
		}
		c := source[i]
		if len(quote) == 1 && (c == '\n' || c == '\r') {
			return token{}, 0, errSyntax
		}
		if c == '\\' && i+1 < len(source) {
			if raw {
				value.WriteByte(c)
				value.WriteByte(source[i+1])
				i += 2
				continue
			}
			decoded, width := unescape(source[i:])
			value.WriteString(decoded)
			i += width
			continue
		}
		value.WriteByte(c)
		i++
	}
	return token{kind: tokString, value: value.String(), bytes: strings.Contains(prefix, "b"), fmt: strings.Contains(prefix, "f")}, i, nil
}

// unescape decodes the escape sequence at the start of text (text[0] is the backslash).
func unescape(text string) (string, int) {
	switch c := text[1]; c {
	case '\n':
		return "", 2
	case '\r':
		if len(text) > 2 && text[2] == '\n' {
			return "", 3
		}
		return "", 2
	case '\\', '\'', '"':
		return string(c), 2
	case 'n':
		return "\n", 2
	case 't':
		return "\t", 2
	case 'r':
		return "\r", 2
	case 'a':
		return "\a", 2
	case 'b':
		return "\b", 2
	case 'f':
		return "\f", 2
	case 'v':
		return "\v", 2
	case 'x', 'u', 'U':
		width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
		if len(text) >= 2+width {
			if code, err := strconv.ParseUint(text[2:2+width], 16, 32); err == nil {
				return string(rune(code)), 2 + width
			}
		}
	case '0', '1', '2', '3', '4', '5', '6', '7':
		end := 2
		for end < len(text) && end < 4 && text[end] >= '0' && text[end] <= '7' {
			end++
		}
		if code, err := strconv.ParseUint(text[1:end], 8, 32); err == nil {
			return string(rune(code)), end
		}
	}
	return text[:2], 2
}

// --- expressions --------------------------------------------------------------------------------

var reserved = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "is": true, "if": true, "else": true, "elif": true,
	"for": true, "while": true, "lambda": true, "def": true, "class": true, "return": true, "yield": true,
	"import": true, "from": true, "as": true, "with": true, "try": true, "except": true, "finally": true,
	"raise": true, "del": true, "pass": true, "break": true, "continue": true, "global": true,
	"nonlocal": true, "assert": true, "async": true, "await": true,
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{kind: tokEOF}
}

func (p *parser) peekAt(offset int) token {
	if p.pos+offset < len(p.tokens) {
		return p.tokens[p.pos+offset]
	}
	return token{kind: tokEOF}
}

func (p *parser) next() token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) atEnd() bool { k := p.peek().kind; return k == tokEOF || k == tokNewline }

func (p *parser) isOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

func (p *parser) isKeyword(word string) bool {
	t := p.peek()
	return t.kind == tokName && t.text == word
}

func (p *parser) expectOp(op string) error {
	if !p.isOp(op) {
		return errSyntax
	}
	p.next()
	return nil
}

func (p *parser) expectKeyword(word string) error {
	if !p.isKeyword(word) {
		return errSyntax
	}
	p.next()
	return nil
}

// parseExpression reads text as one Python expression (Python's eval mode).
func parseExpression(text string) (*node, error) {
	tokens, err := tokenize(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].kind == tokNewline {
		tokens = tokens[:len(tokens)-1]
	}
	p := &parser{tokens: tokens}
	n, err := p.testList()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, errSyntax
	}
	return n, nil
}

// testList is one expression, or several separated by commas (a tuple).
func (p *parser) testList() (*node, error) {
	return p.sequence(p.starOrTest)
}

func (p *parser) sequence(element func() (*node, error)) (*node, error) {
	first, err := element()
	if err != nil {
		return nil, err
	}
	if !p.isOp(",") {
		return first, nil
	}
	items := []*node{first}
	for p.isOp(",") {
		p.next()
		if p.atEnd() || p.isOp("=") || p.isOp(")") || p.isOp(":") || p.isOp(";") || p.isKeyword("in") || isAugmented(p.peek()) {
			break
		}
		item, err := element()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &node{kind: nTuple, items: items}, nil
}

func (p *parser) starOrTest() (*node, error) {
	if p.isOp("*") {
		p.next()
		value, err := p.bitOr()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{value}}, nil
	}
	return p.namedTest()
}

// namedTest is an expression, or a name := expression.
func (p *parser) namedTest() (*node, error) {
	if p.peek().kind == tokName && !reserved[p.peek().text] && p.peekAt(1).kind == tokOp && p.peekAt(1).text == ":=" {
		p.next()
		p.next()
		value, err := p.test()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{value}}, nil
	}
	return p.test()
}

func (p *parser) test() (*node, error) {
	if p.isKeyword("lambda") {
		return p.lambda()
	}
	value, err := p.orTest()
	if err != nil {
		return nil, err
	}
	if p.isKeyword("if") {
		p.next()
		condition, err := p.orTest()
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("else"); err != nil {
			return nil, err
		}
		other, err := p.test()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{value, condition, other}}, nil
	}
	return value, nil
}

func (p *parser) lambda() (*node, error) {
	p.next()
	params, defaults, err := p.parameters(":")
	if err != nil {
		return nil, err
	}
	if err := p.expectOp(":"); err != nil {
		return nil, err
	}
	body, err := p.test()
	if err != nil {
		return nil, err
	}
	// The defaults are the lambda's items, walked with its body.
	return &node{kind: nLambda, params: params, left: body, items: defaults}, nil
}

// parameters reads a parameter list up to the closing operator: names, defaults, *args, **kwargs.
func (p *parser) parameters(closing string) ([]string, []*node, error) {
	var names []string
	var defaults []*node
	for !p.isOp(closing) {
		switch {
		case p.isOp("*") || p.isOp("**"):
			p.next()
			if p.peek().kind == tokName {
				names = append(names, p.next().text)
			}
		case p.isOp("/"):
			p.next()
		case p.peek().kind == tokName && !reserved[p.peek().text]:
			names = append(names, p.next().text)
			if p.isOp(":") && closing == ")" { // an annotation
				p.next()
				annotation, err := p.test()
				if err != nil {
					return nil, nil, err
				}
				defaults = append(defaults, annotation)
			}
			if p.isOp("=") {
				p.next()
				value, err := p.test()
				if err != nil {
					return nil, nil, err
				}
				defaults = append(defaults, value)
			}
		default:
			return nil, nil, errSyntax
		}
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	return names, defaults, nil
}

func (p *parser) orTest() (*node, error) {
	return p.boolean("or", p.andTest)
}

func (p *parser) andTest() (*node, error) {
	return p.boolean("and", p.notTest)
}

func (p *parser) boolean(word string, operand func() (*node, error)) (*node, error) {
	first, err := operand()
	if err != nil {
		return nil, err
	}
	if !p.isKeyword(word) {
		return first, nil
	}
	items := []*node{first}
	for p.isKeyword(word) {
		p.next()
		next, err := operand()
		if err != nil {
			return nil, err
		}
		items = append(items, next)
	}
	return &node{kind: nOther, items: items}, nil
}

func (p *parser) notTest() (*node, error) {
	if p.isKeyword("not") {
		p.next()
		value, err := p.notTest()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{value}}, nil
	}
	return p.comparison()
}

func (p *parser) comparison() (*node, error) {
	left, err := p.bitOr()
	if err != nil {
		return nil, err
	}
	var ops []string
	var comparators []*node
	for {
		op := ""
		t := p.peek()
		switch {
		case t.kind == tokOp && (t.text == "<" || t.text == ">" || t.text == "==" || t.text == ">=" || t.text == "<=" || t.text == "!="):
			op = t.text
			p.next()
		case p.isKeyword("in"):
			op = "in"
			p.next()
		case p.isKeyword("not") && p.peekAt(1).kind == tokName && p.peekAt(1).text == "in":
			op = "not in"
			p.next()
			p.next()
		case p.isKeyword("is"):
			p.next()
			op = "is"
			if p.isKeyword("not") {
				p.next()
				op = "is not"
			}
		}
		if op == "" {
			break
		}
		right, err := p.bitOr()
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
		comparators = append(comparators, right)
	}
	if len(ops) == 0 {
		return left, nil
	}
	return &node{kind: nCompare, left: left, ops: ops, items: comparators}, nil
}

// binary reads operand (op operand)* for one precedence level.
func (p *parser) binary(ops []string, operand func() (*node, error)) (*node, error) {
	left, err := operand()
	if err != nil {
		return nil, err
	}
	for {
		matched := false
		for _, op := range ops {
			if p.isOp(op) {
				matched = true
				break
			}
		}
		if !matched {
			return left, nil
		}
		p.next()
		right, err := operand()
		if err != nil {
			return nil, err
		}
		left = &node{kind: nOther, items: []*node{left, right}}
	}
}

func (p *parser) bitOr() (*node, error)  { return p.binary([]string{"|"}, p.bitXor) }
func (p *parser) bitXor() (*node, error) { return p.binary([]string{"^"}, p.bitAnd) }
func (p *parser) bitAnd() (*node, error) { return p.binary([]string{"&"}, p.shift) }
func (p *parser) shift() (*node, error)  { return p.binary([]string{"<<", ">>"}, p.arith) }
func (p *parser) arith() (*node, error)  { return p.binary([]string{"+", "-"}, p.term) }
func (p *parser) term() (*node, error) {
	return p.binary([]string{"*", "/", "//", "%", "@"}, p.factor)
}

func (p *parser) factor() (*node, error) {
	if p.isOp("+") || p.isOp("-") || p.isOp("~") {
		p.next()
		value, err := p.factor()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{value}}, nil
	}
	return p.power()
}

func (p *parser) power() (*node, error) {
	if p.isKeyword("await") {
		p.next()
	}
	base, err := p.primary()
	if err != nil {
		return nil, err
	}
	if p.isOp("**") {
		p.next()
		exponent, err := p.factor()
		if err != nil {
			return nil, err
		}
		return &node{kind: nOther, items: []*node{base, exponent}}, nil
	}
	return base, nil
}

func (p *parser) primary() (*node, error) {
	value, err := p.atom()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("."):
			p.next()
			t := p.next()
			if t.kind != tokName {
				return nil, errSyntax
			}
			value = &node{kind: nAttr, left: value, ident: t.text}
		case p.isOp("("):
			p.next()
			call := &node{kind: nCall, left: value}
			if err := p.arguments(call); err != nil {
				return nil, err
			}
			value = call
		case p.isOp("["):
			p.next()
			index, err := p.subscripts()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp("]"); err != nil {
				return nil, err
			}
			value = &node{kind: nSubscript, left: value, index: index}
		default:
			return value, nil
		}
	}
}

func (p *parser) arguments(call *node) error {
	for !p.isOp(")") {
		switch {
		case p.isOp("**"):
			p.next()
			value, err := p.test()
			if err != nil {
				return err
			}
			call.keywords = append(call.keywords, keyword{value: value})
		case p.isOp("*"):
			p.next()
			value, err := p.test()
			if err != nil {
				return err
			}
			call.items = append(call.items, &node{kind: nOther, items: []*node{value}})
		case p.peek().kind == tokName && !reserved[p.peek().text] && p.peekAt(1).kind == tokOp && p.peekAt(1).text == "=":
			name := p.next().text
			p.next()
			value, err := p.test()
			if err != nil {
				return err
			}
			call.keywords = append(call.keywords, keyword{name: name, value: value})
		default:
			value, err := p.namedTest()
			if err != nil {
				return err
			}
			if p.isKeyword("for") || p.isKeyword("async") {
				if value, err = p.comprehension(value); err != nil {
					return err
				}
			}
			call.items = append(call.items, value)
		}
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	return p.expectOp(")")
}

func (p *parser) subscripts() (*node, error) {
	first, err := p.subscript()
	if err != nil {
		return nil, err
	}
	if !p.isOp(",") {
		return first, nil
	}
	items := []*node{first}
	for p.isOp(",") {
		p.next()
		if p.isOp("]") {
			break
		}
		item, err := p.subscript()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &node{kind: nTuple, items: items}, nil
}

func (p *parser) subscript() (*node, error) {
	var parts []*node
	slice := false
	if !p.isOp(":") {
		value, err := p.starOrTest()
		if err != nil {
			return nil, err
		}
		parts = append(parts, value)
	}
	for p.isOp(":") {
		slice = true
		p.next()
		if !p.isOp(":") && !p.isOp("]") && !p.isOp(",") {
			value, err := p.test()
			if err != nil {
				return nil, err
			}
			parts = append(parts, value)
		}
	}
	if !slice {
		return parts[0], nil
	}
	return &node{kind: nOther, items: parts}, nil
}

// comprehension reads the "for ... in ... if ..." clauses after element.
func (p *parser) comprehension(element ...*node) (*node, error) {
	items := append([]*node{}, element...)
	for p.isKeyword("for") || p.isKeyword("async") || p.isKeyword("if") {
		if p.isKeyword("if") {
			p.next()
			condition, err := p.orTest()
			if err != nil {
				return nil, err
			}
			items = append(items, condition)
			continue
		}
		if p.isKeyword("async") {
			p.next()
		}
		if err := p.expectKeyword("for"); err != nil {
			return nil, err
		}
		target, err := p.targets()
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("in"); err != nil {
			return nil, err
		}
		source, err := p.orTest()
		if err != nil {
			return nil, err
		}
		items = append(items, target, source)
	}
	return &node{kind: nOther, items: items}, nil
}

// targets is what a for statement or a comprehension assigns: expressions without comparisons,
// separated by commas, up to "in".
func (p *parser) targets() (*node, error) {
	return p.sequence(func() (*node, error) {
		if p.isOp("*") {
			p.next()
		}
		return p.bitOr()
	})
}

func (p *parser) atom() (*node, error) {
	t := p.peek()
	switch t.kind {
	case tokName:
		p.next()
		switch t.text {
		case "True":
			return &node{kind: nConst, value: true}, nil
		case "False":
			return &node{kind: nConst, value: false}, nil
		case "None":
			return &node{kind: nConst, value: nil}, nil
		}
		if reserved[t.text] {
			return nil, errSyntax
		}
		return &node{kind: nName, ident: t.text}, nil
	case tokNumber:
		p.next()
		return &node{kind: nConst, value: number(t.text)}, nil
	case tokString:
		var value strings.Builder
		formatted, isBytes := false, false
		for p.peek().kind == tokString {
			s := p.next()
			formatted = formatted || s.fmt
			isBytes = isBytes || s.bytes
			value.WriteString(s.value)
		}
		if formatted {
			return &node{kind: nOther}, nil
		}
		if isBytes {
			return &node{kind: nConst, value: bytesValue(value.String())}, nil
		}
		return &node{kind: nConst, value: value.String()}, nil
	case tokOp:
		switch t.text {
		case "(":
			p.next()
			if p.isOp(")") {
				p.next()
				return &node{kind: nTuple}, nil
			}
			n, err := p.display(")", nTuple)
			if err != nil {
				return nil, err
			}
			return n, p.expectOp(")")
		case "[":
			p.next()
			if p.isOp("]") {
				p.next()
				return &node{kind: nList}, nil
			}
			n, err := p.display("]", nList)
			if err != nil {
				return nil, err
			}
			return n, p.expectOp("]")
		case "{":
			p.next()
			n, err := p.braces()
			if err != nil {
				return nil, err
			}
			return n, p.expectOp("}")
		case "...":
			p.next()
			return &node{kind: nConst, value: "..."}, nil
		}
	}
	return nil, errSyntax
}

func number(text string) interface{} {
	clean := strings.ReplaceAll(text, "_", "")
	if value, err := strconv.ParseInt(clean, 0, 64); err == nil {
		return value
	}
	if value, err := strconv.ParseFloat(clean, 64); err == nil {
		return value
	}
	return nil
}

// display reads the inside of (...) or [...]: one parenthesized expression, a tuple or list, or a
// comprehension.
func (p *parser) display(closing string, kind nodeKind) (*node, error) {
	first, err := p.starOrTest()
	if err != nil {
		return nil, err
	}
	if p.isKeyword("for") || p.isKeyword("async") {
		return p.comprehension(first)
	}
	if kind == nTuple && !p.isOp(",") {
		return first, nil
	}
	items := []*node{first}
	for p.isOp(",") {
		p.next()
		if p.isOp(closing) {
			break
		}
		item, err := p.starOrTest()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &node{kind: kind, items: items}, nil
}

// braces reads the inside of {...}: a dict, a set, or a comprehension of either.
func (p *parser) braces() (*node, error) {
	if p.isOp("}") {
		return &node{kind: nDict}, nil
	}
	dict := &node{kind: nDict}
	set := &node{kind: nSet}
	for !p.isOp("}") {
		if p.isOp("**") {
			p.next()
			value, err := p.bitOr()
			if err != nil {
				return nil, err
			}
			dict.keys = append(dict.keys, nil)
			dict.values = append(dict.values, value)
		} else {
			key, err := p.starOrTest()
			if err != nil {
				return nil, err
			}
			if p.isOp(":") {
				p.next()
				value, err := p.test()
				if err != nil {
					return nil, err
				}
				if p.isKeyword("for") || p.isKeyword("async") {
					return p.comprehension(key, value)
				}
				dict.keys = append(dict.keys, key)
				dict.values = append(dict.values, value)
			} else {
				if p.isKeyword("for") || p.isKeyword("async") {
					return p.comprehension(key)
				}
				set.items = append(set.items, key)
			}
		}
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	if len(set.items) > 0 {
		if len(dict.keys) > 0 {
			return nil, errSyntax
		}
		return set, nil
	}
	return dict, nil
}

// --- statements ---------------------------------------------------------------------------------

// program is what parseCode reads of a piece of code: every statement's expressions, and the for
// loops whose target is a single name.
type program struct {
	exprs []*node
	loops []forLoop
}

type forLoop struct {
	target string
	iter   *node
}

// parseCode reads server-action code (Python's exec mode), one logical line at a time.
func parseCode(source string) (*program, error) {
	tokens, err := tokenize(source)
	if err != nil {
		return nil, err
	}
	prog := &program{}
	start := 0
	for i := 0; i <= len(tokens); i++ {
		if i < len(tokens) && tokens[i].kind != tokNewline {
			continue
		}
		if i > start {
			p := &parser{tokens: tokens[start:i]}
			if err := p.statement(prog); err != nil {
				return nil, err
			}
			if !p.atEnd() {
				return nil, errSyntax
			}
		}
		start = i + 1
	}
	return prog, nil
}

func (p *parser) add(prog *program, nodes ...*node) {
	for _, n := range nodes {
		if n != nil {
			prog.exprs = append(prog.exprs, n)
		}
	}
}

// statement reads one logical line: a compound statement's header and the body on its line, or
// simple statements separated by semicolons.
func (p *parser) statement(prog *program) error {
	if p.isKeyword("async") {
		p.next()
	}
	switch {
	case p.isKeyword("for"):
		p.next()
		target, err := p.targets()
		if err != nil {
			return err
		}
		if err := p.expectKeyword("in"); err != nil {
			return err
		}
		iter, err := p.testList()
		if err != nil {
			return err
		}
		if target.kind == nName {
			prog.loops = append(prog.loops, forLoop{target: target.ident, iter: iter})
		}
		p.add(prog, target, iter)
		return p.header(prog)
	case p.isKeyword("if") || p.isKeyword("elif") || p.isKeyword("while"):
		p.next()
		condition, err := p.namedTest()
		if err != nil {
			return err
		}
		p.add(prog, condition)
		return p.header(prog)
	case p.isKeyword("else") || p.isKeyword("try") || p.isKeyword("finally"):
		p.next()
		return p.header(prog)
	case p.isKeyword("except"):
		p.next()
		if p.isOp("*") {
			p.next()
		}
		if !p.isOp(":") {
			kind, err := p.test()
			if err != nil {
				return err
			}
			p.add(prog, kind)
			if p.isKeyword("as") {
				p.next()
				p.next()
			}
		}
		return p.header(prog)
	case p.isKeyword("with"):
		p.next()
		for {
			item, err := p.test()
			if err != nil {
				return err
			}
			p.add(prog, item)
			if p.isKeyword("as") {
				p.next()
				target, err := p.bitOr()
				if err != nil {
					return err
				}
				p.add(prog, target)
			}
			if !p.isOp(",") {
				break
			}
			p.next()
		}
		return p.header(prog)
	case p.isKeyword("def") || p.isKeyword("class"):
		isDef := p.isKeyword("def")
		p.next()
		if p.peek().kind != tokName {
			return errSyntax
		}
		p.next()
		if p.isOp("(") {
			p.next()
			if isDef {
				_, defaults, err := p.parameters(")")
				if err != nil {
					return err
				}
				p.add(prog, defaults...)
				if err := p.expectOp(")"); err != nil {
					return err
				}
			} else {
				base := &node{kind: nCall, left: &node{kind: nName, ident: "class"}}
				if err := p.arguments(base); err != nil {
					return err
				}
				p.add(prog, base)
			}
		}
		if p.isOp("->") {
			p.next()
			returns, err := p.test()
			if err != nil {
				return err
			}
			p.add(prog, returns)
		}
		return p.header(prog)
	case p.isOp("@"):
		p.next()
		decorator, err := p.namedTest()
		if err != nil {
			return err
		}
		p.add(prog, decorator)
		return nil
	}
	return p.simpleStatements(prog)
}

// header ends a compound statement's header at its colon and reads the body that shares its line.
func (p *parser) header(prog *program) error {
	if err := p.expectOp(":"); err != nil {
		return err
	}
	if p.atEnd() {
		return nil
	}
	return p.simpleStatements(prog)
}

func (p *parser) simpleStatements(prog *program) error {
	for {
		if err := p.simple(prog); err != nil {
			return err
		}
		if !p.isOp(";") {
			return nil
		}
		p.next()
		if p.atEnd() {
			return nil
		}
	}
}

func isAugmented(t token) bool {
	if t.kind != tokOp {
		return false
	}
	switch t.text {
	case "+=", "-=", "*=", "/=", "//=", "%=", "**=", ">>=", "<<=", "&=", "|=", "^=", "@=":
		return true
	}
	return false
}

func (p *parser) simple(prog *program) error {
	stop := func() bool { return p.atEnd() || p.isOp(";") }
	switch {
	case p.isKeyword("pass") || p.isKeyword("break") || p.isKeyword("continue"):
		p.next()
		return nil
	case p.isKeyword("global") || p.isKeyword("nonlocal") || p.isKeyword("import") || p.isKeyword("from"):
		for !stop() {
			p.next()
		}
		return nil
	case p.isKeyword("return") || p.isKeyword("yield"):
		p.next()
		if p.isKeyword("from") {
			p.next()
		}
		if stop() {
			return nil
		}
		value, err := p.testList()
		if err != nil {
			return err
		}
		p.add(prog, value)
		return nil
	case p.isKeyword("raise"):
		p.next()
		if stop() {
			return nil
		}
		value, err := p.test()
		if err != nil {
			return err
		}
		p.add(prog, value)
		if p.isKeyword("from") {
			p.next()
			cause, err := p.test()
			if err != nil {
				return err
			}
			p.add(prog, cause)
		}
		return nil
	case p.isKeyword("assert"):
		p.next()
		condition, err := p.test()
		if err != nil {
			return err
		}
		p.add(prog, condition)
		if p.isOp(",") {
			p.next()
			message, err := p.test()
			if err != nil {
				return err
			}
			p.add(prog, message)
		}
		return nil
	case p.isKeyword("del"):
		p.next()
		targets, err := p.targets()
		if err != nil {
			return err
		}
		p.add(prog, targets)
		return nil
	}
	first, err := p.testList()
	if err != nil {
		return err
	}
	p.add(prog, first)
	switch {
	case isAugmented(p.peek()):
		p.next()
		value, err := p.testList()
		if err != nil {
			return err
		}
		p.add(prog, value)
	case p.isOp(":"): // an annotated assignment
		p.next()
		annotation, err := p.test()
		if err != nil {
			return err
		}
		p.add(prog, annotation)
		if p.isOp("=") {
			p.next()
			value, err := p.testList()
			if err != nil {
				return err
			}
			p.add(prog, value)
		}
	default:
		for p.isOp("=") {
			p.next()
			value, err := p.testList()
			if err != nil {
				return err
			}
			p.add(prog, value)
		}
	}
	return nil
}
