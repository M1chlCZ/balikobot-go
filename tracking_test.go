//nolint:testpackage // Tests share the in-package HTTP fixture helpers.
package balikobot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
)

func TestTrackStatusUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/trackstatus" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-user:provider-secret"))
		if request.Header.Get("Authorization") != wantAuth {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var body struct {
			CarrierIDs []string `json:"carrier_ids"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil ||
			len(body.CarrierIDs) != 1 || body.CarrierIDs[0] != "TRACK-1" {
			t.Errorf("trackstatus body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [{
				"carrier_id": "TRACK-1",
				"status_id": 1,
				"status_id_v2": 1.2,
				"name": "Zásilka byla doručena příjemci."
			}]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.TrackStatus(context.Background(), carrier.PPL, "TRACK-1")
	if err != nil {
		t.Fatalf("TrackStatus: %v", err)
	}
	if result.StatusID != "1.2" || result.StatusText != "Zásilka byla doručena příjemci." {
		t.Fatalf("trackstatus result = %#v", result)
	}
}

func TestTrackStatusClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		retryAfter string
		body       string
		want       error
		wantHint   time.Duration
		wantID     string
	}{
		{
			name:       "current schema uses name and prefers the detailed v2 status",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status_id":2,"status_id_v2":2.4,"name":"Returning","name_internal":"Internal text"}]}`,
			wantID:     "2.4",
		},
		{
			name:       "current schema accepts coarse status without v2 status",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status_id":1,"name":"Delivered"}]}`,
			wantID:     "1",
		},
		{
			name:       "current schema accepts the coarse error status",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status_id":0,"name":"Error"}]}`,
			wantID:     "0",
		},
		{
			name:       "current schema preserves unknown v2 status instead of delivered fallback",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status_id":1,"status_id_v2":99.9,"name":"Unknown"}]}`,
			wantID:     "99.9",
		},
		{
			name:       "current schema needs explicit envelope success when package status is absent",
			statusCode: http.StatusOK,
			body:       `{"packages":[{"carrier_id":"TRACK-1","status_id":1,"name":"Delivered"}]}`,
			want:       ErrInvalidResponse,
		},
		{
			name:       "explicit package refusal overrides current tracking fields",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status":404,"status_id":1,"status_id_v2":1.2,"name":"Delivered"}]}`,
			want:       ErrNotFound,
		},
		{
			name:       "malformed detailed status must not fall back to delivered",
			statusCode: http.StatusOK,
			body:       `{"status":200,"packages":[{"carrier_id":"TRACK-1","status_id":1,"status_id_v2":null,"name":"Delivered"}]}`,
			want:       ErrInvalidResponse,
		},
		{
			name:       "http 429 is transient and honors Retry-After",
			statusCode: http.StatusTooManyRequests,
			retryAfter: "12",
			want:       ErrUnavailable,
			wantHint:   12 * time.Second,
		},
		{
			name:       "http 503 is transient",
			statusCode: http.StatusServiceUnavailable,
			want:       ErrUnavailable,
		},
		{
			name:       "http 404 means the provider has no tracking data yet",
			statusCode: http.StatusNotFound,
			want:       ErrNotFound,
		},
		{
			name:       "http 400 is transient (transport-level refusals hit the whole account)",
			statusCode: http.StatusBadRequest,
			want:       ErrUnavailable,
		},
		{
			name:       "package status 404 means no tracking data yet",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"carrier_id": "TRACK-1", "status": 404}]}`,
			want:       ErrNotFound,
		},
		{
			name:       "package status 406 is a permanent validation error",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"carrier_id": "TRACK-1", "status": 406}]}`,
			want:       ErrRejected,
		},
		{
			name:       "integer status id is accepted in its raw form",
			statusCode: http.StatusOK,
			body:       `{"packages": [{"carrier_id": "TRACK-1", "status_id": -1, "status_text": "Ordered", "status": 200}]}`,
			wantID:     "-1",
		},
		{
			name:       "missing top-level status is tolerated",
			statusCode: http.StatusOK,
			body:       `{"packages": [{"carrier_id": "TRACK-1", "status_id": 2.2, "status_text": "Transit", "status": 200}]}`,
			wantID:     "2.2",
		},
		{
			name:       "malformed body is a protocol error",
			statusCode: http.StatusOK,
			body:       `{"packages": []}`,
			want:       ErrInvalidResponse,
		},
		{
			name:       "carrier id mismatch is a protocol error",
			statusCode: http.StatusOK,
			body:       `{"packages": [{"carrier_id": "OTHER", "status_id": 2.2, "status_text": "Transit", "status": 200}]}`,
			want:       ErrInvalidResponse,
		},
		{
			name:       "body status 503 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 503, "packages": []}`,
			want:       ErrUnavailable,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if testCase.retryAfter != "" {
					response.Header().Set("Retry-After", testCase.retryAfter)
				}
				response.Header().Set("Content-Type", "application/json")
				if testCase.statusCode != http.StatusOK {
					response.WriteHeader(testCase.statusCode)
				}
				_, _ = fmt.Fprint(response, testCase.body)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			result, err := client.TrackStatus(context.Background(), carrier.PPL, "TRACK-1")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("TrackStatus = %#v, %v; want %v", result, err, testCase.want)
			}
			if hint := retryHint(err); hint != testCase.wantHint {
				t.Fatalf("retry hint = %v, want %v", hint, testCase.wantHint)
			}
			if testCase.want == nil && result.StatusID != testCase.wantID {
				t.Fatalf("status id = %q, want %q", result.StatusID, testCase.wantID)
			}
		})
	}
}

func TestOrderBatchUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/order" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			PackageIDs []string `json:"package_ids"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil ||
			len(body.PackageIDs) != 1 || body.PackageIDs[0] != "add-ppl-1" {
			t.Errorf("order body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"order_id": "order-ppl-2274514",
			"handover_url": "https://pdf.balikobot.cz/ppl/handover",
			"labels_url": "https://pdf.balikobot.cz/ppl/labels",
			"status": 200,
			"package_ids": ["add-ppl-1"]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.OrderBatch(context.Background(), carrier.PPL, "add-ppl-1")
	if err != nil {
		t.Fatalf("OrderBatch: %v", err)
	}
	if result.OrderID != "order-ppl-2274514" {
		t.Fatalf("order result = %#v", result)
	}
}

func TestOrderBatchClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		body       string
		want       error
		wantID     string
	}{
		{
			name:       "body status 208 replays the original closure",
			statusCode: http.StatusOK,
			body:       `{"order_id": "order-ppl-2274514", "status": 208, "package_ids": ["add-ppl-1"]}`,
			wantID:     "order-ppl-2274514",
		},
		{
			name:       "body status 406 is a permanent validation error",
			statusCode: http.StatusOK,
			body:       `{"status": 406}`,
			want:       ErrRejected,
		},
		{
			name:       "body status 503 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 503}`,
			want:       ErrUnavailable,
		},
		{
			name:       "http 500 is transient",
			statusCode: http.StatusInternalServerError,
			want:       ErrUnavailable,
		},
		{
			name:       "missing order id is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": 200}`,
			want:       ErrAmbiguous,
		},
		{
			name:       "unknown body status is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": 418}`,
			want:       ErrAmbiguous,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if testCase.statusCode != http.StatusOK {
					response.WriteHeader(testCase.statusCode)
				}
				_, _ = fmt.Fprint(response, testCase.body)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			result, err := client.OrderBatch(context.Background(), carrier.PPL, "add-ppl-1")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("OrderBatch = %#v, %v; want %v", result, err, testCase.want)
			}
			if testCase.want == nil && result.OrderID != testCase.wantID {
				t.Fatalf("order id = %q, want %q", result.OrderID, testCase.wantID)
			}
		})
	}
}

