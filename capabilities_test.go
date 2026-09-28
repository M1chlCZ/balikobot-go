package balikobot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func findCarrier(carriers []Carrier, code string) *Carrier {
	for index := range carriers {
		if carriers[index].CarrierCode == code {
			return &carriers[index]
		}
	}
	return nil
}

func findService(services []Service, code string) *Service {
	for index := range services {
		if services[index].Code == code {
			return &services[index]
		}
	}
	return nil
}

func TestWhoAmIUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/info/whoami" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"live_account": true,
			"account": {"email": "private@example.test"},
			"carriers": [{"slug": "ppl", "name": "PPL"}, {"slug": "gls"}]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	whoami, err := client.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if whoami.Status != 200 || whoami.LiveAccount == nil || !*whoami.LiveAccount {
		t.Fatalf("whoami = %#v", whoami)
	}
	if len(whoami.Carriers) != 2 || whoami.Carriers[0].Slug != "ppl" ||
		whoami.Carriers[0].Name != "PPL" || whoami.Carriers[1].Slug != "gls" {
		t.Fatalf("carriers = %#v", whoami.Carriers)
	}
	encoded, marshalErr := json.Marshal(whoami)
	if marshalErr != nil || strings.Contains(string(encoded), "private@example.test") {
		t.Fatalf("whoami leaks account data: %s (%v)", encoded, marshalErr)
	}
}

