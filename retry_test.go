//nolint:testpackage // White-box tests inject the retry sleeper.
package balikobot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
)

type failingTransport struct {
	attempts *atomic.Int32
}

func (transport failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.attempts.Add(1)
	return nil, &net.DNSError{
		Err: "no such host", Name: "apiv2.balikobot.example", IsTemporary: true,
	}
}

func recordingSleep(delays *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, delay time.Duration) error {
		*delays = append(*delays, delay)
		return nil
	}
}

func TestRetryPolicyRepeatsReadOnlyCallsUntilSuccess(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"status":200,"branches":[]}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 4, BaseBackoff: 100 * time.Millisecond}
	})
	var delays []time.Duration
	client.sleep = recordingSleep(&delays)

	if _, err := client.Branches(t.Context(), carrier.PPL, "1", country.CZ); err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if !slices.Equal(delays, want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestRetryPolicyStopsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 3, BaseBackoff: 50 * time.Millisecond}
	})
	var delays []time.Duration
	client.sleep = recordingSleep(&delays)

	_, err := client.Branches(t.Context(), carrier.PPL, "1", country.CZ)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Branches = %v, want ErrUnavailable", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}
	if !slices.Equal(delays, want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestRetryPolicyHonorsRetryAfterAsFloor(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		retryAfter  string
		baseBackoff time.Duration
		wantDelay   time.Duration
	}{
		"hint is longer":    {retryAfter: "5", baseBackoff: 100 * time.Millisecond, wantDelay: 5 * time.Second},
		"backoff is longer": {retryAfter: "1", baseBackoff: 2 * time.Second, wantDelay: 2 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				response.Header().Set("Retry-After", testCase.retryAfter)
				response.WriteHeader(http.StatusTooManyRequests)
			}))
			t.Cleanup(server.Close)
			client := newTestClientWithConfig(t, server, func(config *Config) {
				config.RetryPolicy = RetryPolicy{MaxAttempts: 2, BaseBackoff: testCase.baseBackoff}
			})
			var delays []time.Duration
			client.sleep = recordingSleep(&delays)

			_, err := client.TrackStatus(t.Context(), carrier.PPL, "TRACK-1")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("TrackStatus = %v, want ErrUnavailable", err)
			}
			if attempts.Load() != 2 || !slices.Equal(delays, []time.Duration{testCase.wantDelay}) {
				t.Fatalf("attempts = %d, delays = %v; want 2 and %v",
					attempts.Load(), delays, testCase.wantDelay)
			}
		})
	}
}

func TestRetryPolicyNeverRepeatsMutatingCalls(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr error
		call    func(*Client) error
	}{
		{
			name:    "AddPackage",
			wantErr: ErrUnavailable,
			call: func(client *Client) error {
				_, err := client.AddPackage(context.Background(), carrier.PPL, validTestAddRequest())
				return err
			},
		},
		{
			name:    "OrderBatch",
			wantErr: ErrUnavailable,
			call: func(client *Client) error {
				_, err := client.OrderBatch(context.Background(), carrier.PPL, "add-ppl-1")
				return err
			},
		},
		{
			name:    "DropPackage",
			wantErr: ErrUnavailable,
			call: func(client *Client) error {
				return client.DropPackage(context.Background(), carrier.PPL, "add-ppl-1")
			},
		},
		{
			name:    "OrderPickup",
			wantErr: ErrAmbiguous,
			call: func(client *Client) error {
				_, err := client.OrderPickup(
					context.Background(), carrier.DPDCZ, validTestPickupRequest(),
				)
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			client, err := New(Config{
				BaseURL: "http://127.0.0.1:1",
				User:    "api-user",
				APIKey:  "provider-secret",
				HTTPClient: &http.Client{
					Transport: failingTransport{attempts: &attempts},
				},
				RetryPolicy: RetryPolicy{MaxAttempts: 5, BaseBackoff: time.Millisecond},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(client.Close)
			var delays []time.Duration
			client.sleep = recordingSleep(&delays)

			if callErr := testCase.call(client); !errors.Is(callErr, testCase.wantErr) {
				t.Fatalf("call = %v, want %v", callErr, testCase.wantErr)
			}
			if attempts.Load() != 1 || len(delays) != 0 {
				t.Fatalf("attempts = %d, delays = %v; want one attempt without backoff",
					attempts.Load(), delays)
			}
		})
	}
}

func TestRetryPolicyRetriesCapabilityDiscovery(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"status":200,"carriers":[{"slug":"ppl"}]}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2, BaseBackoff: 10 * time.Millisecond}
	})
	client.sleep = recordingSleep(new([]time.Duration))

	whoami, err := client.WhoAmI(t.Context())
	if err != nil || attempts.Load() != 2 || len(whoami.Carriers) != 1 {
		t.Fatalf("WhoAmI = %#v, %v, attempts = %d", whoami, err, attempts.Load())
	}
}

