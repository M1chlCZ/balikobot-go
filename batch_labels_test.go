//nolint:testpackage // White-box tests share the in-package HTTP fixture helpers.
package balikobot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
)

func newBatchLabelServer(
	t *testing.T,
	wantIDs []string,
	mediaType, body string,
) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ppl/labels":
			if request.Method != http.MethodPost {
				t.Errorf("labels request = %s %s", request.Method, request.URL.Path)
			}
			var payload struct {
				PackageIDs []string `json:"package_ids"`
			}
			if json.NewDecoder(request.Body).Decode(&payload) != nil ||
				!slices.Equal(payload.PackageIDs, wantIDs) {
				t.Errorf("labels body = %#v, want %#v", payload.PackageIDs, wantIDs)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				response, `{"status":200,"labels_url":%q}`, server.URL+"/labels.bin",
			)
		case "/labels.bin":
			if request.Method != http.MethodGet {
				t.Errorf("label request = %s %s", request.Method, request.URL.Path)
			}
			if request.Header.Get("Authorization") != "" {
				t.Errorf("label download leaked Authorization = %q", request.Header.Get("Authorization"))
			}
			response.Header().Set("Content-Type", mediaType)
			_, _ = response.Write([]byte(body))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDownloadBatchLabelsSingleReturnsPDF(t *testing.T) {
	t.Parallel()

	pdf := "%PDF-1.4 single label"
	server := newBatchLabelServer(t, []string{"add-ppl-1"}, "application/pdf", pdf)
	client := newTestClient(t, server)
	body, mediaType, err := client.DownloadBatchLabels(
		context.Background(), carrier.PPL, []string{"add-ppl-1"},
	)
	if err != nil || string(body) != pdf || mediaType != "application/pdf" {
		t.Fatalf("DownloadBatchLabels = %q/%q, %v", body, mediaType, err)
	}
}

func TestDownloadBatchLabelsMultipleReturnsZIP(t *testing.T) {
	t.Parallel()

	archive := "PK\x03\x04zip archive bytes"
	ids := []string{"add-ppl-1", "add-ppl-2"}
	server := newBatchLabelServer(t, ids, "application/zip", archive)
	client := newTestClient(t, server)
	body, mediaType, err := client.DownloadBatchLabels(context.Background(), carrier.PPL, ids)
	if err != nil || string(body) != archive || mediaType != "application/zip" {
		t.Fatalf("DownloadBatchLabels = %q/%q, %v", body, mediaType, err)
	}
}

func TestDownloadBatchLabelsSingleAcceptsZPL(t *testing.T) {
	t.Parallel()

	zpl := "^XA^FO50,50^FDLABEL^FS^XZ"
	server := newBatchLabelServer(t, []string{"add-ppl-1"}, "application/zpl", zpl)
	client := newTestClient(t, server)
	body, mediaType, err := client.DownloadBatchLabels(
		context.Background(), carrier.PPL, []string{"add-ppl-1"},
	)
	if err != nil || string(body) != zpl || mediaType != "application/zpl" {
		t.Fatalf("DownloadBatchLabels = %q/%q, %v", body, mediaType, err)
	}
}

func TestBatchLabelsValidateBeforeNetwork(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	tooMany := make([]string, 0, labelsPackageLimit+1)
	for index := 0; index <= labelsPackageLimit; index++ {
		tooMany = append(tooMany, "add-ppl-"+strconv.Itoa(index))
	}
	cases := []struct {
		name        string
		carrierCode carrier.Code
		packageIDs  []string
	}{
		{name: "missing ids", carrierCode: carrier.PPL},
		{name: "empty id", carrierCode: carrier.PPL, packageIDs: []string{""}},
		{name: "invalid id", carrierCode: carrier.PPL, packageIDs: []string{"bad\nid"}},
		{name: "invalid carrier", carrierCode: carrier.Code("PPL"), packageIDs: []string{"add-ppl-1"}},
		{name: "too many ids", carrierCode: carrier.PPL, packageIDs: tooMany},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := client.DownloadBatchLabels(
				context.Background(), testCase.carrierCode, testCase.packageIDs,
			)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("DownloadBatchLabels error = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid batch labels reached the provider %d times", requests.Load())
	}
}

func TestBatchLabelLookupClassifiesResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		statusCode  int
		retryAfter  string
		contentType string
		body        string
		want        error
		wantHint    time.Duration
	}{
		{
			name:        "http rate limit is transient with the Retry-After hint",
			statusCode:  http.StatusTooManyRequests,
			retryAfter:  "30",
			contentType: "application/json",
			body:        `{}`,
			want:        ErrUnavailable,
			wantHint:    30 * time.Second,
		},
		{
			name:        "http server error is transient",
			statusCode:  http.StatusInternalServerError,
			contentType: "application/json",
			body:        `{}`,
			want:        ErrUnavailable,
		},
		{
			name:        "http validation is rejected",
			statusCode:  http.StatusBadRequest,
			contentType: "application/json",
			body:        `{}`,
			want:        ErrRejected,
		},
		{
			name:        "body 503 is transient",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status":503}`,
			want:        ErrUnavailable,
		},
		{
			name:        "body 404 is rejected",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status":404}`,
			want:        ErrRejected,
		},
		{
			name:        "missing status is a protocol error",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"labels_url":"placeholder"}`,
			want:        ErrInvalidResponse,
		},
		{
			name:        "non JSON body is a protocol error",
			statusCode:  http.StatusOK,
			contentType: "text/html",
			body:        `<html></html>`,
			want:        ErrInvalidResponse,
		},
		{
			name:        "foreign label host is a protocol error",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"status":200,"labels_url":"https://evil.example/labels.pdf"}`,
			want:        ErrInvalidResponse,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if testCase.retryAfter != "" {
					response.Header().Set("Retry-After", testCase.retryAfter)
				}
				response.Header().Set("Content-Type", testCase.contentType)
				response.WriteHeader(testCase.statusCode)
				body := strings.ReplaceAll(testCase.body, "placeholder", server.URL+"/labels.pdf")
				_, _ = response.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, _, err := client.DownloadBatchLabels(
				context.Background(), carrier.PPL, []string{"add-ppl-1"},
			)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("DownloadBatchLabels error = %v, want %v", err, testCase.want)
			}
			if hint := retryHint(err); hint != testCase.wantHint {
				t.Fatalf("retry hint = %v, want %v", hint, testCase.wantHint)
			}
		})
	}
}

func TestBatchLabelDownloadValidatesContent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		ids           []string
		statusCode    int
		contentType   string
		body          string
		want          error
		wantMediaType string
	}{
		{
			name:          "single PDF",
			ids:           []string{"add-ppl-1"},
			statusCode:    http.StatusOK,
			contentType:   "application/pdf",
			body:          "%PDF-1.4 label",
			wantMediaType: "application/pdf",
		},
		{
			name:          "single ZPL",
			ids:           []string{"add-ppl-1"},
			statusCode:    http.StatusOK,
			contentType:   "application/zpl",
			body:          "^XA^XZ",
			wantMediaType: "application/zpl",
		},
		{
			name:          "several packages return ZIP",
			ids:           []string{"add-ppl-1", "add-ppl-2"},
			statusCode:    http.StatusOK,
			contentType:   "application/zip",
			body:          "PK\x03\x04archive",
			wantMediaType: "application/zip",
		},
		{
			name:        "single package rejects ZIP",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusOK,
			contentType: "application/zip",
			body:        "PK\x03\x04archive",
			want:        ErrInvalidResponse,
		},
		{
			name:        "several packages reject PDF",
			ids:         []string{"add-ppl-1", "add-ppl-2"},
			statusCode:  http.StatusOK,
			contentType: "application/pdf",
			body:        "%PDF-1.4 label",
			want:        ErrInvalidResponse,
		},
		{
			name:        "several packages reject ZPL",
			ids:         []string{"add-ppl-1", "add-ppl-2"},
			statusCode:  http.StatusOK,
			contentType: "application/zpl",
			body:        "^XA^XZ",
			want:        ErrInvalidResponse,
		},
		{
			name:        "broken PDF",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusOK,
			contentType: "application/pdf",
			body:        "not a pdf",
			want:        ErrInvalidResponse,
		},
		{
			name:        "broken ZIP",
			ids:         []string{"add-ppl-1", "add-ppl-2"},
			statusCode:  http.StatusOK,
			contentType: "application/zip",
			body:        "%PDF-1.4 label",
			want:        ErrInvalidResponse,
		},
		{
			name:        "empty body",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusOK,
			contentType: "application/pdf",
			body:        "",
			want:        ErrInvalidResponse,
		},
		{
			name:        "unexpected content type",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusOK,
			contentType: "text/html",
			body:        "<html></html>",
			want:        ErrInvalidResponse,
		},
		{
			name:        "http 404 is rejected",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusNotFound,
			contentType: "application/json",
			body:        `{}`,
			want:        ErrRejected,
		},
		{
			name:        "http 500 is transient",
			ids:         []string{"add-ppl-1"},
			statusCode:  http.StatusInternalServerError,
			contentType: "application/json",
			body:        `{}`,
			want:        ErrUnavailable,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/ppl/labels" {
					response.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(
						response, `{"status":200,"labels_url":%q}`, server.URL+"/labels.bin",
					)
					return
				}
				response.Header().Set("Content-Type", testCase.contentType)
				response.WriteHeader(testCase.statusCode)
				_, _ = response.Write([]byte(testCase.body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			body, mediaType, err := client.DownloadBatchLabels(
				context.Background(), carrier.PPL, testCase.ids,
			)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("DownloadBatchLabels error = %v, want %v", err, testCase.want)
			}
			if testCase.want != nil {
				return
			}
			if mediaType != testCase.wantMediaType || string(body) != testCase.body {
				t.Fatalf("DownloadBatchLabels = %q/%q, want %q/%q",
					body, mediaType, testCase.body, testCase.wantMediaType)
			}
		})
	}
}
