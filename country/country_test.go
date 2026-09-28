package country

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[Code]string{
		AT: "AT", BE: "BE", BG: "BG", HR: "HR", CY: "CY", CZ: "CZ",
		DK: "DK", EE: "EE", FI: "FI", FR: "FR", DE: "DE", GR: "GR",
		HU: "HU", IE: "IE", IT: "IT", LV: "LV", LT: "LT", LU: "LU",
		MT: "MT", NL: "NL", PL: "PL", PT: "PT", RO: "RO", SK: "SK",
		SI: "SI", ES: "ES", SE: "SE", GB: "GB", CH: "CH", NO: "NO",
		IS: "IS", LI: "LI", UA: "UA", RS: "RS", BA: "BA", ME: "ME",
		MK: "MK", AL: "AL", TR: "TR", US: "US", CA: "CA",
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

	cases := map[string]Code{
		"CZ":     CZ,
		"  cz\t": CZ,
		"sk":     SK,
		"xk":     "XK",
		"zz":     "ZZ",
		" gb ":   GB,
		"de":     DE,
	}
	for input, want := range cases {
		got, err := FromString(input)
		if err != nil || got != want {
			t.Errorf("FromString(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestFromStringRejectsMalformedCodes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "C", "CZE", "C2", "12", "čr", "C Z", "c z"} {
		code, err := FromString(input)
		if err == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(err.Error(), "country") {
			t.Errorf("FromString(%q) error = %v, want country context", input, err)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !CZ.Valid() || !Code("XK").Valid() || !Code("ZZ").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []Code{"", "cz", "CZ ", " CZ", "CZE", "C2", "12"} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if CZ.String() != "CZ" {
		t.Errorf("CZ.String() = %q", CZ.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Country Code `json:"country"`
	}
	custom, err := FromString("xk")
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
	encoded, err = json.Marshal(record{Country: US})
	if err != nil || string(encoded) != `{"country":"US"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if err := json.Unmarshal([]byte(`{"country":"DE"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Country != DE {
		t.Fatalf("decoded = %q", decoded.Country)
	}
}
