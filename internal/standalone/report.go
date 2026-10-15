package standalone

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/faultreport"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// reportRepository is where a report is filed: the public odoo-cli repository. ldflags-injectable
// so a fork files against itself.
var reportRepository = "asao-vn/asao-odoo-tools"

const (
	// reportURLLimit keeps the prefilled issue link under what browsers and GitHub accept.
	reportURLLimit = 7000
)

// reportConsentRule is the one sentence every surface (the draft result, the error hint, the MCP
// instructions) repeats, so an agent meets the same rule wherever it learns about reporting.
const reportConsentRule = "Drafting sends nothing. Only the person files a report: show them the draft, ask, and if they agree give them the link — GitHub shows them the report again before they press Submit. Never file it yourself (gh, a browser tool, an HTTP call)."

var reportFlags = []clicore.Flag{
	{Name: "kind", Kind: clicore.KindString, Required: true, Enum: faultreport.Kinds,
		Description: faultreport.KindDescription},
	{Name: "summary", Kind: clicore.KindString, Required: true, Description: "One line: what went wrong in odoo-cli."},
	{Name: "command", Kind: clicore.KindString, Description: "The odoo-cli command path involved, without flags or values (e.g. \"sales order list\")."},
	{Name: "error-code", Kind: clicore.KindString, Description: "The error.code the command returned, if any."},
	{Name: "expected", Kind: clicore.KindString, Description: "What you expected the command to do."},
	{Name: "observed", Kind: clicore.KindString, Description: "What it did instead. Describe it; do not paste record data."},
	{Name: "workaround", Kind: clicore.KindString, Description: "How you got the task done anyway, if you did. This is the most useful field for the maintainers."},
	{Name: "suggestion", Kind: clicore.KindString, Description: "How you think odoo-cli should behave or be fixed."},
	{Name: "agent", Kind: clicore.KindString, Description: "Who is reporting, e.g. \"claude-code opus\", \"codex\", \"gemini-cli\", \"person\"."},
	{Name: "odoo-series", Kind: clicore.KindString, Description: "The Odoo series involved (e.g. 19.0), if known."},
}

func reportGroup(config programConfig) *clicore.Command {
	draft := local(&clicore.Command{
		Name:    "draft",
		Summary: "Draft a report about a fault in this tool, for the person to file. Sends nothing.",
		Description: `Use it when ` + config.name + ` itself let you down: an INTERNAL error, a result that contradicts the
command's description, a capability you needed and had to work around, or help that misled you. Not
for refusals that are about the person's input, their Odoo or their permissions.

The draft is built on this machine and nothing leaves it. Free text is scrubbed before it is shown:
every endpoint, database, login and profile name in the local profile file, URLs, e-mail addresses,
IP addresses, long tokens and anything written as password=/token=/api_key=. The result carries the
title and body to show the person, a fingerprint to find earlier reports of the same fault, and a
link that opens a prefilled issue on GitHub.

` + reportConsentRule,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       reportFlags,
		Examples: []string{
			config.name + ` report draft --kind BUG --command "sales order list" --error-code INTERNAL_ERROR --summary "panics when an order has no lines" --agent "claude-code" --output json`,
			config.name + ` report draft --kind WORKAROUND_USED --command "inventory transfer list" --summary "no scheduled_date field" --workaround "read stock.picking with explore" --output json`,
		},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return draftReport(config.name, invocation)
		},
	})
	return &clicore.Command{
		Name:     "report",
		Summary:  "Report a fault in this tool to its maintainers — drafted by you or your agent, filed only by you.",
		Children: []*clicore.Command{draft},
	}
}

