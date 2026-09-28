package country_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/m1chlcz/balikobot-go/country"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[country.Code]string{
		country.AT: "AT", country.BE: "BE", country.BG: "BG", country.HR: "HR",
		country.CY: "CY", country.CZ: "CZ", country.DK: "DK", country.EE: "EE",
		country.FI: "FI", country.FR: "FR", country.DE: "DE", country.GR: "GR",
		country.HU: "HU", country.IE: "IE", country.IT: "IT", country.LV: "LV",
		country.LT: "LT", country.LU: "LU", country.MT: "MT", country.NL: "NL",
		country.PL: "PL", country.PT: "PT", country.RO: "RO", country.SK: "SK",
		country.SI: "SI", country.ES: "ES", country.SE: "SE", country.GB: "GB",
		country.CH: "CH", country.NO: "NO", country.IS: "IS", country.LI: "LI",
		country.UA: "UA", country.RS: "RS", country.BA: "BA", country.ME: "ME",
		country.MK: "MK", country.AL: "AL", country.TR: "TR", country.US: "US",
		country.CA: "CA",
	}
	if len(codes) != 41 {
		t.Fatalf("constant count = %d, want 41", len(codes))
	}
	for code, want := range codes {
		if string(code) != want {
			t.Errorf("constant value = %q, want %q", code, want)
		}
		if !code.Valid() {
			t.Errorf("%q is not valid", code)
		}
		if code.String() != want {
			t.Errorf("String() = %q, want %q", code.String(), want)
		}
	}
}

func TestFromStringNormalizesAndAcceptsCustomCodes(t *testing.T) {
	t.Parallel()

	cases := map[string]country.Code{
		"CZ":     country.CZ,
		"  cz\t": country.CZ,
		"sk":     country.SK,
		"xk":     "XK",
		"zz":     "ZZ",
		" gb ":   country.GB,
		"de":     country.DE,
	}
	for input, want := range cases {
		got, fromErr := country.FromString(input)
		if fromErr != nil || got != want {
			t.Errorf("FromString(%q) = %q, %v; want %q", input, got, fromErr, want)
		}
	}
}

func TestFromStringRejectsMalformedCodes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "C", "CZE", "C2", "12", "čr", "C Z", "c z"} {
		code, fromErr := country.FromString(input)
		if fromErr == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != country.Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(fromErr.Error(), "country") {
			t.Errorf("FromString(%q) error = %v, want country context", input, fromErr)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !country.CZ.Valid() || !country.Code("XK").Valid() || !country.Code("ZZ").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []country.Code{"", "cz", "CZ ", " CZ", "CZE", "C2", "12"} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if country.CZ.String() != "CZ" {
		t.Errorf("CZ.String() = %q", country.CZ.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Country country.Code `json:"country"`
	}
	custom, err := country.FromString("xk")
	if err != nil {
		t.Fatalf("FromString: %v", err)
	}
	encoded, err := json.Marshal(record{Country: custom})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"country":"XK"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	encoded, err = json.Marshal(record{Country: country.US})
	if err != nil || string(encoded) != `{"country":"US"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if unmarshalErr := json.Unmarshal([]byte(`{"country":"DE"}`), &decoded); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if decoded.Country != country.DE {
		t.Fatalf("decoded = %q", decoded.Country)
	}
}
