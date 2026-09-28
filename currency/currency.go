// Package currency defines ISO 4217 currency codes used by the Balíkobot API.
//
// Code is a string type, so a currency code stays a plain JSON string on the
// wire. Use the constants for common currencies, or FromString for any other
// well-formed code. The type makes currency mistakes visible at compile time.
package currency

import (
	"fmt"
	"regexp"
	"strings"
)

// Code is an ISO 4217 currency code.
type Code string

// Common ISO 4217 currency codes.
const (
	// CZK is the Czech koruna.
	CZK Code = "CZK"
	// EUR is the euro.
	EUR Code = "EUR"
	// USD is the United States dollar.
	USD Code = "USD"
	// GBP is the pound sterling.
	GBP Code = "GBP"
	// PLN is the Polish złoty.
	PLN Code = "PLN"
	// HUF is the Hungarian forint.
	HUF Code = "HUF"
	// RON is the Romanian leu.
	RON Code = "RON"
	// BGN is the Bulgarian lev.
	BGN Code = "BGN"
	// HRK is the Croatian kuna.
	HRK Code = "HRK"
	// CHF is the Swiss franc.
	CHF Code = "CHF"
	// NOK is the Norwegian krone.
	NOK Code = "NOK"
	// SEK is the Swedish krona.
	SEK Code = "SEK"
	// DKK is the Danish krone.
	DKK Code = "DKK"
)

var pattern = regexp.MustCompile(`^[A-Z]{3}$`)

// FromString normalizes a currency code and returns it as a Code. The function
// trims surrounding whitespace and uppercases the value. Any well-formed code
// is accepted, so custom currencies work too. A malformed value returns an
// error.
func FromString(value string) (Code, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if !pattern.MatchString(normalized) {
		return Code(""), fmt.Errorf("currency: %q is not a valid currency code", value)
	}
	return Code(normalized), nil
}

// Valid reports whether the code is a well-formed currency code.
func (code Code) Valid() bool {
	return pattern.MatchString(string(code))
}

// String returns the wire value of the code.
func (code Code) String() string {
	return string(code)
}
