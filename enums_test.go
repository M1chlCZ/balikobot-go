package balikobot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
	"github.com/m1chlcz/balikobot-go/currency"
)

func TestAddPackageAcceptsCustomEnumValuesFromFromString(t *testing.T) {
	t.Parallel()

	customCarrier, err := carrier.FromString(" MyCarrier99 ")
	if err != nil {
		t.Fatalf("carrier.FromString: %v", err)
	}
	customCountry, err := country.FromString("xk")
	if err != nil {
		t.Fatalf("country.FromString: %v", err)
	}
	customCurrency, err := currency.FromString("czk")
	if err != nil {
		t.Fatalf("currency.FromString: %v", err)
	}

	request := validTestAddRequest()
	request.RecCountry = customCountry
	request.CODCurrency = customCurrency

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/mycarrier99/add" || req.Method != http.MethodPost {
			t.Errorf("request = %s %s", req.Method, req.URL.Path)
		}
		var body struct {
			Packages []map[string]any `json:"packages"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil || len(body.Packages) != 1 {
			t.Errorf("add body = %#v", body)
		}
		entry := body.Packages[0]
		if entry["rec_country"] != "XK" || entry["cod_currency"] != "CZK" {
			t.Errorf("typed fields on the wire = %#v", entry)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{
			"status": 200,
			"packages": [{
				"eid": %q,
				"status": 200,
				"package_id": "custom-1",
				"carrier_id": "CUSTOM-1",
				"label_url": %q
			}]
		}`, request.EID, server.URL+"/label.pdf")
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	result, err := client.AddPackage(t.Context(), customCarrier, request)
	if err != nil {
		t.Fatalf("AddPackage: %v", err)
	}
	if result.PackageID != "custom-1" || result.CarrierID != "CUSTOM-1" {
		t.Fatalf("add result = %#v", result)
	}
}

func TestAddPackageKeepsTheCarrierCurrencyRule(t *testing.T) {
	t.Parallel()

	customCurrency, err := currency.FromString("jpy")
	if err != nil {
		t.Fatalf("currency.FromString: %v", err)
	}
	if !customCurrency.Valid() {
		t.Fatal("custom currency is not valid")
	}
	request := validTestAddRequest()
	request.CODCurrency = customCurrency
	if !errors.Is(validateAddPackage(request), ErrInvalidRequest) {
		t.Fatal("ADD accepted a currency outside the CZK and EUR rule")
	}
}

func TestMethodsRejectMalformedTypedValuesBeforeNetwork(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	request := validTestAddRequest()
	request.RecCountry = country.Code("cz")
	if _, err := client.AddPackage(t.Context(), carrier.PPL, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("lowercase country error = %v", err)
	}
	request = validTestAddRequest()
	request.CODCurrency = currency.Code("czk")
	if _, err := client.AddPackage(t.Context(), carrier.PPL, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("lowercase currency error = %v", err)
	}
	if _, err := client.AddPackage(
		t.Context(), carrier.Code("PPL"), validTestAddRequest(),
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("uppercase carrier error = %v", err)
	}
	if _, err := client.Branches(
		t.Context(), carrier.PPL, "1", country.Code("cz"),
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("lowercase branch country error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("malformed typed values reached the provider %d times", requests.Load())
	}
}

func TestEnumFieldsRoundTripAsPlainJSONStrings(t *testing.T) {
	t.Parallel()

	request := validTestAddRequest()
	request.RecCountry = country.Code("XK")
	request.CODCurrency = currency.Code("JPY")
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if wire["rec_country"] != "XK" || wire["cod_currency"] != "JPY" {
		t.Fatalf("typed fields = %#v and %#v", wire["rec_country"], wire["cod_currency"])
	}
	var decoded AddPackageRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	if decoded.RecCountry != country.Code("XK") || decoded.CODCurrency != currency.Code("JPY") {
		t.Fatalf("round-trip request = %#v", decoded)
	}

	var capability CODCapability
	if err := json.Unmarshal(
		[]byte(`{"Country":"DE","Currency":"EUR","MaxAmountMinor":149995}`), &capability,
	); err != nil {
		t.Fatalf("capability Unmarshal: %v", err)
	}
	if capability.Country != country.DE || capability.Currency != currency.EUR {
		t.Fatalf("capability = %#v", capability)
	}
	encoded, err = json.Marshal(capability)
	if err != nil || string(encoded) != `{"Country":"DE","Currency":"EUR","MaxAmountMinor":149995}` {
		t.Fatalf("capability encoded = %s, %v", encoded, err)
	}

	var branch Branch
	if err := json.Unmarshal([]byte(`{"Country":"CZ"}`), &branch); err != nil {
		t.Fatalf("branch Unmarshal: %v", err)
	}
	if branch.Country != country.CZ {
		t.Fatalf("branch country = %q", branch.Country)
	}

	var who WhoAmICarrier
	if err := json.Unmarshal([]byte(`{"Slug":"customcarrier","Name":"Custom"}`), &who); err != nil {
		t.Fatalf("whoami carrier Unmarshal: %v", err)
	}
	if who.Slug != carrier.Code("customcarrier") {
		t.Fatalf("whoami slug = %q", who.Slug)
	}
	encoded, err = json.Marshal(who)
	if err != nil || string(encoded) != `{"Slug":"customcarrier","Name":"Custom"}` {
		t.Fatalf("whoami carrier encoded = %s, %v", encoded, err)
	}
}
