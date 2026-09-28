package currency

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[Code]string{
		CZK: "CZK",
		EUR: "EUR",
		USD: "USD",
		GBP: "GBP",
		PLN: "PLN",
		HUF: "HUF",
		RON: "RON",
		BGN: "BGN",
		HRK: "HRK",
		CHF: "CHF",
		NOK: "NOK",
		SEK: "SEK",
		DKK: "DKK",
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
		"CZK":     CZK,
		"  czk\t": CZK,
		"Eur":     EUR,
		"jpy":     "JPY",
		"xts":     "XTS",
		" eur ":   EUR,
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

	for _, input := range []string{"", "C", "CZ", "CZKK", "C2K", "1ZK", "čkž", "C Z"} {
		code, err := FromString(input)
		if err == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(err.Error(), "currency") {
			t.Errorf("FromString(%q) error = %v, want currency context", input, err)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !CZK.Valid() || !Code("JPY").Valid() || !Code("XTS").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []Code{"", "czk", "CZK ", " CZK", "CZ", "CZKK", "C2K"} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if CZK.String() != "CZK" {
		t.Errorf("CZK.String() = %q", CZK.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Currency Code `json:"currency"`
	}
	custom, err := FromString("jpy")
	if err != nil {
		t.Fatalf("FromString: %v", err)
	}
	encoded, err := json.Marshal(record{Currency: custom})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"currency":"JPY"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	encoded, err = json.Marshal(record{Currency: EUR})
	if err != nil || string(encoded) != `{"currency":"EUR"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if err := json.Unmarshal([]byte(`{"currency":"CHF"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Currency != CHF {
		t.Fatalf("decoded = %q", decoded.Currency)
	}
}
