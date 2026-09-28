package balikobot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
)

func validTestPickupRequest() PickupRequest {
	return PickupRequest{
		Date: "2026-09-14", WeightKG: 12.5, PackageCount: 3, Note: "Zazvoňte u skladu.",
	}
}

func TestOrderPickupDPDContract(t *testing.T) {
	t.Parallel()

	for _, carrierCode := range []carrier.Code{carrier.DPDCZ, carrier.DPD} {
		t.Run(carrierCode.String(), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(
				http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					assertPickupRequest(t, request, carrierCode)
					var body map[string]any
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					}
					if len(body) != 4 || body["date"] != "2026-09-14" ||
						body["weight"] != 12.5 || body["package_count"] != float64(3) ||
						body["message"] != "Zazvoňte u skladu." {
						t.Errorf("DPD body = %#v", body)
					}
					response.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(response, `{"status":"200"}`)
				}),
			)
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			result, err := client.OrderPickup(t.Context(), carrierCode, validTestPickupRequest())
			if err != nil || !result.Confirmed || result.ProviderID != "" {
				t.Fatalf("OrderPickup = %#v, %v", result, err)
			}
		})
	}
}

func TestOrderPickupPPLContractPreservesConfirmation(t *testing.T) {
	t.Parallel()

	for _, confirmed := range []bool{true, false} {
		t.Run(strconv.FormatBool(confirmed), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(
				http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					assertPickupRequest(t, request, carrier.PPL)
					var body map[string]any
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					}
					if len(body) != 2 || body["date"] != "2026-09-14" || body["note"] != "Zazvoňte u skladu." {
						t.Errorf("PPL body = %#v", body)
					}
					response.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(
						response,
						`{"status":200,"pickup_order_id":"BB12345600152024001","confirmed":%t}`,
						confirmed,
					)
				}),
			)
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			result, err := client.OrderPickup(t.Context(), carrier.PPL, validTestPickupRequest())
			if err != nil || result.Confirmed != confirmed || result.ProviderID != "BB12345600152024001" {
				t.Fatalf("OrderPickup = %#v, %v", result, err)
			}
		})
	}
}

func assertPickupRequest(t *testing.T, request *http.Request, carrierCode carrier.Code) {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Path != "/"+carrierCode.String()+"/orderpickup" {
		t.Errorf("request = %s %s", request.Method, request.URL.Path)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-user:provider-secret"))
	if request.Header.Get("Authorization") != wantAuth {
		t.Error("pickup request missing expected authorization")
	}
	if request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
		t.Errorf(
			"request Content-Type/Accept = %q/%q",
			request.Header.Get("Content-Type"),
			request.Header.Get("Accept"),
		)
	}
}

func TestOrderPickupClassifiesFailuresWithoutRetry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		carrier    carrier.Code
		statusCode int
		body       string
		want       error
	}{
		{"http validation", carrier.DPDCZ, http.StatusBadRequest, `{"status":400}`, ErrRejected},
		{
			"http unauthorized",
			carrier.PPL,
			http.StatusUnauthorized,
			`{"status":401}`,
			ErrRejected,
		},
		{
			"http rate limited",
			carrier.PPL,
			http.StatusTooManyRequests,
			`{"status":429}`,
			ErrRejected,
		},
		{
			"http conflict may be existing pickup",
			carrier.DPDCZ,
			http.StatusConflict,
			`{"status":409}`,
			ErrAmbiguous,
		},
		{"http timeout", carrier.DPDCZ, http.StatusRequestTimeout, `{"status":408}`, ErrAmbiguous},
		{
			"http unavailable",
			carrier.DPDCZ,
			http.StatusServiceUnavailable,
			`{"status":503}`,
			ErrAmbiguous,
		},
		{
			"http failed after success",
			carrier.PPL,
			http.StatusInternalServerError,
			`{"status":200}`,
			ErrAmbiguous,
		},
		{"unexpected 2xx", carrier.DPDCZ, http.StatusCreated, `{"status":200}`, ErrAmbiguous},
		{
			"explicit validation rejection",
			carrier.DPDCZ,
			http.StatusOK,
			`{"status":400}`,
			ErrRejected,
		},
		{
			"undocumented payment state",
			carrier.DPDCZ,
			http.StatusOK,
			`{"status":402}`,
			ErrAmbiguous,
		},
		{
			"undocumented acceptance state",
			carrier.DPDCZ,
			http.StatusOK,
			`{"status":406}`,
			ErrAmbiguous,
		},
		{
			"body conflict may be existing pickup",
			carrier.DPDCZ,
			http.StatusOK,
			`{"status":409}`,
			ErrAmbiguous,
		},
		{
			"locked may be existing pickup",
			carrier.DPDCZ,
			http.StatusOK,
			`{"status":423}`,
			ErrAmbiguous,
		},
		{"body unavailable", carrier.DPDCZ, http.StatusOK, `{"status":503}`, ErrAmbiguous},
		{"unknown body status", carrier.DPDCZ, http.StatusOK, `{"status":418}`, ErrAmbiguous},
		{"undocumented replay", carrier.DPDCZ, http.StatusOK, `{"status":208}`, ErrAmbiguous},
		{"malformed success", carrier.DPDCZ, http.StatusOK, `{"status":`, ErrAmbiguous},
		{"missing status", carrier.DPDCZ, http.StatusOK, `{}`, ErrAmbiguous},
		{"null status", carrier.DPDCZ, http.StatusOK, `{"status":null}`, ErrAmbiguous},
		{
			"PPL missing confirmation", carrier.PPL, http.StatusOK,
			`{"status":200,"pickup_order_id":"BB123"}`, ErrAmbiguous,
		},
		{
			"PPL null confirmation", carrier.PPL, http.StatusOK,
			`{"status":200,"pickup_order_id":"BB123","confirmed":null}`, ErrAmbiguous,
		},
		{
			"PPL wrong confirmation type", carrier.PPL, http.StatusOK,
			`{"status":200,"pickup_order_id":"BB123","confirmed":"true"}`, ErrAmbiguous,
		},
		{
			"PPL missing ID",
			carrier.PPL,
			http.StatusOK,
			`{"status":200,"confirmed":true}`,
			ErrAmbiguous,
		},
		{
			"PPL invalid ID", carrier.PPL, http.StatusOK,
			`{"status":200,"pickup_order_id":"BB\n123","confirmed":true}`, ErrAmbiguous,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(testCase.statusCode)
				_, _ = fmt.Fprint(response, testCase.body)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.OrderPickup(t.Context(), testCase.carrier, validTestPickupRequest())
			if !errors.Is(err, testCase.want) || requests.Load() != 1 {
				t.Fatalf("OrderPickup = %v, requests=%d; want %v and one request", err, requests.Load(), testCase.want)
			}
		})
	}
}

