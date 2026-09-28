package carrier

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstantsUseWireValues(t *testing.T) {
	t.Parallel()

	codes := map[Code]string{
		PPL:        "ppl",
		DPD:        "dpd",
		DPDCZ:      "dpdcz",
		DPDSK:      "dpdsk",
		GEIS:       "geis",
		GLS:        "gls",
		INTIME:     "intime",
		CP:         "cp",
		CESKAPOSTA: "ceskaposta",
		BALIKOVNA:  "balikovna",
		ZASILKOVNA: "zasilkovna",
		SP:         "sp",
		ULOZENKA:   "ulozenka",
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
		"ppl":                   PPL,
		"  PPL\t":               PPL,
		"CustomCarrier9":        "customcarrier9",
		"ab":                    "ab",
		"a1":                    "a1",
		"12":                    "12",
		"lockers":               "lockers",
		strings.Repeat("a", 32): Code(strings.Repeat("a", 32)),
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
		code, err := FromString(input)
		if err == nil {
			t.Errorf("FromString(%q) accepted %q", input, code)
		}
		if code != Code("") {
			t.Errorf("FromString(%q) code = %q, want empty", input, code)
		}
		if !strings.Contains(err.Error(), "carrier") {
			t.Errorf("FromString(%q) error = %v, want carrier context", input, err)
		}
	}
}

func TestValidRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	if !PPL.Valid() || !Code("ab").Valid() || !Code("customcarrier9").Valid() {
		t.Error("well-formed codes reported as invalid")
	}
	for _, code := range []Code{"", "P", "PPL", " ab", "ab ", "a-b", "a b", "čp", Code(strings.Repeat("a", 33))} {
		if code.Valid() {
			t.Errorf("Code(%q).Valid() = true", code)
		}
	}
	if PPL.String() != "ppl" {
		t.Errorf("PPL.String() = %q", PPL.String())
	}
}

func TestJSONRoundTripUsesPlainStrings(t *testing.T) {
	t.Parallel()

	type record struct {
		Carrier Code `json:"carrier"`
	}
	custom, err := FromString("MyCustomCarrier1")
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
	encoded, err = json.Marshal(record{Carrier: ZASILKOVNA})
	if err != nil || string(encoded) != `{"carrier":"zasilkovna"}` {
		t.Fatalf("constant encoded = %s, %v", encoded, err)
	}
	var decoded record
	if err := json.Unmarshal([]byte(`{"carrier":"customcarrier9"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Carrier != Code("customcarrier9") {
		t.Fatalf("decoded = %q", decoded.Carrier)
	}
}
