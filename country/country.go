// Package country defines ISO 3166-1 alpha-2 country codes used by the
// Balíkobot API. The constants cover the European Union member states and the
// common nearby destinations.
//
// Code is a string type, so a country code stays a plain JSON string on the
// wire. Use the constants for common destinations, or FromString for any other
// well-formed code. The type makes country mistakes visible at compile time.
package country

import (
	"fmt"
	"regexp"
	"strings"
)

// Code is an ISO 3166-1 alpha-2 country code.
type Code string

// European Union member states and common nearby destinations.
const (
	// AT is Austria.
	AT Code = "AT"
	// BE is Belgium.
	BE Code = "BE"
	// BG is Bulgaria.
	BG Code = "BG"
	// HR is Croatia.
	HR Code = "HR"
	// CY is Cyprus.
	CY Code = "CY"
	// CZ is Czechia.
	CZ Code = "CZ"
	// DK is Denmark.
	DK Code = "DK"
	// EE is Estonia.
	EE Code = "EE"
	// FI is Finland.
	FI Code = "FI"
	// FR is France.
	FR Code = "FR"
	// DE is Germany.
	DE Code = "DE"
	// GR is Greece.
	GR Code = "GR"
	// HU is Hungary.
	HU Code = "HU"
	// IE is Ireland.
	IE Code = "IE"
	// IT is Italy.
	IT Code = "IT"
	// LV is Latvia.
	LV Code = "LV"
	// LT is Lithuania.
	LT Code = "LT"
	// LU is Luxembourg.
	LU Code = "LU"
	// MT is Malta.
	MT Code = "MT"
	// NL is the Netherlands.
	NL Code = "NL"
	// PL is Poland.
	PL Code = "PL"
	// PT is Portugal.
	PT Code = "PT"
	// RO is Romania.
	RO Code = "RO"
	// SK is Slovakia.
	SK Code = "SK"
	// SI is Slovenia.
	SI Code = "SI"
	// ES is Spain.
	ES Code = "ES"
	// SE is Sweden.
	SE Code = "SE"
	// GB is the United Kingdom.
	GB Code = "GB"
	// CH is Switzerland.
	CH Code = "CH"
	// NO is Norway.
	NO Code = "NO"
	// IS is Iceland.
	IS Code = "IS"
	// LI is Liechtenstein.
	LI Code = "LI"
	// UA is Ukraine.
	UA Code = "UA"
	// RS is Serbia.
	RS Code = "RS"
	// BA is Bosnia and Herzegovina.
	BA Code = "BA"
	// ME is Montenegro.
	ME Code = "ME"
	// MK is North Macedonia.
	MK Code = "MK"
	// AL is Albania.
	AL Code = "AL"
	// TR is Türkiye.
	TR Code = "TR"
	// US is the United States.
	US Code = "US"
	// CA is Canada.
	CA Code = "CA"
)

var pattern = regexp.MustCompile(`^[A-Z]{2}$`)

// FromString normalizes a country code and returns it as a Code. The function
// trims surrounding whitespace and uppercases the value. Any well-formed code
// is accepted, so custom countries work too. A malformed value returns an
// error.
func FromString(value string) (Code, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if !pattern.MatchString(normalized) {
		return Code(""), fmt.Errorf("country: %q is not a valid country code", value)
	}
	return Code(normalized), nil
}

// Valid reports whether the code is a well-formed country code.
func (code Code) Valid() bool {
	return pattern.MatchString(string(code))
}

// String returns the wire value of the code.
func (code Code) String() string {
	return string(code)
}
