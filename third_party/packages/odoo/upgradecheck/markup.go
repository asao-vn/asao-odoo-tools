package upgradecheck

import (
	"encoding/xml"
	"errors"
	"html"
	"io"
	"strings"
)

// startTags reads the attributes of every start tag of an HTML text, as a lenient HTML parser does:
// names in lower case, values unescaped, comments and declarations skipped. A mail template's body
// is HTML that need not be well-formed XML, so it is never refused.
func startTags(body string) []map[string]string {
	var tags []map[string]string
	for i := 0; i < len(body); {
		lt := strings.IndexByte(body[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		rest := body[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest[4:], "-->")
			if end < 0 {
				return tags
			}
			i += 4 + end + 3
			continue
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?") || strings.HasPrefix(rest, "</"):
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				return tags
			}
			i += end + 1
			continue
		}
		if len(rest) < 2 || !isASCIILetter(rest[1]) {
			i++
			continue
		}
		j := 1
		for j < len(rest) && !isSpace(rest[j]) && rest[j] != '>' && rest[j] != '/' {
			j++
		}
		attributes := map[string]string{}
		for j < len(rest) && rest[j] != '>' {
			if isSpace(rest[j]) || rest[j] == '/' {
				j++
				continue
			}
			start := j
			for j < len(rest) && !isSpace(rest[j]) && rest[j] != '=' && rest[j] != '>' && !(rest[j] == '/' && j+1 < len(rest) && rest[j+1] == '>') {
				j++
			}
			name := strings.ToLower(rest[start:j])
			for j < len(rest) && isSpace(rest[j]) {
				j++
			}
			value := ""
			if j < len(rest) && rest[j] == '=' {
				j++
				for j < len(rest) && isSpace(rest[j]) {
					j++
				}
				if j < len(rest) && (rest[j] == '"' || rest[j] == '\'') {
					quote := rest[j]
					end := strings.IndexByte(rest[j+1:], quote)
					if end < 0 {
						value = rest[j+1:]
						j = len(rest)
					} else {
						value = rest[j+1 : j+1+end]
						j += end + 2
					}
				} else {
					start := j
					for j < len(rest) && !isSpace(rest[j]) && rest[j] != '>' {
						j++
					}
					value = rest[start:j]
				}
				value = html.UnescapeString(value)
			}
			if name != "" {
				attributes[name] = value
			}
		}
		tags = append(tags, attributes)
		i += j
	}
	return tags
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isSpace(c byte) bool       { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// xmlElement is one element of a view's arch.
type xmlElement struct {
	tag        string
	attributes map[string]string
	children   []*xmlElement
	text       string // the text directly inside the element
}

func (e *xmlElement) attr(name string) string { return e.attributes[name] }

// parseXML reads a view's arch, which Odoo requires to be well-formed XML with one root.
func parseXML(arch string) (*xmlElement, error) {
	decoder := xml.NewDecoder(strings.NewReader(arch))
	var root *xmlElement
	var stack []*xmlElement
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			element := &xmlElement{tag: tok.Name.Local, attributes: map[string]string{}}
			for _, attribute := range tok.Attr {
				element.attributes[attribute.Name.Local] = attribute.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("xml: more than one root element")
				}
				root = element
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, element)
			}
			stack = append(stack, element)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(tok)
			} else if strings.TrimSpace(string(tok)) != "" {
				return nil, errors.New("xml: text outside the root element")
			}
		}
	}
	if root == nil {
		return nil, errors.New("xml: no root element")
	}
	return root, nil
}
