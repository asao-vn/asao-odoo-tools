package odoo

import (
	"strings"
	"unicode/utf8"
)

// Field classes (DEC-2026-0178): what a field holds, by its name and Odoo type, so that how much of
// the data an agent sees is decided the same way for the catalog's reads, explore, packs and the
// review.
type FieldClass string

const (
	// FieldCredential holds a secret: never shown at any level.
	FieldCredential FieldClass = "CREDENTIAL"
	// FieldPersonal says who a person is or how to reach them: phones, emails, addresses, tax and
	// identity numbers, bank accounts, birthdays.
	FieldPersonal FieldClass = "PERSONAL"
	// FieldFreeText is text people write (notes, descriptions, messages): where an outsider's
	// instructions arrive.
	FieldFreeText FieldClass = "FREE_TEXT"
	// FieldFile is a file or an image.
	FieldFile FieldClass = "FILE"
	// FieldPlain is everything else: names, references, states, dates, numbers, amounts, relations.
	FieldPlain FieldClass = "PLAIN"
)

var personalFieldWords = []string{
	"phone", "mobile", "email", "street", "zip", "city", "vat", "birthday", "birthdate", "identification",
	"passport", "ssn", "sinid", "private_", "bank_account", "acc_number", "address_home", "km_home_work",
	"emergency_contact", "emergency_phone", "spouse", "children", "place_of_birth", "country_of_birth",
}

var freeTextFieldWords = []string{"description", "note", "comment", "body", "message", "narration", "remark", "reason", "feedback"}

// ClassifyField classes a field by its name and, when known, its Odoo type ("" when not).
func ClassifyField(name, odooType string) FieldClass {
	lowered := strings.ToLower(name)
	switch {
	case IsSecretField(lowered):
		return FieldCredential
	case odooType == "binary" || odooType == "image":
		return FieldFile
	}
	for _, word := range personalFieldWords {
		if strings.Contains(lowered, word) {
			return FieldPersonal
		}
	}
	if odooType == "text" || odooType == "html" {
		return FieldFreeText
	}
	for _, word := range freeTextFieldWords {
		if strings.Contains(lowered, word) {
			return FieldFreeText
		}
	}
	return FieldPlain
}

// freeTextValueLength is how long a plain field's text may be before it is taken for free text: a
// name, a reference or a state is short; a note is not.
const freeTextValueLength = 160

// LooksLikeFreeText says whether a value read from an unknown field reads as text people wrote: it
// spans lines or runs long. It is the safety net under ClassifyField's names, for a field such as
// x_customer_remark whose name says nothing.
func LooksLikeFreeText(value string) bool {
	return strings.ContainsAny(value, "\n\r") || utf8.RuneCountInString(value) > freeTextValueLength
}
