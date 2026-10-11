package odoo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The provider series gate.
//
// Everything this package knows about Odoo was established against ONE series, 19.0: the
// catalog's field and method mappings, the semantic contracts, the module requirements and every
// conformance record (Odoo19HistoricalProviderVersion). Another series is not "probably the same".
// Measured against Odoo 20.0 (build 20.0-20260926) on 2026-10-10, ten catalog operations read
// fields that no longer exist (res.partner.company_type, stock.quant.product_uom_id,
// hr.leave.holiday_status_id, uom.uom.rounding, resource.calendar.tz, ...) and fail, while others
// keep answering with a different meaning (account groups became parent accounts, so the chart of
// accounts list grows from 218 to 301 rows). The second kind is the dangerous one: nothing fails.
//
// So the gate fails closed. An RPCClient asks the provider for its series BEFORE it sends any
// credential and refuses to log in, and therefore refuses every execute_kw, unless the series is
// one this build carries a catalog for. There is no fallback from an unknown or newer series to
// the 19.0 catalog, and an answer this code cannot parse is a refusal, not a guess.
//
// A series moves through two states. A CANDIDATE series has a dialect (provider_dialect.go) that
// makes it answer in the 19.0 vocabulary the catalog is written in, but no accepted conformance
// evidence: a client refuses it unless its caller explicitly admits candidates
// (RPCClient.AdmitCandidateSeries), and every operation on it reads UNVERIFIED. It becomes
// SUPPORTED only when its conformance evidence is accepted. Editing these lists alone makes no
// series supported.

// Odoo19ProviderSeries is the Odoo series the catalog in this package is established for.
const Odoo19ProviderSeries = "19.0"

// Odoo20ProviderSeries has a dialect (odoo20Dialect) and no accepted conformance evidence yet.
const Odoo20ProviderSeries = "20.0"

// SupportedProviderSeries lists the Odoo series this build can execute its catalog against. The
// comparison is exact: "saas~19.1" (Odoo Online) is a different series from "19.0" and has
// already removed provider surface the 19.0 evidence relied on.
func SupportedProviderSeries() []string {
	return []string{Odoo19ProviderSeries}
}

// CandidateProviderSeries lists the series this build carries a dialect for but no accepted
// conformance evidence. A client executes against one only when its caller admits candidates.
func CandidateProviderSeries() []string {
	return []string{Odoo20ProviderSeries}
}

// ProviderVersionInfo is what an Odoo server reports about itself through common.version, which
// answers without authentication.
type ProviderVersionInfo struct {
	// ServerVersion is the full build string, e.g. "19.0-20260803", "19.0+e", "saas~19.1+e".
	ServerVersion string `json:"serverVersion"`
	// ServerSerie is the series as the server names it, e.g. "19.0" or "saas~19.1".
	ServerSerie string `json:"serverSerie"`
}

var leadingSeries = regexp.MustCompile(`^(?:saas~)?[0-9]+\.[0-9]+`)

// Series is the provider series: server_serie when the server reports one, otherwise the
// "MAJOR.MINOR" prefix of server_version. It is empty when neither yields a series.
func (i ProviderVersionInfo) Series() string {
	if serie := strings.TrimSpace(i.ServerSerie); serie != "" {
		return serie
	}
	return leadingSeries.FindString(strings.TrimSpace(i.ServerVersion))
}

// ProviderVersionUnsupportedError refuses a provider whose series this build has no catalog for.
// It is raised before login, so the provider never received the credential.
type ProviderVersionUnsupportedError struct {
	ProviderVersion string
	ProviderSeries  string
	Supported       []string
	// Candidate is true when the series has a dialect but no accepted evidence: the caller could
	// have admitted it explicitly and did not.
	Candidate bool
	// Reason says why the series is refused: SeriesOlder, SeriesNewer, SeriesIntermediate,
	// SeriesCandidate or SeriesUnrecognized.
	Reason string
}

