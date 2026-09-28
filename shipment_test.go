//nolint:testpackage // White-box tests exercise unexported validateAddPackage and validLabelURL.
package balikobot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
	"github.com/m1chlcz/balikobot-go/currency"
)

func validTestAddRequest() AddPackageRequest {
	return AddPackageRequest{
		EID:         "018f00000000400080000000000000aa-S1",
		ServiceType: "1",
		RecName:     "Testovací Příjemce",
		RecStreet:   "Psí 1",
		RecCity:     "Praha",
		RecZip:      "11000",
		RecCountry:  country.CZ,
		RecPhone:    "+420777000000",
		RecEmail:    "recipient@example.test",
		WeightKG:    1.25,
		LengthCM:    30,
		WidthCM:     20,
		HeightCM:    10,
		Price:       1990,
		CODCurrency: currency.CZK,
	}
}

func retryHint(err error) time.Duration {
	transient, ok := errors.AsType[*TransientError](err)
	if ok {
		return transient.RetryAfter
	}
	return 0
}

func TestAddRejectsEIDOverFortyCharacters(t *testing.T) {
	t.Parallel()
	request := validTestAddRequest()
	request.EID = strings.Repeat("A", 40)
	if err := validateAddPackage(request); err != nil {
		t.Fatalf("40-character eid: %v", err)
	}
	request.EID += "A"
	if err := validateAddPackage(request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("41-character eid: %v", err)
	}
}

func TestAddPackageUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/add" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-user:provider-secret"))
		if request.Header.Get("Authorization") != wantAuth {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var body struct {
			Packages []map[string]any `json:"packages"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Packages) != 1 {
			t.Errorf("add body = %#v", body)
		}
		entry := body.Packages[0]
		if entry["eid"] != "018f00000000400080000000000000aa-S1" ||
			entry["service_type"] != "1" || entry["weight"] != 1.25 ||
			entry["rec_name"] != "Testovací Příjemce" {
			t.Errorf("add package = %#v", entry)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [{
				"eid": "018f00000000400080000000000000aa-S1",
				"order_number": 1,
				"carrier_id": "DR1536622512M",
				"package_id": "add-ppl-8728035",
				"label_url": "` + server.URL + `/label.pdf",
				"status": 200
			}],
			"labels_url": "` + server.URL + `/labels.pdf"
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
	if err != nil {
		t.Fatalf("AddPackage: %v", err)
	}
	if result.PackageID != "add-ppl-8728035" || result.CarrierID != "DR1536622512M" ||
		result.LabelURL != server.URL+"/label.pdf" {
		t.Fatalf("add result = %#v", result)
	}
}

func TestAddPackageDuplicateEIDReturnsOriginalRecord(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [{
				"eid": "018f00000000400080000000000000aa-S1",
				"order_number": 1,
				"carrier_id": "ORIGINAL-CARRIER",
				"package_id": "add-ppl-original",
				"label_url": "` + server.URL + `/label.pdf",
				"status": 208
			}]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
	if err != nil {
		t.Fatalf("AddPackage duplicate: %v", err)
	}
	if result.PackageID != "add-ppl-original" || result.CarrierID != "ORIGINAL-CARRIER" {
		t.Fatalf("duplicate result = %#v", result)
	}
}

func TestAddPackageDuplicateEIDTopLevelReplayIsSuccess(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 208,
			"packages": [{
				"eid": "018f00000000400080000000000000aa-S1",
				"order_number": 1,
				"carrier_id": "Z6622235874",
				"package_id": "add-zasilkovna-59168",
				"label_url": "` + server.URL + `/label.pdf",
				"status": 208
			}],
			"labels_url": "` + server.URL + `/label.pdf"
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.AddPackage(context.Background(), carrier.ZASILKOVNA, validTestAddRequest())
	if err != nil {
		t.Fatalf("AddPackage top-level 208 replay: %v", err)
	}
	if result.PackageID != "add-zasilkovna-59168" || result.CarrierID != "Z6622235874" {
		t.Fatalf("top-level 208 result = %#v", result)
	}
}

func TestAddRequiresDestinationCurrencyOnEveryShipment(t *testing.T) {
	t.Parallel()

	request := validTestAddRequest()
	request.CODCurrency = ""
	if !errors.Is(validateAddPackage(request), ErrInvalidRequest) {
		t.Fatal("ADD without cod_currency accepted")
	}
	for _, currencyCode := range []currency.Code{currency.CZK, currency.EUR} {
		request = validTestAddRequest()
		request.CODCurrency = currencyCode
		if err := validateAddPackage(request); err != nil {
			t.Fatalf("cod_currency %s rejected: %v", currencyCode, err)
		}
	}
	request = validTestAddRequest()
	request.CODCurrency = currency.USD
	if !errors.Is(validateAddPackage(request), ErrInvalidRequest) {
		t.Fatal("unsupported cod_currency accepted")
	}
	request = validTestAddRequest()
	symbol := int64(1234)
	request.VS = &symbol
	if !errors.Is(validateAddPackage(request), ErrInvalidRequest) {
		t.Fatal("variable symbol without a COD amount accepted")
	}
}

func TestAddPackageClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		retryAfter string
		body       string
		want       error
		wantHint   time.Duration
	}{
		{
			name:       "http 400 is a permanent validation error",
			statusCode: http.StatusBadRequest,
			body:       `{"status": 400, "packages": [{"eid": "018f00000000400080000000000000aa-S1", "status": 400, "errors": [{"type": 413, "attribute": "rec_zip"}]}]}`,
			want:       ErrRejected,
		},
		{
			name:       "http 401 is permanent",
			statusCode: http.StatusUnauthorized,
			body:       `{"status": 401}`,
			want:       ErrRejected,
		},
		{
			name:       "http 429 is transient with the Retry-After hint",
			statusCode: http.StatusTooManyRequests,
			retryAfter: "45",
			want:       ErrUnavailable,
			wantHint:   45 * time.Second,
		},
		{
			name:       "http 500 is transient",
			statusCode: http.StatusInternalServerError,
			want:       ErrUnavailable,
		},
		{
			name:       "top-level body 426 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 426, "packages": []}`,
			want:       ErrUnavailable,
		},
		{
			name:       "top-level body 503 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 503, "packages": []}`,
			want:       ErrUnavailable,
		},
		{
			name:       "top-level body 402 is a permanent validation error",
			statusCode: http.StatusOK,
			body:       `{"status": 402, "packages": []}`,
			want:       ErrRejected,
		},
		{
			name:       "unknown top-level body status is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": 418, "packages": []}`,
			want:       ErrAmbiguous,
		},
		{
			name:       "malformed top-level body status is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": "wat", "packages": []}`,
			want:       ErrAmbiguous,
		},
		{
			name:       "body 426 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"eid": "018f00000000400080000000000000aa-S1", "status": 426}]}`,
			want:       ErrUnavailable,
		},
		{
			name:       "body 503 is transient",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"eid": "018f00000000400080000000000000aa-S1", "status": 503}]}`,
			want:       ErrUnavailable,
		},
		{
			name:       "body 413 is a permanent validation error",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"eid": "018f00000000400080000000000000aa-S1", "status": 413}]}`,
			want:       ErrRejected,
		},
		{
			name:       "malformed success is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": "broken"`,
			want:       ErrAmbiguous,
		},
		{
			name:       "success without the matching eid is ambiguous",
			statusCode: http.StatusOK,
			body:       `{"status": 200, "packages": [{"eid": "OTHER", "status": 200, "package_id": "x", "carrier_id": "y", "label_url": "/l.pdf"}]}`,
			want:       ErrAmbiguous,
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
				response.WriteHeader(testCase.statusCode)
				_, _ = response.Write([]byte(testCase.body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("AddPackage error = %v, want %v", err, testCase.want)
			}
			if hint := retryHint(err); hint != testCase.wantHint {
				t.Fatalf("retry hint = %v, want %v", hint, testCase.wantHint)
			}
		})
	}
}

func TestAddPackageMalformed2xxIsAmbiguous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		contentType string
		body        string
	}{
		{
			name:        "success without JSON content type",
			statusCode:  http.StatusOK,
			contentType: "text/plain",
			body:        `{"status":200,"packages":[]}`,
		},
		{
			name:       "unexpected empty success status",
			statusCode: http.StatusNoContent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(
				func(response http.ResponseWriter, _ *http.Request) {
					if test.contentType != "" {
						response.Header().Set("Content-Type", test.contentType)
					}
					response.WriteHeader(test.statusCode)
					_, _ = response.Write([]byte(test.body))
				},
			))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
			if !errors.Is(err, ErrAmbiguous) {
				t.Fatalf("AddPackage error = %v, want ambiguous", err)
			}
		})
	}
}

func TestAddPackageTimeoutAfterRequestIsAmbiguous(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		server := httptest.NewTestServer(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"status": 200, "packages": []}`))
		}))
		client := newTestClientWithConfig(t, server, func(config *Config) {
			config.Timeout = 50 * time.Millisecond
		})
		_, err := client.AddPackage(t.Context(), carrier.PPL, validTestAddRequest())
		if !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("timeout error = %v, want ErrAmbiguous", err)
		}
	})
}

func TestAddPackageRefusedConnectionIsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	client, err := New(Config{
		BaseURL: server.URL, User: "api-user", APIKey: "provider-secret",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.Close)
	_, err = client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrAmbiguous) {
		t.Fatalf("refused connection error = %v, want ErrUnavailable", err)
	}
}

func TestAddPackageValidatesBeforeNetwork(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	request := validTestAddRequest()
	request.EID = "not a valid eid!"
	if _, err := client.AddPackage(context.Background(), carrier.PPL, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid request error = %v", err)
	}
	if _, err := client.AddPackage(
		context.Background(), carrier.Code("PPL"), validTestAddRequest(),
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid carrier error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("provider received %d requests for invalid input", requests.Load())
	}
}

func TestOverviewMatchesExternalReference(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ppl/overview" || request.Method != http.MethodGet {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [
				{"eid": "018f00000000400080000000000000aa-S1", "carrier_id": "C1", "package_id": "add-ppl-1", "label_url": "` + server.URL + `/a.pdf"},
				{"eid": "legacy-eid", "carrier_id": "C2", "package_id": 42, "label_url": "` + server.URL + `/b.pdf"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	packages, err := client.Overview(
		context.Background(), carrier.PPL, "018f00000000400080000000000000aa-S1",
	)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(packages) != 2 ||
		packages[0].PackageID != "add-ppl-1" || packages[1].PackageID != "42" ||
		packages[1].EID != "legacy-eid" {
		t.Fatalf("overview = %#v", packages)
	}
}

func TestOverviewClassifiesOutages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	if _, err := client.Overview(
		context.Background(), carrier.PPL, "018f00000000400080000000000000aa-S1",
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("overview outage error = %v", err)
	}
}

func TestLabelsUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/ppl/labels" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			PackageIDs []string `json:"package_ids"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil ||
			len(body.PackageIDs) != 1 || body.PackageIDs[0] != "add-ppl-1" {
			t.Errorf("LABELS body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"labels_url": "` + server.URL + `/redownload.pdf"
		}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	labelURL, err := client.Labels(context.Background(), carrier.PPL, "add-ppl-1")
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	if labelURL != server.URL+"/redownload.pdf" {
		t.Fatalf("labels URL = %q", labelURL)
	}
}

func TestOrderViewLabelsUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet ||
			request.URL.Path != "/ppl/orderview/order-ppl-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"order_id": "order-ppl-1",
			"package_ids": ["add-ppl-other", "add-ppl-1"],
			"labels_url": "` + server.URL + `/ordered.pdf"
		}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	labelURL, err := client.OrderViewLabels(
		context.Background(), carrier.PPL, "order-ppl-1", "add-ppl-1",
	)
	if err != nil {
		t.Fatalf("OrderViewLabels: %v", err)
	}
	if labelURL != server.URL+"/ordered.pdf" {
		t.Fatalf("order labels URL = %q", labelURL)
	}
}

func TestLabelLookupValidatesResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		contentType string
		body        string
		orderView   bool
		want        error
	}{
		{
			name:        "LABELS requires a top-level status",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"labels_url": "placeholder"}`,
			want:        ErrInvalidResponse,
		},
		{
			name:        "LABELS classifies a provider outage",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status": 503}`,
			want:        ErrUnavailable,
		},
		{
			name:        "LABELS classifies a permanent refusal",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status": 404}`,
			want:        ErrRejected,
		},
		{
			name:        "LABELS requires JSON",
			statusCode:  http.StatusOK,
			contentType: "text/html",
			body:        `<html></html>`,
			want:        ErrInvalidResponse,
		},
		{
			name:        "ORDERVIEW validates the requested order",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status":200,"order_id":"other","package_ids":["add-ppl-1"],"labels_url":"placeholder"}`,
			orderView:   true,
			want:        ErrInvalidResponse,
		},
		{
			name:        "ORDERVIEW validates package membership",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status":200,"order_id":"order-ppl-1","package_ids":["other"],"labels_url":"placeholder"}`,
			orderView:   true,
			want:        ErrInvalidResponse,
		},
		{
			name:        "HTTP 500 is transient",
			statusCode:  http.StatusInternalServerError,
			contentType: "application/json",
			body:        `{}`,
			orderView:   true,
			want:        ErrUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				response.WriteHeader(test.statusCode)
				body := strings.ReplaceAll(test.body, "placeholder", server.URL+"/label.pdf")
				_, _ = response.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			var err error
			if test.orderView {
				_, err = client.OrderViewLabels(
					context.Background(), carrier.PPL, "order-ppl-1", "add-ppl-1",
				)
			} else {
				_, err = client.Labels(context.Background(), carrier.PPL, "add-ppl-1")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDownloadLabelValidatesContent(t *testing.T) {
	t.Parallel()

	pdf := "%PDF-1.4 fake label contents"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Errorf("label download leaked Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Accept") != "" {
			t.Errorf("label download Accept = %q, want empty", request.Header.Get("Accept"))
		}
		switch request.URL.Path {
		case "/label.pdf":
			response.Header().Set("Content-Type", "application/pdf")
			_, _ = response.Write([]byte(pdf))
		case "/label.zpl":
			response.Header().Set("Content-Type", "application/zpl")
			_, _ = response.Write([]byte("^XA^FO50,50^ADN,36,20^FDLABEL^FS^XZ"))
		case "/broken.pdf":
			response.Header().Set("Content-Type", "application/pdf")
			_, _ = response.Write([]byte("not a pdf"))
		case "/missing.pdf":
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	body, mimeType, err := client.DownloadLabel(context.Background(), server.URL+"/label.pdf")
	if err != nil || string(body) != pdf || mimeType != "application/pdf" {
		t.Fatalf("pdf label = %q/%q, %v", body, mimeType, err)
	}
	_, mimeType, err = client.DownloadLabel(context.Background(), server.URL+"/label.zpl")
	if err != nil || mimeType != "application/zpl" {
		t.Fatalf("zpl label = %q, %v", mimeType, err)
	}
	if _, _, err = client.DownloadLabel(
		context.Background(), server.URL+"/broken.pdf",
	); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("broken pdf error = %v", err)
	}
	if _, _, err = client.DownloadLabel(
		context.Background(), server.URL+"/missing.pdf",
	); !errors.Is(err, ErrRejected) {
		t.Fatalf("missing label error = %v", err)
	}
	if _, _, err = client.DownloadLabel(
		context.Background(), "https://example.com/label.pdf",
	); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("foreign label host error = %v", err)
	}
}

func TestResolveBranchIDDerivation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		carrierCode carrier.Code
		service, id string
		zip, want   string
	}{
		{carrierCode: carrier.PPL, id: "KM123", want: "123"},
		{carrierCode: carrier.PPL, id: "456", want: "456"},
		{carrierCode: carrier.CP, zip: "130 00", want: "13000"},
		{carrierCode: carrier.SP, zip: "811 01", want: "81101"},
		{carrierCode: carrier.ULOZENKA, service: "CP_NP", id: "x", zip: "130 00", want: "13000"},
		{carrierCode: carrier.ULOZENKA, service: "OTHER", id: "x", zip: "130 00", want: "x"},
		{carrierCode: carrier.INTIME, id: "42", want: "42"},
		{carrierCode: carrier.ZASILKOVNA, id: "123", want: "123"},
		{carrierCode: carrier.DPD, id: "99", want: "99"},
	}
	for _, testCase := range cases {
		if got := ResolveBranchID(
			testCase.carrierCode, testCase.service, testCase.id, testCase.zip,
		); got != testCase.want {
			t.Fatalf("ResolveBranchID(%q, %q, %q, %q) = %q, want %q",
				testCase.carrierCode, testCase.service, testCase.id, testCase.zip, got, testCase.want)
		}
	}
}