func TestActivatedServicesNormalizesAndHonorsActiveParcel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/activatedservices" {
			t.Errorf("path = %q", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"active_parcel": true,
			"service_types": [
				{"service_type": "80", "name": " Balíkovna ", "pickup_points_delivery": true},
				{"service_type": 80, "name": "Balíkovna", "pickup_points_delivery": true}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	activated, err := client.ActivatedServices(context.Background(), "ppl")
	if err != nil {
		t.Fatalf("ActivatedServices: %v", err)
	}
	if activated.ActiveParcel == nil || !*activated.ActiveParcel {
		t.Fatalf("active parcel = %#v", activated.ActiveParcel)
	}
	if len(activated.Services) != 1 || activated.Services[0].Code != "80" ||
		activated.Services[0].Name != "Balíkovna" ||
		activated.Services[0].PickupPointsDelivery == nil ||
		!*activated.Services[0].PickupPointsDelivery {
		t.Fatalf("services = %#v", activated.Services)
	}

	inactive := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"active_parcel": false,
			"service_types": [{"service_type": "80", "name": "Balíkovna"}]
		}`))
	}))
	t.Cleanup(inactive.Close)
	inactiveClient := newTestClient(t, inactive)
	activated, err = inactiveClient.ActivatedServices(context.Background(), "ppl")
	if err != nil || len(activated.Services) != 0 ||
		activated.ActiveParcel == nil || *activated.ActiveParcel {
		t.Fatalf("inactive services = %#v, %v", activated, err)
	}
}

func TestCountriesNormalizesCodes(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"service_types": [
				{"service_type": "VMCZ", "countries": [" cz ", "SK"]},
				{"service_type": 6830, "countries": ["AT"]}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	services, err := client.Countries(context.Background(), "zasilkovna")
	if err != nil {
		t.Fatalf("Countries: %v", err)
	}
	if len(services) != 2 ||
		services[0].ServiceType != "VMCZ" || services[0].Countries[0] != "CZ" ||
		services[1].ServiceType != "6830" || services[1].Countries[0] != "AT" {
		t.Fatalf("countries = %#v", services)
	}
}

func TestCODNormalizesCountriesAndToleratesUnsupported(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"service_types": [{
				"service_type": "VMCZ",
				"countries": [
					{"country": " cz ", "currency": "czk", "max_price": 1499.95},
					{"country": "US", "currency": "USD", "max_price": 100}
				]
			}]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	services, err := client.COD(context.Background(), "zasilkovna")
	if err != nil {
		t.Fatalf("COD: %v", err)
	}
	if len(services) != 1 || services[0].ServiceType != "VMCZ" ||
		len(services[0].Countries) != 2 {
		t.Fatalf("cod = %#v", services)
	}
	first := services[0].Countries[0]
	if first.Country != "CZ" || first.Currency != "CZK" || first.MaxAmountMinor != 149995 {
		t.Fatalf("cod entry = %#v", first)
	}

	unsupported := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotImplemented)
	}))
	t.Cleanup(unsupported.Close)
	unsupportedClient := newTestClient(t, unsupported)
	services, err = unsupportedClient.COD(context.Background(), "zasilkovna")
	if err != nil || len(services) != 0 {
		t.Fatalf("unsupported cod = %#v, %v", services, err)
	}
}

func TestCarrierCapabilitiesDiscoversEveryWhoAmIContractedCarrier(t *testing.T) {
	t.Parallel()

	var paths []string
	server := newCapabilityFixtureServer(t, &paths)
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	carriers, err := client.CarrierCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CarrierCapabilities: %v", err)
	}
	if len(carriers) != 2 {
		t.Fatalf("carriers = %#v", carriers)
	}
	if len(paths) != 5 || paths[0] != "/info/whoami" {
		t.Fatalf("discovery paths = %#v", paths)
	}
	ppl := findCarrier(carriers, "ppl")
	if ppl == nil {
		t.Fatalf("ppl carrier missing: %#v", carriers)
	}
	if len(ppl.Services) != 2 {
		t.Fatalf("ppl snapshot = %#v", ppl)
	}
	service := findService(ppl.Services, "LONG_SERVICE_CODE_123456789")
	if service == nil {
		t.Fatalf("long service missing: %#v", ppl.Services)
	}
	if service.Code != "LONG_SERVICE_CODE_123456789" || service.Name != "PPL Home" ||
		service.HomeDelivery == nil || !*service.HomeDelivery {
		t.Fatalf("service = %#v", service)
	}
	if service.Countries["CZ"] != true || service.Countries["DE"] != true || service.Countries["US"] != false {
		t.Fatalf("countries = %#v", service.Countries)
	}
	if len(service.COD) != 0 {
		t.Fatalf("prepaid discovery loaded COD: %#v", service.COD)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "/cod4services") {
			t.Fatalf("prepaid discovery requested COD: %s", path)
		}
	}
	if encoded, marshalErr := json.Marshal(carriers); marshalErr != nil ||
		strings.Contains(string(encoded), "private@example.test") {
		t.Fatalf("capabilities leak WHOAMI account data: %s (%v)", encoded, marshalErr)
	}
}

func newCapabilityFixtureServer(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		*paths = append(*paths, request.URL.Path)
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-user:provider-secret"))
		authValid := request.Header.Get("Authorization") == wantAuth
		acceptValid := request.Header.Get("Accept") == "application/json"
		if !authValid {
			t.Errorf("authorization header valid = %t", authValid)
		}
		if !acceptValid {
			t.Errorf("accept header valid = %t", acceptValid)
		}
		response.Header().Set("Content-Type", "application/json")
		body, ok := capabilityFixtureBody(request.URL.Path)
		if !ok {
			t.Errorf("unexpected discovery path = %s", request.URL.Path)
			body = `{"status":500}`
		}
		_, _ = response.Write([]byte(body))
	}))
}

func capabilityFixtureBody(path string) (string, bool) {
	bodies := map[string]string{
		"/info/whoami":           `{"status":200,"live_account":false,"account":{"email":"private@example.test"},"carriers":[{"slug":"ppl","name":"PPL"},{"slug":"gls","name":"GLS"},{"slug":"ppl","name":"PPL"}]}`,
		"/ppl/activatedservices": `{"status":200,"service_types":[{"service_type":"LONG_SERVICE_CODE_123456789","name":"PPL Home","home_delivery":true},{"service_type":"NO_BOX","name":"No box","box_delivery":false}]}`,
		"/ppl/countries4service": `{"status":200,"service_types":[{"service_type":"LONG_SERVICE_CODE_123456789","countries":["CZ","DE","US"]},{"service_type":"NO_BOX","countries":["CZ"]}]}`,
		"/ppl/cod4services":      `{"status":200,"service_types":[{"service_type":"LONG_SERVICE_CODE_123456789","countries":[{"country":"CZ","currency":"CZK","max_price":1499.95},{"country":"CZ","currency":"EUR","max_price":100.01}]}]}`,
		"/gls/activatedservices": `{"status":200,"service_types":[]}`,
		"/gls/countries4service": `{"status":200,"service_types":[]}`,
		"/gls/cod4services":      `{"status":200,"service_types":[]}`,
	}
	body, ok := bodies[path]
	return body, ok
}

func TestCarrierCapabilitiesPreservesUnknownOptionalDeliveryFlags(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		body := map[string]string{
			"/info/whoami":           `{"status":200,"carriers":[{"slug":"ppl"}]}`,
			"/ppl/activatedservices": `{"status":200,"service_types":[{"service_type":"HOME_ONLY","name":"Home","home_delivery":true},{"service_type":"UNKNOWN_FLAGS","name":"Unknown"},{"service_type":"FALSE_FLAGS","name":"False","home_delivery":false,"box_delivery":false,"pickup_points_delivery":false}]}`,
			"/ppl/countries4service": `{"status":200,"service_types":[{"service_type":"HOME_ONLY","countries":["CZ"]},{"service_type":"UNKNOWN_FLAGS","countries":["CZ"]},{"service_type":"FALSE_FLAGS","countries":["CZ"]}]}`,
			"/ppl/cod4services":      `{"status":200,"service_types":[]}`,
		}
		_, _ = response.Write([]byte(body[request.URL.Path]))
	}))
	t.Cleanup(server.Close)
	carriers, err := newTestClient(t, server).CarrierCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CarrierCapabilities: %v", err)
	}
	services := carriers[0].Services
	unknown := findService(services, "UNKNOWN_FLAGS")
	if unknown == nil || unknown.HomeDelivery != nil || unknown.BoxDelivery != nil ||
		unknown.PickupPointsDelivery != nil {
		t.Fatalf("omitted flags were not preserved as unknown: %#v", unknown)
	}
	falseFlags := findService(services, "FALSE_FLAGS")
	if falseFlags == nil || falseFlags.HomeDelivery == nil || *falseFlags.HomeDelivery ||
		falseFlags.BoxDelivery == nil || *falseFlags.BoxDelivery {
		t.Fatalf("explicit false flags were not preserved: %#v", falseFlags)
	}
}

func TestCarrierCapabilitiesFailsWithoutPartialSnapshotOnCarrierError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/info/whoami" {
			_, _ = response.Write([]byte(`{"status":200,"carriers":[{"slug":"ppl"},{"slug":"gls"}]}`))
			return
		}
		if strings.HasPrefix(request.URL.Path, "/ppl/") {
			_, _ = response.Write([]byte(`{"status":200,"service_types":[]}`))
			return
		}
		_, _ = response.Write([]byte(`{"status":503,"message":"carrier unavailable"}`))
	}))
	t.Cleanup(server.Close)
	carriers, err := newTestClient(t, server).CarrierCapabilities(context.Background())
	if err == nil {
		t.Fatal("CarrierCapabilities unexpectedly accepted a partial discovery")
	}
	if len(carriers) != 0 {
		t.Fatalf("partial snapshot returned on error: %#v", carriers)
	}
}

func TestCarrierCapabilitiesScopesDictionariesToConfiguredCarriers(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name         string
		scope        [][]string
		wantCarriers int
		wantRequests int32
		wantErr      error
	}{
		{"used carrier only", [][]string{{" PPL ", "ppl"}}, 1, 3, nil},
		{"empty scope", [][]string{{}}, 0, 1, nil},
		{"nil explicit scope", [][]string{nil}, 0, 1, nil},
		{"missing from account", [][]string{{"dpd"}}, 0, 1, ErrInvalidResponse},
		{"invalid scope", [][]string{{"ppl/lockers"}}, 0, 1, ErrInvalidResponse},
		{"used carrier unavailable", [][]string{{"lockers"}}, 0, 3, ErrUnavailable},
		{"unscoped discovers all", nil, 0, 5, ErrUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/info/whoami":
					_, _ = response.Write([]byte(`{"status":200,"carriers":[{"slug":"ppl"},{"slug":"lockers"}]}`))
				case "/lockers/countries4service":
					response.WriteHeader(http.StatusInternalServerError)
				default:
					_, _ = response.Write([]byte(`{"status":200,"service_types":[]}`))
				}
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			carriers, err := client.CarrierCapabilities(t.Context(), testCase.scope...)
			if !errors.Is(err, testCase.wantErr) || len(carriers) != testCase.wantCarriers {
				t.Fatalf("scoped capabilities = %#v, %v; want %d carriers, %v",
					carriers, err, testCase.wantCarriers, testCase.wantErr)
			}
			if requests.Load() != testCase.wantRequests {
				t.Fatalf("provider requests = %d, want %d", requests.Load(), testCase.wantRequests)
			}
		})
	}
}

func TestCapabilityPriceRejectsUnsafeExponentAndFractionalMinorUnits(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`1e999999999`, `1e-999999999`, `1.001`, `-1`, `NaN`} {
		if value, ok := majorPriceToMinor(json.RawMessage(raw)); ok {
			t.Fatalf("majorPriceToMinor(%s) = %d, want rejection", raw, value)
		}
	}
	if value, ok := majorPriceToMinor(json.RawMessage(`1499.95`)); !ok || value != 149995 {
		t.Fatalf("majorPriceToMinor(1499.95) = %d, %t", value, ok)
	}
}

func TestNormalizeCapabilitiesRejectsConflictingCODEntries(t *testing.T) {
	t.Parallel()

	active := true
	code := serviceCode{value: "SERVICE", set: true}
	_, err := normalizeCapabilities(
		activatedServicesCapabilityResponse{
			ServiceTypes: []activatedServiceWire{{Code: code, Name: "Service", HomeDelivery: &active}},
		},
		countriesCapabilityResponse{
			ServiceTypes: []countriesServiceWire{{Code: code, Countries: []string{"CZ"}}},
		},
		codCapabilityResponse{
			ServiceTypes: []codServiceWire{{Code: code, Countries: []codCountryWire{
				{Country: "CZ", Currency: "CZK", MaxPrice: json.RawMessage(`100`)},
				{Country: "CZ", Currency: "CZK", MaxPrice: json.RawMessage(`101`)},
			}}},
		},
	)
	if err == nil {
		t.Fatal("conflicting COD entries were accepted")
	}
}

func TestServiceCodeAcceptsStringsAndNumbers(t *testing.T) {
	t.Parallel()

	var response countriesCapabilityResponse
	if err := json.Unmarshal([]byte(`{
		"status": 200,
		"service_types": [
			{"service_type": "VMCZ", "countries": ["CZ"]},
			{"service_type": 6830, "countries": ["AT"]}
		]
	}`), &response); err != nil {
		t.Fatalf("countries4service decode: %v", err)
	}
	if len(response.ServiceTypes) != 2 ||
		response.ServiceTypes[0].Code.value != "VMCZ" ||
		response.ServiceTypes[1].Code.value != "6830" {
		t.Fatalf("service codes = %#v", response.ServiceTypes)
	}
	var activated activatedServicesCapabilityResponse
	if err := json.Unmarshal([]byte(`{
		"status": 200,
		"service_types": [{"service_type": "80", "name": "AT Rakouská pošta HD"}]
	}`), &activated); err != nil {
		t.Fatalf("activatedservices decode: %v", err)
	}
	if len(activated.ServiceTypes) != 1 || activated.ServiceTypes[0].Code.value != "80" {
		t.Fatalf("activated service codes = %#v", activated.ServiceTypes)
	}
}

func TestCarrierCapabilitiesSkipsUnusedCODDictionary(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/info/whoami":
			_, _ = response.Write([]byte(`{"status":200,"carriers":[{"slug":"zasilkovna"}]}`))
		case "/zasilkovna/activatedservices":
			_, _ = response.Write(
				[]byte(
					`{"status":200,"active_parcel":true,"service_types":[{"service_type":"VMCZ","name":"Výdejní místa","pickup_points_delivery":true}]}`,
				),
			)
		case "/zasilkovna/countries4service":
			_, _ = response.Write(
				[]byte(
					`{"status":200,"service_types":[{"service_type":"VMCZ","countries":["CZ"]},{"service_type":106,"countries":["CZ"]}]}`,
				),
			)
		case "/zasilkovna/cod4services":
			t.Error("prepaid discovery requested COD")
			_, _ = response.Write(
				[]byte(`{"status":501,"message":"Tato metoda není u dopravce podporována.","service_types":[]}`),
			)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	carriers, err := client.CarrierCapabilities(context.Background())
	if err != nil {
		t.Fatalf("CarrierCapabilities: %v", err)
	}
	carrier := findCarrier(carriers, "zasilkovna")
	if carrier == nil || len(carrier.Services) != 1 ||
		carrier.Services[0].Code != "VMCZ" || !carrier.Services[0].Countries["CZ"] ||
		len(carrier.Services[0].COD) != 0 {
		t.Fatalf("snapshot = %#v", carrier)
	}
}

func TestCapabilityGETAllowsOnlyOptionalHTTP501(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name             string
		status           int
		allowUnsupported bool
		wantAccept       bool
	}{
		{"optional unsupported", http.StatusNotImplemented, true, true},
		{"required unsupported", http.StatusNotImplemented, false, false},
		{"optional unavailable", http.StatusServiceUnavailable, true, false},
		{"optional rate limited", http.StatusTooManyRequests, true, false},
		{"optional unauthorized", http.StatusUnauthorized, true, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte("unsupported"))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			var result codCapabilityResponse
			err := client.capabilityGET(t.Context(), "/messenger/cod4services", &result, testCase.allowUnsupported)
			if (err == nil) != testCase.wantAccept {
				t.Fatalf("capabilityGET error = %v, want accepted %t", err, testCase.wantAccept)
			}
		})
	}
}

func TestCarrierCapabilitiesDoesNotRequestCOD(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/info/whoami":
			_, _ = response.Write([]byte(`{"status":200,"carriers":[{"slug":"messenger"},{"slug":"ppl"}]}`))
		case "/messenger/cod4services":
			t.Error("unused COD dictionary requested")
			response.WriteHeader(http.StatusNotImplemented)
			_, _ = response.Write([]byte(`{"status":501}`))
		default:
			_, _ = response.Write([]byte(`{"status":200,"service_types":[]}`))
		}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	carriers, err := client.CarrierCapabilities(t.Context())
	if err != nil || len(carriers) != 2 || carriers[1].CarrierCode != CarrierPPL {
		t.Fatalf("CarrierCapabilities = %#v, %v", carriers, err)
	}
}

func TestCapabilityWireNormalizesCountries(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"legacy array": `[{"service_type":"4","countries":["CZ","RO","US"]}]`,
		"sparse object": `{
			"2":{"service_type":3,"countries":["SK"]},
			"7":{"service_type":4,"countries":["CZ","RO","US"]}}`,
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			services, err := decodeWireCountries(encoded)
			if err != nil {
				t.Fatalf("decode capabilities: %v", err)
			}
			if len(services) != 1 || services[0].Code != "4" ||
				!services[0].Countries["CZ"] || !services[0].Countries["RO"] ||
				services[0].Countries["US"] || services[0].Countries["SK"] {
				t.Fatalf("delivery capabilities = %#v", services)
			}
		})
	}
}

func TestCapabilityWireRejectsMalformedCountries(t *testing.T) {
	t.Parallel()

	entries := make([]string, 0, capabilityServiceLimit+1)
	for index := range capabilityServiceLimit + 1 {
		entries = append(entries, fmt.Sprintf(`"%d":{"service_type":4,"countries":["CZ"]}`, index))
	}
	cases := map[string]string{
		"non-numeric key":    `{"invalid":{"service_type":4,"countries":["CZ"]}}`,
		"negative key":       `{"-1":{"service_type":4,"countries":["CZ"]}}`,
		"missing code":       `{"4":{"countries":["CZ"]}}`,
		"missing array code": `[{"countries":["CZ"]}]`,
		"null entry":         `{"4":null}`,
		"wrong countries":    `{"4":{"service_type":4,"countries":"CZ"}}`,
		"wrong collection":   `true`,
		"too many entries":   "{" + strings.Join(entries, ",") + "}",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeWireCountries(encoded); err == nil {
				t.Fatal("invalid countries services accepted")
			}
		})
	}
}

func decodeWireCountries(encoded string) ([]Service, error) {
	var countries countriesCapabilityResponse
	if err := json.Unmarshal([]byte(`{"status":200,"service_types":`+encoded+`}`), &countries); err != nil {
		return nil, err
	}
	if !countries.Status.set || countries.Status.value != 200 {
		return nil, ErrInvalidResponse
	}
	activated := activatedServicesCapabilityResponse{
		ServiceTypes: []activatedServiceWire{{
			Code: serviceCode{value: "4", set: true}, Name: "Delivery",
		}},
	}
	return normalizeCapabilities(activated, countries, codCapabilityResponse{})
}

const (
	testAccountBody = `{"status":200,"live_account":false,"carriers":[]}`
	liveAccountBody = `{"status":200,"live_account":true,"carriers":[]}`
)

type accountFixture struct {
	status      atomic.Int32
	body        atomic.Value
	whoamiCalls atomic.Int32
	writes      atomic.Int32
}

type accountFailingTransport struct{}

func (accountFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, ErrUnavailable
}

func newAccountFixture(t *testing.T, liveAccount bool) (*Client, *accountFixture) {
	t.Helper()
	fixture := &accountFixture{}
	fixture.status.Store(http.StatusOK)
	fixture.body.Store(testAccountBody)
	server := httptest.NewTestServer(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/info/whoami" {
			fixture.whoamiCalls.Add(1)
			response.WriteHeader(int(fixture.status.Load()))
			_, _ = response.Write([]byte(fixture.body.Load().(string)))
			return
		}
		fixture.writes.Add(1)
		_, _ = response.Write([]byte(`{"status":200}`))
	}))
	baseURL := server.URL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:1"
	}
	client, err := New(Config{
		BaseURL:     baseURL,
		User:        "api-user",
		APIKey:      "provider-secret",
		HTTPClient:  server.Client(),
		LiveAccount: boolPointer(liveAccount),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.Close)
	return client, fixture
}

func validAccountPickupRequest() PickupRequest {
	return PickupRequest{Date: "2026-09-20", WeightKG: 1, PackageCount: 1}
}

func assertAccountPickupAccepted(t *testing.T, client *Client) {
	t.Helper()
	if _, err := client.OrderPickup(t.Context(), CarrierDPDCZ, validAccountPickupRequest()); err != nil {
		t.Fatalf("OrderPickup: %v", err)
	}
}

func TestAccountModeGuardsWritesBeforeSending(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		liveAccount bool
		status      int
		body        string
		wantAccept  bool
	}{
		{"test account", false, http.StatusOK, testAccountBody, true},
		{"live account", true, http.StatusOK, liveAccountBody, true},
		{"live key in test mode", false, http.StatusOK, liveAccountBody, false},
		{"test key in live mode", true, http.StatusOK, testAccountBody, false},
		{"missing account mode", false, http.StatusOK, `{"status":200}`, false},
		{"null account mode", false, http.StatusOK, `{"status":200,"live_account":null}`, false},
		{"string account mode", false, http.StatusOK, `{"status":200,"live_account":"false"}`, false},
		{"missing success status", false, http.StatusOK, `{"live_account":false}`, false},
		{"rejected account", false, http.StatusUnauthorized, testAccountBody, false},
		{"unavailable account", false, http.StatusServiceUnavailable, testAccountBody, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client, fixture := newAccountFixture(t, testCase.liveAccount)
			fixture.status.Store(int32(testCase.status))
			fixture.body.Store(testCase.body)
			_, err := client.OrderPickup(t.Context(), CarrierDPDCZ, validAccountPickupRequest())
			if (err == nil) != testCase.wantAccept {
				t.Fatalf("OrderPickup error = %v, want accepted %t", err, testCase.wantAccept)
			}
			wantWrites := int32(0)
			if testCase.wantAccept {
				wantWrites = 1
			} else if !errors.Is(err, ErrRejected) {
				t.Fatalf("unsent pickup error = %v, want definitive rejection", err)
			}
			if fixture.writes.Load() != wantWrites || fixture.whoamiCalls.Load() != 1 {
				t.Fatalf("provider calls: WHOAMI %d, writes %d; want 1, %d",
					fixture.whoamiCalls.Load(), fixture.writes.Load(), wantWrites)
			}
		})
	}
}

func TestAccountModeCachesSuccessAcrossConcurrentWrites(t *testing.T) {
	t.Parallel()

	client, fixture := newAccountFixture(t, false)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			if _, err := client.OrderPickup(
				t.Context(), CarrierDPDCZ, validAccountPickupRequest(),
			); err != nil {
				t.Errorf("OrderPickup: %v", err)
			}
		})
	}
	workers.Wait()
	if fixture.whoamiCalls.Load() != 1 || fixture.writes.Load() != 12 {
		t.Fatalf("provider calls: WHOAMI %d, writes %d; want 1, 12",
			fixture.whoamiCalls.Load(), fixture.writes.Load())
	}
}

func TestAccountModeRechecksExpiredApproval(t *testing.T) {
	t.Parallel()

	client, fixture := newAccountFixture(t, false)
	assertAccountPickupAccepted(t, client)
	client.accountModeMu.Lock()
	client.accountVerifiedAt = time.Now().Add(-6 * time.Minute)
	client.accountModeMu.Unlock()
	fixture.body.Store(liveAccountBody)
	if _, err := client.OrderPickup(
		t.Context(), CarrierDPDCZ, validAccountPickupRequest(),
	); err == nil {
		t.Fatal("expired approval allowed a pickup for the wrong account mode")
	}
	if fixture.whoamiCalls.Load() != 2 || fixture.writes.Load() != 1 {
		t.Fatalf("provider calls: WHOAMI %d, writes %d; want 2, 1",
			fixture.whoamiCalls.Load(), fixture.writes.Load())
	}
}

func TestAccountModeRetriesVerificationAfterFailure(t *testing.T) {
	t.Parallel()

	client, fixture := newAccountFixture(t, false)
	fixture.body.Store(liveAccountBody)
	if _, err := client.OrderPickup(
		t.Context(), CarrierDPDCZ, validAccountPickupRequest(),
	); err == nil {
		t.Fatal("mismatched account allowed a pickup")
	}
	fixture.body.Store(testAccountBody)
	assertAccountPickupAccepted(t, client)
	if fixture.whoamiCalls.Load() != 2 || fixture.writes.Load() != 1 {
		t.Fatalf("provider calls: WHOAMI %d, writes %d; want 2, 1",
			fixture.whoamiCalls.Load(), fixture.writes.Load())
	}
}

func TestAccountModeBlocksShipmentMutationsWithoutAmbiguousOutcome(t *testing.T) {
	t.Parallel()

	client, fixture := newAccountFixture(t, false)
	fixture.body.Store(liveAccountBody)
	_, addErr := client.AddPackage(t.Context(), CarrierPPL, validTestAddRequest())
	_, orderErr := client.OrderBatch(t.Context(), CarrierPPL, "12345")
	dropErr := client.DropPackage(t.Context(), CarrierPPL, "12345")
	for _, err := range []error{addErr, orderErr, dropErr} {
		if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrAmbiguous) {
			t.Errorf("unsent mutation error = %v, want unavailable without ambiguity", err)
		}
	}
	if fixture.writes.Load() != 0 {
		t.Fatalf("wrong account received %d shipment writes", fixture.writes.Load())
	}
}

func TestAccountModeTransportFailureInvalidatesWriteApproval(t *testing.T) {
	t.Parallel()

	client, fixture := newAccountFixture(t, false)
	assertAccountPickupAccepted(t, client)
	transport := client.client.Transport
	client.client.Transport = accountFailingTransport{}
	if _, err := client.CarrierCapabilities(t.Context()); err == nil {
		t.Fatal("WHOAMI transport failure allowed capability refresh")
	}
	client.client.Transport = transport
	fixture.body.Store(liveAccountBody)
	if _, err := client.OrderPickup(
		t.Context(), CarrierDPDCZ, validAccountPickupRequest(),
	); err == nil {
		t.Fatal("WHOAMI transport failure retained write approval")
	}
	if fixture.whoamiCalls.Load() != 2 || fixture.writes.Load() != 1 {
		t.Fatalf("provider calls: WHOAMI %d, writes %d; want 2, 1",
			fixture.whoamiCalls.Load(), fixture.writes.Load())
	}
}

func TestAccountModeCapabilityFailureInvalidatesWriteApproval(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status int
		body   string
	}{
		{"mismatch", http.StatusOK, liveAccountBody},
		{"missing mode", http.StatusOK, `{"status":200,"carriers":[]}`},
		{"unavailable", http.StatusServiceUnavailable, testAccountBody},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client, fixture := newAccountFixture(t, false)
			assertAccountPickupAccepted(t, client)
			fixture.status.Store(int32(testCase.status))
			fixture.body.Store(testCase.body)
			if _, err := client.CarrierCapabilities(t.Context()); err == nil {
				t.Fatal("failed account check allowed capability refresh")
			}
			if _, err := client.OrderPickup(
				t.Context(), CarrierDPDCZ, validAccountPickupRequest(),
			); err == nil {
				t.Fatal("failed capability account check retained write approval")
			}
			if fixture.whoamiCalls.Load() != 3 || fixture.writes.Load() != 1 {
				t.Fatalf("provider calls: WHOAMI %d, writes %d; want 3, 1",
					fixture.whoamiCalls.Load(), fixture.writes.Load())
			}
		})
	}
}