func TestRetryPolicyRetriesStreamingBeforeFirstByte(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	pdf := "%PDF-1.4 retried label"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/pdf")
		_, _ = response.Write([]byte(pdf))
	}))
	t.Cleanup(server.Close)
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 3, BaseBackoff: 10 * time.Millisecond}
	})
	var delays []time.Duration
	client.sleep = recordingSleep(&delays)

	var target countingBuffer
	if err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target); err != nil {
		t.Fatalf("DownloadLabelTo: %v", err)
	}
	if attempts.Load() != 2 || target.buffer.String() != pdf {
		t.Fatalf("attempts = %d, body = %q", attempts.Load(), target.buffer.String())
	}
	if !slices.Equal(delays, []time.Duration{10 * time.Millisecond}) {
		t.Fatalf("delays = %v", delays)
	}
}

func TestRetryPolicyDoesNotRepeatStreamingAfterPartialWrite(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		response.Header().Set("Content-Type", "application/pdf")
		response.Header().Set("Content-Length", "1000")
		_, _ = response.Write([]byte("%PDF-1.4 partial"))
		response.(http.Flusher).Flush()
	}))
	t.Cleanup(server.Close)
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 3, BaseBackoff: 10 * time.Millisecond}
	})
	var delays []time.Duration
	client.sleep = recordingSleep(&delays)

	var target countingBuffer
	err := client.DownloadLabelTo(t.Context(), server.URL+"/label.pdf", &target)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("DownloadLabelTo = %v, want ErrUnavailable", err)
	}
	if attempts.Load() != 1 || len(delays) != 0 {
		t.Fatalf("attempts = %d, delays = %v; want one attempt without backoff",
			attempts.Load(), delays)
	}
	if target.written == 0 {
		t.Fatal("mid-stream failure wrote no partial output")
	}
}

func TestRetryPolicyJitterStaysWithinBackoff(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	for range 20 {
		client := newTestClientWithConfig(t, server, func(config *Config) {
			config.RetryPolicy = RetryPolicy{
				MaxAttempts: 2, BaseBackoff: time.Second, Jitter: true,
			}
		})
		var delays []time.Duration
		client.sleep = recordingSleep(&delays)

		if _, err := client.Branches(t.Context(), carrier.PPL, "1", country.CZ); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Branches = %v", err)
		}
		if len(delays) != 1 || delays[0] < 0 || delays[0] >= time.Second {
			t.Fatalf("jittered delay = %v", delays)
		}
	}
}

func TestNewRejectsInvalidRetryPolicy(t *testing.T) {
	t.Parallel()

	for name, policy := range map[string]RetryPolicy{
		"negative attempts": {MaxAttempts: -1},
		"negative backoff":  {BaseBackoff: -time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := New(Config{
				BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
				RetryPolicy: policy,
			})
			if err == nil || client != nil {
				t.Fatalf("configuration = %#v, %v", client, err)
			}
		})
	}
}
