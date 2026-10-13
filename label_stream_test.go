//nolint:testpackage // White-box tests exercise the fixed label byte limit.
package balikobot

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/m1chlcz/balikobot-go/carrier"
)

type countingBuffer struct {
	buffer  bytes.Buffer
	written int64
}

func (target *countingBuffer) Write(data []byte) (int, error) {
	count, err := target.buffer.Write(data)
	target.written += int64(count)
	return count, err
}

type failingWriter struct {
	err error
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func TestDownloadLabelToStreamsValidatedBody(t *testing.T) {
	t.Parallel()

	pdf := "%PDF-1.4 streamed label"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/label.pdf" {
			t.Errorf("label request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Errorf("label download leaked Authorization = %q", request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/pdf")
		_, _ = response.Write([]byte(pdf))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	if err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target); err != nil {
		t.Fatalf("DownloadLabelTo: %v", err)
	}
	if target.buffer.String() != pdf {
		t.Fatalf("streamed body = %q", target.buffer.String())
	}
}

func TestDownloadLabelToValidatesMagicBeforeWriting(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"wrong magic":  "not a pdf",
		"empty body":   "",
		"short prefix": "%PD",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/pdf")
				_, _ = response.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			var target countingBuffer
			err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target)
			if !errors.Is(err, ErrInvalidResponse) || target.written != 0 {
				t.Fatalf("DownloadLabelTo = %v, written = %d", err, target.written)
			}
		})
	}
}

func TestDownloadLabelToEnforcesLabelLimit(t *testing.T) {
	t.Parallel()

	body := "%PDF-" + strings.Repeat("a", labelResponseLimit)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/pdf")
		_, _ = response.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("DownloadLabelTo = %v, want ErrInvalidResponse", err)
	}
	if target.written == 0 || target.written > labelResponseLimit {
		t.Fatalf("streamed %d bytes, limit is %d", target.written, labelResponseLimit)
	}
}

func TestDownloadLabelToRejectsHostileHost(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("hostile label URL reached the network")
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	err := client.DownloadLabelTo(t.Context(), "https://hostile.example.test/label.pdf", &target)
	if !errors.Is(err, ErrInvalidRequest) || target.written != 0 {
		t.Fatalf("DownloadLabelTo = %v, written = %d", err, target.written)
	}
}

func TestDownloadLabelToClassifiesMidStreamFailure(t *testing.T) {
	t.Parallel()

	const declaredLength = 1000
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/pdf")
		response.Header().Set("Content-Length", "1000")
		_, _ = response.Write([]byte("%PDF-1.4 partial"))
		response.(http.Flusher).Flush()
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("DownloadLabelTo = %v, want ErrUnavailable", err)
	}
	if target.written == 0 || target.written >= declaredLength {
		t.Fatalf("streamed %d of %d declared bytes", target.written, declaredLength)
	}
}

func TestDownloadLabelToReturnsWriterFailureUnchanged(t *testing.T) {
	t.Parallel()

	pdf := "%PDF-1.4 label"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/pdf")
		_, _ = response.Write([]byte(pdf))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	failure := errors.New("target writer closed")
	err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", failingWriter{err: failure})
	if !errors.Is(err, failure) {
		t.Fatalf("DownloadLabelTo = %v, want the writer failure", err)
	}
}

func TestDownloadBatchLabelsToStreamsZIP(t *testing.T) {
	t.Parallel()

	archive := "PK\x03\x04zip archive bytes"
	ids := []string{"add-ppl-1", "add-ppl-2"}
	server := newBatchLabelServer(t, ids, "application/zip", archive)
	client := newTestClient(t, server)

	var target countingBuffer
	mediaType, err := client.DownloadBatchLabelsTo(t.Context(), carrier.PPL, ids, &target)
	if err != nil || mediaType != "application/zip" || target.buffer.String() != archive {
		t.Fatalf("DownloadBatchLabelsTo = %q, %q, %v", target.buffer.String(), mediaType, err)
	}
}

func TestDownloadBatchLabelsToRejectsForeignLookupURL(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(
			`{"status":200,"labels_url":"https://hostile.example.test/labels.pdf"}`,
		))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	mediaType, err := client.DownloadBatchLabelsTo(
		t.Context(), carrier.PPL, []string{"add-ppl-1"}, &target,
	)
	if !errors.Is(err, ErrInvalidResponse) || mediaType != "" || target.written != 0 {
		t.Fatalf("DownloadBatchLabelsTo = %q, %v, written = %d", mediaType, err, target.written)
	}
	if requests.Load() != 1 {
		t.Fatalf("lookup requests = %d, want 1", requests.Load())
	}
}

func TestDownloadBatchLabelsToValidatesBeforeNetwork(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	var target countingBuffer
	mediaType, err := client.DownloadBatchLabelsTo(
		t.Context(), carrier.PPL, []string{"bad\nid"}, &target,
	)
	if !errors.Is(err, ErrInvalidRequest) || mediaType != "" || target.written != 0 {
		t.Fatalf("DownloadBatchLabelsTo = %q, %v, written = %d", mediaType, err, target.written)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid batch reached the provider %d times", requests.Load())
	}
}
