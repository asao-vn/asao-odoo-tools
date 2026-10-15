// Package faultreport is what `odoo-cli report draft` and `asao feedback` share (DEC-2026-0170): the
// kinds of fault a report may be about, the fingerprint that groups reports of one fault, and the
// scrubbing that keeps a person's systems, people and secrets out of agent-written text.
//
// It imports nothing from either command tree (only clicore), so the standalone and the governed binaries can both
// use it without reaching each other.
package faultreport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// Kinds are what a report may be about. They name a fault in the TOOL, never in the person's data.
var Kinds = []string{"BUG", "WRONG_RESULT", "MISSING_CAPABILITY", "UNCLEAR_DOCS", "WORKAROUND_USED", "IDEA"}

// KindDescription is the flag help every surface uses for --kind.
const KindDescription = "BUG (the tool failed), WRONG_RESULT (it answered, wrongly), MISSING_CAPABILITY (you needed something it does not offer), UNCLEAR_DOCS (help or remediation misled you), WORKAROUND_USED (you got past it another way), IDEA."

// FieldLimit bounds every free-text field, in runes.
const FieldLimit = 2000

var (
	errorCodePattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)
	commandWordPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// ValidKind reports whether kind is one of Kinds.
func ValidKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ValidErrorCode reports whether code looks like an error.code, not a message.
func ValidErrorCode(code string) bool { return errorCodePattern.MatchString(code) }

// CommandPath keeps the leading command words of raw and drops every flag and value: a value may be a
// record, and the path is all a maintainer needs. The program names given are dropped too.
func CommandPath(raw string, programs ...string) string {
	var words []string
	for _, word := range strings.Fields(raw) {
		if strings.HasPrefix(word, "-") {
			break
		}
		if contains(programs, word) {
			continue
		}
		if !commandWordPattern.MatchString(word) {
			break
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

// Fingerprint names one fault: same kind, command and error code, same fingerprint. Without a code the
// (scrubbed) summary stands in, which is the best a free-text fault offers.
func Fingerprint(prefix, kind, command, code, summary string) string {
	key := kind + "|" + command + "|" + code
	if code == "" {
		key += "|" + strings.ToLower(OneLine(summary))
	}
	sum := sha256.Sum256([]byte(key))
	return prefix + hex.EncodeToString(sum[:5])
}

// Term is one literal to replace, such as a profile's endpoint, with the label shown instead.
type Term struct{ Value, Label string }

// Scrubber removes what identifies the person's systems, people or secrets from free text. Count is
// how many replacements it made.
type Scrubber struct {
	terms []Term
	Count int
}

var patterns = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|apikey|authorization)(\s*[:=]\s*|\s+)("[^"]*"|'[^']*'|\S+)`), "$1=<redacted>"},
	{regexp.MustCompile(`(?i)\b(?:https?|wss?|ftp)://[^\s)>"'\]]+`), "<url>"},
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "<email>"},
	{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`), "<ip>"},
	{regexp.MustCompile(`\b[A-Za-z0-9_\-+/=]{32,}\b`), "<redacted>"},
	{regexp.MustCompile(`\+?\d(?:[ .-]?\d){8,}`), "<number>"},
}

// NewScrubber replaces the given terms (longest first) and the home directory, then the generic
// patterns. Terms shorter than three characters are ignored: they would eat ordinary words.
func NewScrubber(terms ...Term) *Scrubber {
	s := &Scrubber{}
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		s.terms = append(s.terms, Term{home, "~"})
	}
	for _, term := range terms {
		if len(strings.TrimSpace(term.Value)) >= 3 {
			s.terms = append(s.terms, term)
		}
	}
	sort.SliceStable(s.terms, func(i, j int) bool { return len(s.terms[i].Value) > len(s.terms[j].Value) })
	return s
}

// Clean scrubs text, after bounding it to FieldLimit runes.
func (s *Scrubber) Clean(text string) string {
	text = Truncate(strings.TrimSpace(text), FieldLimit)
	for _, term := range s.terms {
		pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(term.Value))
		text = pattern.ReplaceAllStringFunc(text, func(string) string { s.Count++; return term.Label })
	}
	for _, rule := range patterns {
		text = rule.pattern.ReplaceAllStringFunc(text, func(match string) string {
			s.Count++
			return rule.pattern.ReplaceAllString(match, rule.replace)
		})
	}
	return text
}

// OneLine collapses whitespace, newlines included.
func OneLine(value string) string { return strings.Join(strings.Fields(value), " ") }

// Truncate bounds value to max runes, marking a cut with an ellipsis.
func Truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max-1]) + "…"
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Advisor points an agent at the draft command when the tool itself failed: an INTERNAL error. It
// only adds context; the refusal's code, category and exit code stay what the command said.
func Advisor(program, draftCommand, consentRule string) clicore.ErrorAdvisor {
	return func(invocation *clicore.Invocation, err *clicore.Error) {
		if err == nil || err.Category != clicore.CategoryInternal || invocation == nil || invocation.Command == nil {
			return
		}
		path := invocation.Command.PathString()
		if strings.HasPrefix(path, strings.Fields(draftCommand)[0]) {
			return
		}
		err.WithContextValue("report", map[string]interface{}{
			"why": "This failure is inside " + program + ", not in your input or the person's data. The maintainers can fix it only if someone tells them.",
			"draft": fmt.Sprintf(`%s %s --kind BUG --command %q --error-code %s --summary "<one line>" --output json`,
				program, draftCommand, path, err.Code),
			"consent": consentRule,
		})
	}
}