func draftReport(program string, invocation *clicore.Invocation) (interface{}, error) {
	kind := invocation.String("kind")
	code := strings.TrimSpace(invocation.String("error-code"))
	if code != "" && !faultreport.ValidErrorCode(code) {
		return nil, clicore.UsageError("REPORT_ERROR_CODE_INVALID", "--error-code must be an error.code such as INTERNAL_ERROR").
			WithRemediation("Pass the code field of the error document only, not its message.")
	}
	command := faultreport.CommandPath(invocation.String("command"), "odoo-cli", "aerp", "odoo")
	scrub := newScrubber()

	fields := []struct{ heading, value string }{
		{"Expected", invocation.String("expected")},
		{"Observed", invocation.String("observed")},
		{"Workaround used", invocation.String("workaround")},
		{"Suggested fix", invocation.String("suggestion")},
	}
	summary := scrub.Clean(faultreport.OneLine(invocation.String("summary")))
	if summary == "" {
		return nil, clicore.UsageError("REPORT_SUMMARY_EMPTY", "--summary must say what went wrong")
	}

	compatibility := direct.CatalogProvenance().CompatibilityIdentity
	fingerprint := faultreport.Fingerprint("ocr-", kind, command, code, summary)
	environment := [][2]string{
		{"tool", program + " " + releaseVersion + " (contract " + Version + ")"},
		{"catalog", compatibility.CatalogIdentity},
		{"platform", runtime.GOOS + "/" + runtime.GOARCH},
	}
	if series := faultreport.OneLine(invocation.String("odoo-series")); series != "" {
		environment = append(environment, [2]string{"odoo series", scrub.Clean(series)})
	}
	if agent := faultreport.OneLine(invocation.String("agent")); agent != "" {
		environment = append(environment, [2]string{"reported by", scrub.Clean(agent)})
	}

	var body strings.Builder
	fmt.Fprintf(&body, "**Kind:** %s  \n", kind)
	if command != "" {
		fmt.Fprintf(&body, "**Command:** `%s %s`  \n", program, command)
	}
	if code != "" {
		fmt.Fprintf(&body, "**Error code:** `%s`  \n", code)
	}
	fmt.Fprintf(&body, "**Fingerprint:** `%s`\n", fingerprint)
	for _, field := range fields {
		if text := scrub.Clean(field.value); text != "" {
			fmt.Fprintf(&body, "\n### %s\n%s\n", field.heading, text)
		}
	}
	body.WriteString("\n### Environment\n")
	for _, pair := range environment {
		fmt.Fprintf(&body, "- %s: %s\n", pair[0], pair[1])
	}
	body.WriteString("\n_Drafted with `" + program + " report draft`; scrubbed on the reporter's machine and filed by the person._\n")

	title := faultreport.Truncate(fmt.Sprintf("[%s] %s", strings.ToLower(kind), summary), 120)
	issueURL, truncated := issueLink(title, body.String())
	return map[string]interface{}{
		"status":      "DRAFT_NOT_SENT",
		"sent":        false,
		"fingerprint": fingerprint,
		"title":       title,
		"body":        body.String(),
		"redactions":  scrub.Count,
		"destination": "https://github.com/" + reportRepository + "/issues",
		// The person opens this; GitHub shows the report and asks them to press Submit.
		"fileIssueURL":       issueURL,
		"bodyTruncatedInURL": truncated,
		"existingReportsURL": "https://github.com/" + reportRepository + "/issues?q=" + url.QueryEscape(fingerprint),
		"consent": map[string]interface{}{
			"required": true,
			"rule":     reportConsentRule,
			"askThePerson": "Tell the person in one or two sentences why you think " + program +
				" is at fault, show them the title and body, and ask whether they want to file it. If an issue with this fingerprint already exists, offer that link instead.",
		},
	}, nil
}

// reportAdvisor points an agent at `report draft` when the tool itself failed.
func reportAdvisor(program string) clicore.ErrorAdvisor {
	return faultreport.Advisor(program, "report draft", reportConsentRule)
}

func issueLink(title, body string) (string, bool) {
	base := "https://github.com/" + reportRepository + "/issues/new?labels=agent-report&title=" + url.QueryEscape(title) + "&body="
	note := "\n\n_(cut to fit the link; the reporter has the full draft)_"
	if link := base + url.QueryEscape(body); len(link) <= reportURLLimit {
		return link, false
	}
	runes := []rune(body)
	for len(runes) > 0 {
		runes = runes[:len(runes)*9/10]
		if link := base + url.QueryEscape(string(runes)+note); len(link) <= reportURLLimit {
			return link, true
		}
	}
	return base, true
}

// newScrubber scrubs, besides the generic patterns, every endpoint, host, database, login, credential
// reference and profile name in the local profile file.
func newScrubber() *faultreport.Scrubber {
	store, err := localprofile.LoadOrEmpty()
	if err != nil || store == nil {
		return faultreport.NewScrubber()
	}
	var terms []faultreport.Term
	for _, name := range store.Names() {
		profile := store.Profiles[name]
		if parsed, err := url.Parse(profile.Endpoint); err == nil && parsed.Hostname() != "" {
			terms = append(terms, faultreport.Term{Value: parsed.Hostname(), Label: "<odoo-host>"})
		}
		terms = append(terms,
			faultreport.Term{Value: profile.Endpoint, Label: "<odoo-endpoint>"},
			faultreport.Term{Value: profile.Database, Label: "<odoo-database>"},
			faultreport.Term{Value: profile.Login, Label: "<odoo-login>"},
			faultreport.Term{Value: profile.CredentialRef, Label: "<credential-ref>"},
			faultreport.Term{Value: name, Label: "<profile>"})
	}
	return faultreport.NewScrubber(terms...)
}
