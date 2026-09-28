package balikobot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	return newTestClientWithConfig(t, server, nil)
}

func newTestClientWithConfig(
	t *testing.T,
	server *httptest.Server,
	mutate func(*Config),
) *Client {
	t.Helper()
	baseURL := server.URL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:1"
	}
	config := Config{
		BaseURL:    baseURL,
		User:       "api-user",
		APIKey:     "provider-secret",
		HTTPClient: server.Client(),
	}
	if mutate != nil {
		mutate(&config)
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func boolPointer(value bool) *bool {
	return &value
}

func TestClientUsesInjectedHTTPClient(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"status":200,"branches":[]}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)

	if _, err := client.Branches(t.Context(), "ppl", "1", "CZ"); err != nil {
		t.Fatalf("Branches: %v", err)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]Config{
		"non loopback http base URL": {
			BaseURL: "http://192.0.2.1:9000", User: "api-user", APIKey: "key",
		},
		"unsupported scheme": {
			BaseURL: "ftp://127.0.0.1:9000", User: "api-user", APIKey: "key",
		},
		"base URL with path": {
			BaseURL: "http://127.0.0.1:9000/api", User: "api-user", APIKey: "key",
		},
		"base URL with query": {
			BaseURL: "http://127.0.0.1:9000?a=1", User: "api-user", APIKey: "key",
		},
		"missing api user": {
			BaseURL: "http://127.0.0.1:9000", APIKey: "key",
		},
		"missing api key": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user",
		},
		"overlong api user": {
			BaseURL: "http://127.0.0.1:9000", User: strings.Repeat("a", 101), APIKey: "key",
		},
		"overlong api key": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: strings.Repeat("a", 4097),
		},
		"negative timeout": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
			Timeout: -time.Second,
		},
		"negative response limit": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
			MaxResponseBytes: -1,
		},
		"oversized response limit": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
			MaxResponseBytes: 1 << 30 << 1,
		},
		"invalid label host": {
			BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
			LabelHosts: []string{"bad/host"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := New(config)
			if err == nil || client != nil {
				t.Fatalf("configuration = %#v, %v", client, err)
			}
		})
	}

	client, err := New(Config{User: "api-user", APIKey: "key"})
	if err != nil || client == nil {
		t.Fatalf("default configuration = %#v, %v", client, err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Fatalf("default base URL = %q", client.baseURL)
	}
	client.Close()
	client.Close()

	loopback, err := New(Config{
		BaseURL: "http://127.0.0.1:9000", User: "api-user", APIKey: "key",
	})
	if err != nil || loopback == nil {
		t.Fatalf("loopback configuration = %#v, %v", loopback, err)
	}
	loopback.Close()
}

func TestBranchesUsesOfficialV2Contract(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/ppl/branches/service/1/country/CZ" || request.Method != http.MethodGet {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-user:provider-secret"))
		if request.Header.Get("Authorization") != wantAuth {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", request.Header.Get("Accept"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"branches": [
				{
					"branch_id": "123",
					"branch_uid": "KM123",
					"type": "branch",
					"name": "PPL Pickup Praha",
					"street": "Psí 1",
					"city": "Praha",
					"zip": "11000",
					"country": "CZ",
					"unknownFutureField": true
				},
				{
					"id": 456,
					"name": "PPL Pickup Brno",
					"street": "Veterinární 2",
					"city": "Brno",
					"zip": "60200"
				}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "ppl", "1", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if requests.Load() != 1 || len(branches) != 2 {
		t.Fatalf("branches = %#v requests=%d", branches, requests.Load())
	}
	first := branches[0]
	if first.ID != "123" || first.Name != "PPL Pickup Praha" ||
		first.Street != "Psí 1" || first.City != "Praha" ||
		first.Zip != "11000" || first.Country != "CZ" {
		t.Fatalf("first branch = %#v", first)
	}
	second := branches[1]
	if second.ID != "456" || second.Country != "" {
		t.Fatalf("second branch = %#v", second)
	}
}