// Why a series is refused (DEC-2026-0157).
const (
	// SeriesOlder is older than every series this build knows: it was never verified.
	SeriesOlder = "OLDER_THAN_SUPPORTED"
	// SeriesNewer is newer than every series this build knows.
	SeriesNewer = "NEWER_THAN_SUPPORTED"
	// SeriesIntermediate is an intermediate Odoo Online release (saas~X.Y), between two majors.
	SeriesIntermediate = "INTERMEDIATE_RELEASE"
	// SeriesCandidate has a dialect but no accepted evidence, and the caller did not admit it.
	SeriesCandidate = "CANDIDATE_NOT_ADMITTED"
	// SeriesUnrecognized names no series this build can place.
	SeriesUnrecognized = "UNRECOGNIZED"
)

// seriesRefusalReason places a refused series against the series this build knows.
func seriesRefusalReason(series string) string {
	if strings.HasPrefix(series, "saas~") {
		return SeriesIntermediate
	}
	major, ok := seriesMajor(series)
	if !ok {
		return SeriesUnrecognized
	}
	oldest, newest := -1, -1
	for _, known := range append(SupportedProviderSeries(), CandidateProviderSeries()...) {
		if m, ok := seriesMajor(known); ok {
			if oldest < 0 || m < oldest {
				oldest = m
			}
			if m > newest {
				newest = m
			}
		}
	}
	switch {
	case major < oldest:
		return SeriesOlder
	case major > newest:
		return SeriesNewer
	}
	return SeriesUnrecognized
}

func seriesMajor(series string) (int, bool) {
	head, _, _ := strings.Cut(strings.TrimPrefix(series, "saas~"), ".")
	major, err := strconv.Atoi(head)
	return major, err == nil
}

func (e *ProviderVersionUnsupportedError) Error() string {
	series := e.ProviderSeries
	if series == "" {
		series = "unknown"
	}
	if e.Candidate {
		return fmt.Sprintf("odoo provider series %s has no accepted conformance evidence in this build (supported: %s) and was not admitted as a candidate; nothing was executed",
			series, strings.Join(e.Supported, ", "))
	}
	why := ""
	switch e.Reason {
	case SeriesOlder:
		why = ": it is older than every series this build was verified on"
	case SeriesNewer:
		why = ": it is newer than every series this build knows"
	case SeriesIntermediate:
		why = ": it is an intermediate Odoo Online release, between two verified majors"
	}
	return fmt.Sprintf("odoo provider series %s is not supported by this build (supported: %s)%s; nothing was executed",
		series, strings.Join(e.Supported, ", "), why)
}

func (e *ProviderVersionUnsupportedError) ErrorClass() string {
	return string(ErrClassProviderVersionUnsupported)
}

func (e *ProviderVersionUnsupportedError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_version": e.ProviderVersion,
		"provider_series":  e.ProviderSeries,
		"supported_series": append([]string(nil), e.Supported...),
		"candidate":        e.Candidate,
		"reason":           e.Reason,
	}
}

// CheckProviderSeries returns nil only when info names a series in SupportedProviderSeries.
func CheckProviderSeries(info ProviderVersionInfo) error {
	return checkProviderSeriesAdmission(info, false)
}

// checkProviderSeriesAdmission also accepts a candidate series when admitCandidates is true.
func checkProviderSeriesAdmission(info ProviderVersionInfo, admitCandidates bool) error {
	series := info.Series()
	supported := SupportedProviderSeries()
	refusal := &ProviderVersionUnsupportedError{ProviderVersion: info.ServerVersion, ProviderSeries: series, Supported: supported, Reason: seriesRefusalReason(series)}
	if series == "" {
		return refusal
	}
	for _, known := range supported {
		if series == known {
			return nil
		}
	}
	for _, candidate := range CandidateProviderSeries() {
		if series == candidate {
			if admitCandidates {
				return nil
			}
			refusal.Candidate = true
			refusal.Reason = SeriesCandidate
			return refusal
		}
	}
	return refusal
}
