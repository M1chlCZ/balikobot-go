package currency_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/m1chlcz/balikobot-go/currency"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[currency.Code]string{
		currency.CZK: "CZK",
		currency.EUR: "EUR",
		currency.USD: "USD",
		currency.GBP: "GBP",
		currency.PLN: "PLN",
		currency.HUF: "HUF",
		currency.RON: "RON",
		currency.BGN: "BGN",
		currency.HRK: "HRK",
		currency.CHF: "CHF",
		currency.NOK: "NOK",
		currency.SEK: "SEK",
		currency.DKK: "DKK",
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

	cases := map[string]currency.Code{
		"CZK":     currency.CZK,
		"  czk\t": currency.CZK,
		"Eur":     currency.EUR,
		"jpy":     "JPY",
		"xts":     "XTS",
		" eur ":   currency.EUR,
	}
	for input, want := range cases {
		got, fromErr := currency.FromString(input)
		if fromErr != nil || got != want {
			t.Errorf("FromString(%q) = %q, %v; want %q", input, got, fromErr, want)
		}
	}
}

func TestFromStringRejectsMalformedCodes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "C", "CZ", "CZKK", "C2K", "1ZK", "čkž", "C Z"} {
		code, fromErr := currency.FromString(input)
		if fromErr == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != currency.Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(fromErr.Error(), "currency") {
			t.Errorf("FromString(%q) error = %v, want currency context", input, fromErr)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !currency.CZK.Valid() || !currency.Code("JPY").Valid() || !currency.Code("XTS").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []currency.Code{"", "czk", "CZK ", " CZK", "CZ", "CZKK", "C2K"} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if currency.CZK.String() != "CZK" {
		t.Errorf("CZK.String() = %q", currency.CZK.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Currency currency.Code `json:"currency"`
	}
	custom, err := currency.FromString("jpy")
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
	encoded, err = json.Marshal(record{Currency: currency.EUR})
	if err != nil || string(encoded) != `{"currency":"EUR"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if unmarshalErr := json.Unmarshal([]byte(`{"currency":"CHF"}`), &decoded); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if decoded.Currency != currency.CHF {
		t.Fatalf("decoded = %q", decoded.Currency)
	}
}
