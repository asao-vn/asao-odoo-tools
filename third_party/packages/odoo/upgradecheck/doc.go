// Package upgradecheck says, before an upgrade to the next Odoo series, which of the customizations
// people made in a database will stop working there: saved filters, automation rules, server
// actions, custom fields, email templates, views, record rules, access rights, export templates,
// default values and window actions.
//
// Every model and field such a customization references is resolved on the source database first,
// then on the target series, through a map made from the registries of the two series' conformance
// labs (data/, built by scripts/dev/odoo-upgrade-check/bundle.py). What the map does not know (an
// Enterprise or third-party module) is said to be unknown, never guessed. Code and templates are read
// to find references and never quoted.
//
// The method and its measurement on customizations planted on 19.0 and replayed on 20.0:
// docs/architecture/provider-research/odoo-upgrade-check/README.md in the ASAO monorepo.
package upgradecheck