func TestMutatingCallsTreatNonJSONSuccessAsAmbiguous(t *testing.T) {
	t.Parallel()

	for _, contentType := range []string{"", "text/plain"} {
		name := "missing content type"
		if contentType != "" {
			name = "wrong content type"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(
				func(response http.ResponseWriter, request *http.Request) {
					if contentType != "" {
						response.Header().Set("Content-Type", contentType)
					}
					response.WriteHeader(http.StatusOK)
					switch request.URL.Path {
					case "/ppl/order":
						_, _ = fmt.Fprint(response, `{"status": 200, "order_id": "order-ppl-1"}`)
					case "/ppl/drop":
						_, _ = fmt.Fprint(response, `{"status": 200}`)
					default:
						response.WriteHeader(http.StatusNotFound)
					}
				},
			))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)

			if _, err := client.OrderBatch(
				context.Background(), carrier.PPL, "add-ppl-1",
			); !errors.Is(err, ErrAmbiguous) {
				t.Errorf("OrderBatch = %v, want ErrAmbiguous", err)
			}
			if err := client.DropPackage(
				context.Background(), carrier.PPL, "add-ppl-1",
			); !errors.Is(err, ErrAmbiguous) {
				t.Errorf("DropPackage = %v, want ErrAmbiguous", err)
			}
		})
	}
}

func TestDropPackageUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/drop" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			PackageIDs []string `json:"package_ids"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil ||
			len(body.PackageIDs) != 1 || body.PackageIDs[0] != "add-ppl-1" {
			t.Errorf("drop body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"status": 200}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	if err := client.DropPackage(context.Background(), carrier.PPL, "add-ppl-1"); err != nil {
		t.Fatalf("DropPackage: %v", err)
	}
}

func TestDropPackageClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		body       string
		want       error
	}{
		{
			name:       "body status 404 means the package is already gone",
			statusCode: http.StatusOK,
			body:       `{"status": 404}`,
		},
		{
			name:       "body status 405 means the package was already ordered",
			statusCode: http.StatusOK,
			body:       `{"status": 405}`,
			want:       ErrRejected,
		},
		{
			name:       "http 429 is transient",
			statusCode: http.StatusTooManyRequests,
			want:       ErrUnavailable,
		},
		{
			name:       "http 500 is transient",
			statusCode: http.StatusInternalServerError,
			want:       ErrUnavailable,
		},
		{
			name:       "malformed success body is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"unexpected": true}`,
			want:       ErrAmbiguous,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if testCase.statusCode != http.StatusOK {
					response.WriteHeader(testCase.statusCode)
				}
				_, _ = fmt.Fprint(response, testCase.body)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			err := client.DropPackage(context.Background(), carrier.PPL, "add-ppl-1")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("DropPackage = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestTrackStatusRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		BaseURL: "http://127.0.0.1:1", User: "api-user", APIKey: "provider-secret",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.Close)
	for _, carrierID := range []string{"", "bad\nid", " " + string(make([]byte, 200))} {
		if _, trackErr := client.TrackStatus(
			context.Background(), carrier.PPL, carrierID,
		); !errors.Is(trackErr, ErrInvalidRequest) {
			t.Fatalf("TrackStatus(%q) = %v, want ErrInvalidRequest", carrierID, trackErr)
		}
	}
	if _, orderErr := client.OrderBatch(
		context.Background(), carrier.PPL, "",
	); !errors.Is(orderErr, ErrInvalidRequest) {
		t.Fatalf("OrderBatch empty id = %v, want ErrInvalidRequest", orderErr)
	}
	if dropErr := client.DropPackage(
		context.Background(), carrier.PPL, "bad\x00id",
	); !errors.Is(dropErr, ErrInvalidRequest) {
		t.Fatalf("DropPackage invalid id = %v, want ErrInvalidRequest", dropErr)
	}
}