func TestOrderPickupUnreadableSuccessIsUnknown(t *testing.T) {
	t.Parallel()

	for _, contentType := range []string{"text/html", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				response.Header().Set("Content-Type", contentType)
				response.Header().Set("Content-Length", "1000")
				_, _ = fmt.Fprint(response, `{"status":200}`)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.OrderPickup(t.Context(), carrier.DPDCZ, validTestPickupRequest())
			if !errors.Is(err, ErrAmbiguous) || requests.Load() != 1 {
				t.Fatalf("OrderPickup = %v, requests=%d", err, requests.Load())
			}
		})
	}
}

func TestOrderPickupTimeoutIsUnknownWithoutRetry(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewTestServer(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			time.Sleep(200 * time.Millisecond)
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(response, `{"status":200}`)
		}))
		client := newTestClientWithConfig(t, server, func(config *Config) {
			config.Timeout = 50 * time.Millisecond
		})
		_, err := client.OrderPickup(t.Context(), carrier.DPDCZ, validTestPickupRequest())
		if !errors.Is(err, ErrAmbiguous) || requests.Load() != 1 {
			t.Fatalf("OrderPickup = %v, requests=%d", err, requests.Load())
		}
	})
}

func TestOrderPickupValidatesBeforeNetwork(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(*PickupRequest)
	}{
		{"missing date", func(request *PickupRequest) { request.Date = "" }},
		{"invalid date", func(request *PickupRequest) { request.Date = "2026-02-30" }},
		{"noncanonical date", func(request *PickupRequest) { request.Date = "2026-9-14" }},
		{"date with time", func(request *PickupRequest) { request.Date = "2026-09-14T00:00:00Z" }},
		{"zero packages", func(request *PickupRequest) { request.PackageCount = 0 }},
		{"negative packages", func(request *PickupRequest) { request.PackageCount = -1 }},
		{"excessive packages", func(request *PickupRequest) { request.PackageCount = 10001 }},
		{"zero weight", func(request *PickupRequest) { request.WeightKG = 0 }},
		{"negative weight", func(request *PickupRequest) { request.WeightKG = -1 }},
		{"excessive weight", func(request *PickupRequest) { request.WeightKG = 100001 }},
		{"infinite weight", func(request *PickupRequest) { request.WeightKG = math.Inf(1) }},
		{"NaN weight", func(request *PickupRequest) { request.WeightKG = math.NaN() }},
		{"long note", func(request *PickupRequest) { request.Note = strings.Repeat("ž", 256) }},
		{"multiline note", func(request *PickupRequest) { request.Note = "Sklad\npřízemí" }},
		{"invalid UTF8", func(request *PickupRequest) { request.Note = string([]byte{0xff}) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				response.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			request := validTestPickupRequest()
			testCase.change(&request)
			_, err := client.OrderPickup(t.Context(), carrier.DPDCZ, request)
			if !errors.Is(err, ErrRejected) || requests.Load() != 0 {
				t.Fatalf("OrderPickup = %v, requests=%d", err, requests.Load())
			}
		})
	}
}

func TestOrderPickupRejectsUnsupportedCarriersBeforeNetwork(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	for _, carrierCode := range []carrier.Code{
		carrier.CP, carrier.CESKAPOSTA, carrier.BALIKOVNA, carrier.GLS,
		carrier.INTIME, carrier.DPDSK, "PPL", "../ppl",
	} {
		_, err := client.OrderPickup(t.Context(), carrierCode, validTestPickupRequest())
		if !errors.Is(err, ErrRejected) {
			t.Errorf("carrier %q: OrderPickup = %v", carrierCode, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}