func TestAddPackageAcceptsDocumentedZPLQueryLabelURL(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [{
				"eid": "018f00000000400080000000000000aa-S1",
				"order_number": 1,
				"carrier_id": "ZPL-CARRIER",
				"package_id": "add-cp-1",
				"label_url": "` + server.URL + `/label?zpl=1",
				"status": 200
			}]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	result, err := client.AddPackage(context.Background(), carrier.CP, validTestAddRequest())
	if err != nil {
		t.Fatalf("AddPackage with documented zpl label URL: %v", err)
	}
	if result.LabelURL != server.URL+"/label?zpl=1" {
		t.Fatalf("label URL = %q", result.LabelURL)
	}
	for _, raw := range []string{
		server.URL + "/label?zpl=0",
		server.URL + "/label?zpl=1&position=2",
		server.URL + "/label?foo=bar",
		server.URL + "/label?zpl=1&zpl=1",
		server.URL + "/label?zpl=1;extra=1",
	} {
		if validLabelURL(client, raw) {
			t.Fatalf("validLabelURL accepted %q", raw)
		}
	}
	if !validLabelURL(client, server.URL+"/label?zpl=1") {
		t.Fatal("validLabelURL rejected the documented ?zpl=1 label URL")
	}
}

func TestRetryAfterHeaderClampsBeyondCap(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Retry-After", "7200")
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	_, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if hint := retryHint(err); hint != time.Hour {
		t.Fatalf("clamped hint = %v, want 1h", hint)
	}
}

func TestOverviewSkipsUnrelatedMalformedEntries(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"packages": [
				{"eid": "", "carrier_id": "", "label_url": ""},
				{"eid": "legacy-eid", "carrier_id": "C0", "package_id": "p0", "label_url": "https://evil.example/x.pdf"},
				{"eid": "018f00000000400080000000000000aa-S1", "carrier_id": "C1", "package_id": "p1", "label_url": "` + server.URL + `/a.pdf"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	packages, err := client.Overview(
		context.Background(), carrier.PPL, "018f00000000400080000000000000aa-S1",
	)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(packages) != 1 || packages[0].PackageID != "p1" {
		t.Fatalf("overview = %#v", packages)
	}
}

func TestOverviewFailsOnMalformedReconciledEntry(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"packages": [
				{"eid": "018f00000000400080000000000000aa-S1", "carrier_id": "C1", "package_id": "", "label_url": ""}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	if _, err := client.Overview(
		context.Background(), carrier.PPL, "018f00000000400080000000000000aa-S1",
	); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("overview malformed reconciled entry error = %v", err)
	}
}

func TestOverviewClassifiesTopLevelStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want error
	}{
		{
			name: "body 426 is transient",
			body: `{"status": 426, "packages": []}`,
			want: ErrUnavailable,
		},
		{
			name: "body 503 is transient",
			body: `{"status": 503, "packages": []}`,
			want: ErrUnavailable,
		},
		{
			name: "body 400 is a permanent validation error",
			body: `{"status": 400, "packages": []}`,
			want: ErrRejected,
		},
		{
			name: "body 402 is a permanent validation error",
			body: `{"status": 402, "packages": []}`,
			want: ErrRejected,
		},
		{
			name: "unknown body status is a protocol error",
			body: `{"status": 418, "packages": []}`,
			want: ErrInvalidResponse,
		},
		{
			name: "malformed body status is a protocol error",
			body: `{"status": "wat", "packages": []}`,
			want: ErrInvalidResponse,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(testCase.body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			if _, err := client.Overview(
				context.Background(), carrier.PPL,
				"018f00000000400080000000000000aa-S1",
			); !errors.Is(err, testCase.want) {
				t.Fatalf("Overview error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestCODVariableSymbolIsOptionalInteger(t *testing.T) {
	t.Parallel()

	for _, symbol := range []int64{0, 9999999999, -1, 10000000000} {
		request := validTestAddRequest()
		request.CODPrice, request.CODCurrency, request.VS = 100, currency.CZK, &symbol
		err := validateAddPackage(request)
		if symbol < 0 || symbol >= trackReferenceModulus {
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("vs %d accepted: %v", symbol, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err = json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["vs"] != float64(symbol) {
			t.Fatalf("vs = %#v, want %d", wire["vs"], symbol)
		}
	}
	request := validTestAddRequest()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"vs"`) {
		t.Fatalf("non-COD contains vs: %s", body)
	}
	request.CODPrice, request.CODCurrency = 100, currency.CZK
	if !errors.Is(validateAddPackage(request), ErrInvalidRequest) {
		t.Fatal("COD without vs accepted")
	}
}
