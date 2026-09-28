// Package carrier defines the carrier codes used by the Balíkobot API.
//
// Code is a string type, so a carrier code stays a plain JSON string on the
// wire. Use the constants for common carriers, or FromString for any other
// well-formed code. The type makes carrier mistakes visible at compile time.
package carrier

import (
	"fmt"
	"regexp"
	"strings"
)

// Code is a carrier code used in Balíkobot request paths.
type Code string

// Common carrier codes accepted by the Balíkobot API.
const (
	// PPL is the PPL code.
	PPL Code = "ppl"
	// DPD is the DPD code.
	DPD Code = "dpd"
	// DPDCZ is the DPD Czech Republic code.
	DPDCZ Code = "dpdcz"
	// DPDSK is the DPD Slovakia code.
	DPDSK Code = "dpdsk"
	// GEIS is the Geis code.
	GEIS Code = "geis"
	// GLS is the GLS code.
	GLS Code = "gls"
	// INTIME is the InTime code.
	INTIME Code = "intime"
	// CP is the Česká pošta code.
	CP Code = "cp"
	// CESKAPOSTA is the alternative Česká pošta code.
	CESKAPOSTA Code = "ceskaposta"
	// BALIKOVNA is the Balíkovna code.
	BALIKOVNA Code = "balikovna"
	// ZASILKOVNA is the Zásilkovna code.
	ZASILKOVNA Code = "zasilkovna"
	// SP is the Slovenská pošta code.
	SP Code = "sp"
	// ULOZENKA is the Uloženka code.
	ULOZENKA Code = "ulozenka"
)

var pattern = regexp.MustCompile(`^[a-z0-9]{2,32}$`)

// FromString normalizes a carrier code and returns it as a Code. The function
// trims surrounding whitespace and lowercases the value. Any well-formed code
// is accepted, so custom carriers work too. A malformed value returns an
// error.
func FromString(value string) (Code, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !pattern.MatchString(normalized) {
		return Code(""), fmt.Errorf("carrier: %q is not a valid carrier code", value)
	}
	return Code(normalized), nil
}

// Valid reports whether the code is a well-formed carrier code.
func (code Code) Valid() bool {
	return pattern.MatchString(string(code))
}

// String returns the wire value of the code.
func (code Code) String() string {
	return string(code)
}
