package carrier_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/m1chlcz/balikobot-go/carrier"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[carrier.Code]string{
		carrier.PPL:        "ppl",
		carrier.DPD:        "dpd",
		carrier.DPDCZ:      "dpdcz",
		carrier.DPDSK:      "dpdsk",
		carrier.GEIS:       "geis",
		carrier.GLS:        "gls",
		carrier.INTIME:     "intime",
		carrier.CP:         "cp",
		carrier.CESKAPOSTA: "ceskaposta",
		carrier.BALIKOVNA:  "balikovna",
		carrier.ZASILKOVNA: "zasilkovna",
		carrier.SP:         "sp",
		carrier.ULOZENKA:   "ulozenka",
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

	cases := map[string]carrier.Code{
		"ppl":                   carrier.PPL,
		"  PPL\t":               carrier.PPL,
		"CustomCarrier9":        "customcarrier9",
		"ab":                    "ab",
		"a1":                    "a1",
		"12":                    "12",
		"lockers":               "lockers",
		strings.Repeat("a", 32): carrier.Code(strings.Repeat("a", 32)),
	}
	for input, want := range cases {
		got, fromErr := carrier.FromString(input)
		if fromErr != nil || got != want {
			t.Errorf("FromString(%q) = %q, %v; want %q", input, got, fromErr, want)
		}
	}
}

func TestFromStringRejectsMalformedCodes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"",
		"a",
		"-",
		"a b",
		"a/b",
		"PPL!",
		"žluťoučký",
		strings.Repeat("a", 33),
	} {
		code, fromErr := carrier.FromString(input)
		if fromErr == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != carrier.Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(fromErr.Error(), "carrier") {
			t.Errorf("FromString(%q) error = %v, want carrier context", input, fromErr)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !carrier.PPL.Valid() || !carrier.Code("ab").Valid() || !carrier.Code("customcarrier9").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []carrier.Code{
		"", "P", "PPL", " ab", "ab ", "a-b", "a b", "čp", carrier.Code(strings.Repeat("a", 33)),
	} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if carrier.PPL.String() != "ppl" {
		t.Errorf("PPL.String() = %q", carrier.PPL.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Carrier carrier.Code `json:"carrier"`
	}
	custom, err := carrier.FromString("MyCustomCarrier1")
	if err != nil {
		t.Fatalf("FromString: %v", err)
	}
	encoded, err := json.Marshal(record{Carrier: custom})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"carrier":"mycustomcarrier1"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	encoded, err = json.Marshal(record{Carrier: carrier.ZASILKOVNA})
	if err != nil || string(encoded) != `{"carrier":"zasilkovna"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if unmarshalErr := json.Unmarshal([]byte(`{"carrier":"customcarrier9"}`), &decoded); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if decoded.Carrier != carrier.Code("customcarrier9") {
		t.Fatalf("decoded = %q", decoded.Carrier)
	}
}