func TestBranchesZasilkovnaObjectListAndCountryOnlyPath(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/zasilkovna/branches/country/CZ" {
			t.Errorf("zasilkovna path = %q", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": "200",
			"branches": {
				"456": {"id": "456", "name": "Zásilkovna Brno", "street": "Veterinární 2", "city": "Brno", "zip": "60200", "country": "CZ"},
				"123": {"id": "123", "name": "Zásilkovna Praha 8", "street": "Psí 1", "city": "Praha", "zip": "18000", "country": "CZ"}
			}
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "zasilkovna", "VMCZ", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 || branches[0].ID != "123" || branches[1].ID != "456" {
		t.Fatalf("object-shaped branches = %#v", branches)
	}
}

func TestBranchesObjectListSortsMixedKeysDeterministically(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"branches": {
				"1a": {"id": "non-numeric", "name": "Pobočka C", "zip": "13000", "country": "CZ"},
				"10": {"id": "ten", "name": "Pobočka B", "zip": "12000", "country": "CZ"},
				"2": {"id": "two", "name": "Pobočka A", "zip": "11000", "country": "CZ"}
			}
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "ppl", "1", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 3 ||
		branches[0].ID != "two" || branches[1].ID != "ten" || branches[2].ID != "non-numeric" {
		t.Fatalf("mixed-key order = %#v", branches)
	}
}

func TestBranchesFiltersCountryClientSide(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/cp/branches/service/NP/country/CZ" {
			t.Errorf("cp path = %q", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"branches": [
				{"id": "1", "name": "Balíkovna Praha", "street": "Psí 1", "city": "Praha", "zip": "11000", "country": "CZ"},
				{"id": "2", "name": "Balíkovna Bratislava", "street": "Psí 2", "city": "Bratislava", "zip": "81101", "country": "SK"},
				{"id": "3", "name": "Balíkovna bez země", "street": "Psí 3", "city": "Praha", "zip": "12000"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "cp", "NP", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 || branches[0].ID != "1" || branches[1].ID != "3" {
		t.Fatalf("client-side filtered branches = %#v", branches)
	}
}

func TestBranchesFallsBackToZipName(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"branches": [
				{"id": "1", "street": "Psí 1", "city": "Praha", "zip": "11000", "country": "CZ"},
				{"id": "2", "street": "Psí 2", "city": "Praha", "country": "CZ"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "ppl", "1", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 1 || branches[0].Name != "11000" {
		t.Fatalf("zip name fallback branches = %#v", branches)
	}
}

func TestBranchesClassifiesProviderFailure(t *testing.T) {
	t.Parallel()

	for _, statusCode := range []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(statusCode)+" is retryable", func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(statusCode)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Branches HTTP %d error = %v", statusCode, err)
			}
		})
	}

	for _, statusCode := range []int{http.StatusBadRequest, http.StatusRequestTimeout} {
		t.Run(http.StatusText(statusCode)+" is a protocol violation", func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(statusCode)
				_, _ = response.Write([]byte(`{"status":400}`))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Branches HTTP %d error = %v", statusCode, err)
			}
		})
	}

	for _, bodyStatus := range []string{"426", "503", `"503"`} {
		t.Run("body status "+bodyStatus+" is retryable", func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(`{"status":` + bodyStatus + `}`))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Branches body %s error = %v", bodyStatus, err)
			}
		})
	}

	t.Run("body status 501 is permanent", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"status":501}`))
		}))
		t.Cleanup(server.Close)
		client := newTestClient(t, server)
		_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("Branches body 501 error = %v", err)
		}
	})

	t.Run("timeout is retryable", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			server := httptest.NewTestServer(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				time.Sleep(100 * time.Millisecond)
				response.WriteHeader(http.StatusOK)
			}))
			client := newTestClientWithConfig(t, server, func(config *Config) {
				config.Timeout = 20 * time.Millisecond
			})
			_, err := client.Branches(t.Context(), "ppl", "1", "CZ")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Branches timeout error = %v", err)
			}
		})
	})

	t.Run("oversized response is retryable", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(strings.Repeat("x", 300)))
		}))
		t.Cleanup(server.Close)
		client := newTestClientWithConfig(t, server, func(config *Config) {
			config.MaxResponseBytes = 256
		})
		_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Branches oversized response error = %v", err)
		}
	})
}

func TestBranchesRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"missing status":      `{"branches":[]}`,
		"non-numeric status":  `{"status":"OK","branches":[]}`,
		"rejected status":     `{"status":400,"branches":[]}`,
		"not an object":       `[]`,
		"branches not a list": `{"status":200,"branches":"none"}`,
		"broken json":         `{"status":200`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Branches %s error = %v", name, err)
			}
		})
	}

	t.Run("non JSON content type", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "text/html")
			_, _ = response.Write([]byte(`{"status":200,"branches":[]}`))
		}))
		t.Cleanup(server.Close)
		client := newTestClient(t, server)
		_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("Branches content type error = %v", err)
		}
	})
}

func TestBranchesSkipsBranchesWithoutUsableIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"status": 200,
			"branches": [
				{"id": "1", "name": "Pobočka A", "street": "Psí 1", "city": "Praha", "zip": "11000", "country": "CZ"},
				{"name": "Chybí id", "street": "Psí 2", "city": "Praha", "zip": "11000"},
				{"id": "../escape", "name": "Zlé id", "street": "Psí 3", "city": "Praha", "zip": "11000"},
				{"branch_id": "2", "name": "", "street": "Psí 4", "city": "Praha", "zip": "11000"},
				{"id": "3", "name": "Pobočka B", "street": "Psí 5", "city": "Praha", "zip": "11000", "country": "czechia"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	branches, err := client.Branches(context.Background(), "ppl", "1", "CZ")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 || branches[0].ID != "1" ||
		branches[1].ID != "2" || branches[1].Name != "11000" {
		t.Fatalf("branches = %#v", branches)
	}
}

func TestBranchesRejectsUnsafeQueryBeforeNetwork(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	queries := [][3]string{
		{"", "1", "CZ"},
		{"../ppl", "1", "CZ"},
		{"PPL Upper", "1", "CZ"},
		{"ppl", "", "CZ"},
		{"ppl", "1/2", "CZ"},
		{"ppl", strings.Repeat("1", 17), "CZ"},
		{"ppl", "1", ""},
		{"ppl", "1", "cz"},
		{"ppl", "1", "CZE"},
	}
	for _, query := range queries {
		if _, err := client.Branches(
			context.Background(), query[0], query[1], query[2],
		); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Branches(%q, %q, %q) error = %v", query[0], query[1], query[2], err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("unsafe branch queries reached provider %d times", hits.Load())
	}
}

func TestClientDoesNotFollowRedirectsOrReplayCookies(t *testing.T) {
	t.Parallel()

	var targetHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ppl/branches/service/1/country/CZ":
			if request.Header.Get("Cookie") != "" {
				t.Errorf("branches request replayed cookie %q", request.Header.Get("Cookie"))
			}
			http.SetCookie(response, &http.Cookie{Name: "provider-session", Value: "secret"})
			response.Header().Set("Location", "/redirect-target")
			response.WriteHeader(http.StatusFound)
		case "/redirect-target":
			targetHits.Add(1)
		}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	_, err := client.Branches(context.Background(), "ppl", "1", "CZ")
	if !errors.Is(err, ErrInvalidResponse) || targetHits.Load() != 0 {
		t.Fatalf("redirect branches = %v, target hits=%d", err, targetHits.Load())
	}
}

func TestBranchesSelectedCarriersUseCurrentRoute(t *testing.T) {
	t.Parallel()

	for _, carrier := range []string{"dpd", "dpdcz", "dpdsk", "cp", "ceskaposta", "balikovna", "ppl", "gls", "intime"} {
		t.Run(carrier, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter, request *http.Request,
			) {
				want := "/" + carrier + "/branches/service/1/country/CZ"
				if request.Method != http.MethodGet || request.URL.Path != want {
					t.Errorf("BRANCHES = %s %s, want GET %s", request.Method, request.URL.Path, want)
				}
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(`{"status":200,"branches":[{
					"branch_id":"12345","id":"legacy","name":"Výdejní místo",
					"street":"Psí 1","city":"Praha","zip":"11000","country":"CZ"
				}]}`))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			points, err := client.Branches(t.Context(), carrier, "1", "CZ")
			if err != nil || len(points) != 1 || points[0].ID != "12345" {
				t.Fatalf("BRANCHES = %#v, %v", points, err)
			}
		})
	}
}

func TestBranchCoordinatesPreserveValidPairsAndTolerateMissingGPS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		fields    string
		latitude  float64
		longitude float64
		valid     bool
	}{
		{"short numeric", `"lat":50.0755,"lng":14.4378`, 50.0755, 14.4378, true},
		{"short string", `"lat":" 50.0755 ","lng":"14.4378"`, 50.0755, 14.4378, true},
		{"long numeric", `"latitude":-33.86,"longitude":151.2`, -33.86, 151.2, true},
		{"long string", `"latitude":"50.0755","longitude":"14.4378"`, 50.0755, 14.4378, true},
		{"equator", `"lat":0,"lng":14`, 0, 14, true},
		{"boundary", `"lat":90,"lng":-180`, 90, -180, true},
		{"prefer complete short pair", `"lat":50,"lng":14,"latitude":51,"longitude":15`, 50, 14, true},
		{"fallback complete long pair", `"lat":null,"lng":14,"latitude":51,"longitude":15`, 51, 15, true},
		{"missing GPS", `"other":true`, 0, 0, false},
		{"missing longitude", `"lat":50`, 0, 0, false},
		{"never combine aliases", `"lat":50,"longitude":14`, 0, 0, false},
		{"null GPS", `"lat":null,"lng":null`, 0, 0, false},
		{"placeholder", `"lat":0,"lng":0`, 0, 0, false},
		{"latitude outside range", `"lat":90.01,"lng":14`, 0, 0, false},
		{"longitude outside range", `"lat":50,"lng":-180.01`, 0, 0, false},
		{"NaN", `"lat":"NaN","lng":14`, 0, 0, false},
		{"infinity", `"lat":50,"lng":"+Inf"`, 0, 0, false},
		{"overflow number", `"lat":1e9999,"lng":14`, 0, 0, false},
		{"invalid strings", `"lat":"north","lng":"east"`, 0, 0, false},
		{"boolean", `"lat":true,"lng":14`, 0, 0, false},
		{"object", `"lat":{"value":50},"lng":14`, 0, 0, false},
		{"array", `"lat":[50],"lng":14`, 0, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(
					response,
					`{"status":200,"branches":[{"branch_id":"123","type":"BOX","name":"Box","country":"CZ",%s}]}`,
					testCase.fields,
				)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server)
			branches, err := client.Branches(t.Context(), CarrierPPL, "1", "CZ")
			if err != nil || len(branches) != 1 {
				t.Fatalf("Branches = %#v, %v; missing GPS must retain the branch", branches, err)
			}
			branch := branches[0]
			if branch.ID != "123" || branch.Type != "BOX" {
				t.Fatalf("branch = %#v", branch)
			}
			hasCoordinates := branch.Latitude != nil && branch.Longitude != nil
			if hasCoordinates != testCase.valid {
				t.Fatalf("GPS presence in %#v: want %v", branch, testCase.valid)
			}
			if testCase.valid && (*branch.Latitude != testCase.latitude ||
				*branch.Longitude != testCase.longitude) {
				t.Fatalf("GPS = %v,%v; want %v,%v",
					*branch.Latitude, *branch.Longitude, testCase.latitude, testCase.longitude)
			}
		})
	}
}

func TestReadResponseBodyEnforcesLimit(t *testing.T) {
	t.Parallel()

	body, err := readResponseBody(context.Background(), strings.NewReader(strings.Repeat("a", 100)), 100)
	if err != nil || len(body) != 100 {
		t.Fatalf("exact limit read = %d bytes, %v", len(body), err)
	}
	if _, err = readResponseBody(
		context.Background(), strings.NewReader(strings.Repeat("a", 101)), 100,
	); !errors.Is(err, errResponseLimit) || !errors.Is(err, errResponseRead) {
		t.Fatalf("over limit read error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = readResponseBody(
		ctx, strings.NewReader("payload"), 100,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read error = %v", err)
	}
}

func TestRetryAfterHeaderParsingAndClamping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"not-a-number", 0},
		{"1", time.Second},
		{" 45 ", 45 * time.Second},
		{"7200", time.Hour},
	}
	for _, testCase := range cases {
		response := &http.Response{Header: http.Header{}}
		if testCase.header != "" {
			response.Header.Set("Retry-After", testCase.header)
		}
		if got := retryAfterHeader(response); got != testCase.want {
			t.Fatalf("retryAfterHeader(%q) = %v, want %v", testCase.header, got, testCase.want)
		}
	}
}

func TestTransientErrorCarriesRetryHint(t *testing.T) {
	t.Parallel()

	var missing *TransientError
	if got := missing.Error(); got != ErrUnavailable.Error() {
		t.Fatalf("nil transient error = %q", got)
	}
	if !errors.Is(missing, ErrUnavailable) {
		t.Fatal("nil transient error does not match ErrUnavailable")
	}
	hinted := &TransientError{RetryAfter: 90 * time.Second}
	if got := hinted.Error(); !strings.Contains(got, "1m30s") {
		t.Fatalf("hinted transient error = %q", got)
	}
	if !errors.Is(hinted, ErrUnavailable) {
		t.Fatal("hinted transient error does not match ErrUnavailable")
	}
}

func TestLabelHostAllowlist(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"status":200,"branches":[]}`))
	}))
	t.Cleanup(server.Close)
	loopbackHost := strings.TrimPrefix(server.URL, "http://")
	client := newTestClientWithConfig(t, server, func(config *Config) {
		config.LabelHosts = []string{"labels.example.test", ".cdn.example.test", loopbackHost}
	})

	for raw, want := range map[string]bool{
		"https://labels.example.test/label.pdf":       true,
		"https://LABELS.EXAMPLE.TEST/label.pdf":       true,
		"https://cdn.example.test/label.pdf":          false,
		"https://deep.cdn.example.test/label.pdf":     true,
		"https://example.test/label.pdf":              false,
		"https://badexample.test/label.pdf":           false,
		"https://labels.example.test.evil.test/x.pdf": false,
		"https://pdf.balikobot.cz/label.pdf":          false,
		server.URL + "/label.pdf":                     true,
		"ftp://" + loopbackHost + "/label.pdf":        false,
		"https://labels.example.test/label.pdf?zpl=1": true,
		"https://labels.example.test/label.pdf?x=1":   false,
		"https://labels.example.test/label.pdf#part":  false,
		"https://user@labels.example.test/label.pdf":  false,
		"https://labels.example.test":                 false,
	} {
		if got := validLabelURL(client, raw); got != want {
			t.Errorf("validLabelURL(%q) = %t, want %t", raw, got, want)
		}
	}

	loopbackClient := newTestClient(t, server)
	if !validLabelURL(loopbackClient, server.URL+"/label.pdf") {
		t.Fatal("loopback origin was not allowed by default")
	}
	if validLabelURL(loopbackClient, "https://pdf.balikobot.cz/label.pdf") {
		t.Fatal("loopback default accepted the provider host")
	}

	production, err := New(Config{User: "api-user", APIKey: "key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(production.Close)
	if !validLabelURL(production, "https://pdf.balikobot.cz/label.pdf") {
		t.Fatal("default allowlist rejected the provider label host")
	}
	if !validLabelURL(production, "https://cdn.balikobot.cz/label.pdf") {
		t.Fatal("default allowlist rejected a provider subdomain")
	}
	if validLabelURL(production, "https://other.example.test/label.pdf") {
		t.Fatal("default allowlist accepted a foreign host")
	}
}
