// Package balikobot is a zero-dependency client for the Balíkobot shipping
// API v2.
//
// The client covers the documented endpoints for carrier branches, packages,
// labels, tracking, pickup orders and account capabilities. It performs no
// logging, caching, persistence or automatic retries. Every method maps the
// provider answer to a small set of sentinel errors, so the caller decides how
// to react to a retryable outage or a permanent rejection.
//
// Requests use HTTP Basic authentication: Config.User is the API user and
// Config.APIKey is the API password. All JSON responses are read with a hard
// byte limit and every request carries the caller's context.
package balikobot

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
)

const (
	// DefaultBaseURL is the production Balíkobot API v2 endpoint.
	DefaultBaseURL = "https://apiv2.balikobot.cz"
	// DefaultTimeout is the request timeout used when Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxResponseBytes is the JSON response size limit used when
	// Config.MaxResponseBytes is zero.
	DefaultMaxResponseBytes = 8 << 20

	maxResponseBytesLimit  = 1 << 30
	labelResponseLimit     = 4 << 20
	dialTimeout            = 3 * time.Second
	readBufferSize         = 32 << 10
	branchFieldLimit       = 200
	addFieldLimit          = 255
	referenceLimit         = 40
	zipLimit               = 16
	identifierLimit        = 100
	fieldPackageIDs        = "package_ids"
	contentTypeJSON        = "application/json"
	mimeTypePDF            = "application/pdf"
	mimeTypeZPL            = "application/zpl"
	schemeHTTP             = "http"
	schemeHTTPS            = "https"
	nullLiteral            = "null"
	accountModeCacheTTL    = 5 * time.Minute
	credentialUserLimit    = 100
	credentialKeyLimit     = 4096
	minRetryAfterSeconds   = 1
	maxRetryAfterSeconds   = int(time.Hour / time.Second)
	minorUnitsPerCurrency  = 100
	decimalExponentLimit   = 64
	trackReferenceModulus  = 10_000_000_000
	pickupPackageLimit     = 10_000
	pickupWeightLimit      = 100_000
	pickupNoteLimit        = 255
	capabilityCarrierLimit = 128
	capabilityServiceLimit = 512
	capabilityNameLimit    = 512
	labelQueryZPL          = "zpl=1"
	pdfMagicPrefix         = "%PDF-"
	zplMagicPrefix         = "^X"
)

var (
	servicePattern  = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)
	branchIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	eidPattern      = regexp.MustCompile(`^[A-Za-z0-9-]{8,` + strconv.Itoa(referenceLimit) + `}$`)
	trackIDPattern  = regexp.MustCompile(`^-?[0-9]{1,3}(\.[0-9]{1,2})?$`)

	errResponseRead      = errors.New("balikobot: response body read failed")
	errResponseLimit     = errors.New("balikobot: response body exceeds limit")
	errAccountUnverified = errors.New("balikobot: account mode is not verified")

	// ErrUnavailable reports that the provider is temporarily unavailable or
	// that the request never left the client. A retry can succeed. A
	// TransientError wraps this sentinel when the provider sent a retry hint.
	ErrUnavailable = errors.New("balikobot: temporarily unavailable")
	// ErrNotFound reports a documented "no data yet" answer, currently the
	// missing tracking data of a package the carrier has not scanned yet.
	ErrNotFound = errors.New("balikobot: resource not found")
	// ErrInvalidResponse reports a provider answer that violates the protocol,
	// for example a broken body, a missing status or a foreign label URL.
	ErrInvalidResponse = errors.New("balikobot: invalid provider response")
	// ErrInvalidRequest reports arguments rejected locally before any network
	// call, for example an invalid carrier code or a missing recipient field.
	ErrInvalidRequest = errors.New("balikobot: invalid request")
	// ErrRejected reports a permanent refusal of the request or of the
	// supplied data. Retrying the same request cannot succeed.
	ErrRejected = errors.New("balikobot: provider permanently rejected the request")
	// ErrAmbiguous reports that a mutating call may have reached the provider.
	// Reconcile the result by external reference before any retry.
	ErrAmbiguous = errors.New("balikobot: request outcome is unknown")
)

// TransientError reports a retryable provider answer and carries the optional
// Retry-After hint of the provider. It matches ErrUnavailable with [errors.Is].
type TransientError struct {
	// RetryAfter is the provider retry hint. A zero value means that the
	// provider sent no hint and the caller should apply its own backoff.
	RetryAfter time.Duration
}

// Error implements the error interface.
func (err *TransientError) Error() string {
	if err == nil || err.RetryAfter <= 0 {
		return ErrUnavailable.Error()
	}
	return ErrUnavailable.Error() + " (retry after " + err.RetryAfter.String() + ")"
}

// Unwrap returns ErrUnavailable, so [errors.Is](err, ErrUnavailable) is true
// for every transient provider answer.
func (err *TransientError) Unwrap() error {
	return ErrUnavailable
}

// Config configures a Client.
type Config struct {
	// BaseURL is the API root, for example "https://apiv2.balikobot.cz". The
	// default is DefaultBaseURL. Only the loopback host of a test server may
	// use the http scheme.
	BaseURL string
	// User is the API user. It must not be empty.
	User string
	// APIKey is the API key used as the HTTP Basic password. It must not be
	// empty.
	APIKey string
	// HTTPClient is an optional caller-owned HTTP client. The client clones
	// it, applies Timeout and refuses redirects. Close does not close the
	// idle connections of a caller-owned client.
	HTTPClient *http.Client
	// Timeout is the whole-request timeout. Zero selects DefaultTimeout.
	Timeout time.Duration
	// MaxResponseBytes is the hard byte limit for a JSON response body. Zero
	// selects DefaultMaxResponseBytes. Label downloads use a fixed 4 MiB
	// limit.
	MaxResponseBytes int
	// LabelHosts optionally restricts label downloads to these hosts. A
	// leading dot selects a suffix match, so ".balikobot.cz" covers every
	// subdomain while "pdf.balikobot.cz" matches one host. When empty, the
	// client allows the Balíkobot label hosts, or only the BaseURL origin
	// when BaseURL is a loopback test server.
	LabelHosts []string
	// LiveAccount optionally enables account-mode verification before every
	// mutating call. When set, the client first calls WHOAMI and requires the
	// live_account flag to equal this value. A failed check blocks the write
	// before it is sent. When nil, no account-mode check is performed.
	LiveAccount *bool
}

// Client is a Balíkobot API v2 client. Create it with New. Every method is
// safe for concurrent use except Close, which must not run concurrently with
// other calls.
type Client struct {
	client               *http.Client
	baseURL              string
	authorization        string
	origin               string
	loopback             bool
	maxResponseBytes     int
	labelHosts           []string
	liveAccountExpected  *bool
	accountModeMu        sync.Mutex
	accountVerifiedAt    time.Time
	closeIdleConnections func()
	closeOnce            sync.Once
}

// New validates the configuration and returns a client. The user name and the
// API key must not be empty, and BaseURL must be an absolute URL. The https
// scheme is mandatory unless the host is a loopback address, which supports
// local test servers.
func New(config Config) (*Client, error) {
	user := strings.TrimSpace(config.User)
	if user == "" || len(user) > credentialUserLimit {
		return nil, errors.New("balikobot: invalid configuration: user is required and limited to 100 bytes")
	}
	if config.APIKey == "" || len(config.APIKey) > credentialKeyLimit {
		return nil, errors.New("balikobot: invalid configuration: API key is required and limited to 4096 bytes")
	}
	if config.Timeout < 0 {
		return nil, errors.New("balikobot: invalid configuration: timeout must not be negative")
	}
	if config.MaxResponseBytes < 0 || config.MaxResponseBytes > maxResponseBytesLimit {
		return nil, errors.New(
			"balikobot: invalid configuration: response limit must be between 0 and 1073741824 bytes",
		)
	}
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	origin, loopback, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	labelHosts, err := normalizeLabelHosts(config.LabelHosts)
	if err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	httpClient, closeIdleConnections := newHTTPClient(config.HTTPClient, timeout)
	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+config.APIKey))
	return &Client{
		client:               httpClient,
		baseURL:              baseURL,
		authorization:        authorization,
		origin:               origin,
		loopback:             loopback,
		maxResponseBytes:     maxResponseBytes,
		labelHosts:           labelHosts,
		liveAccountExpected:  config.LiveAccount,
		closeIdleConnections: closeIdleConnections,
	}, nil
}

// Close releases the idle connections created by the client. It does nothing
// for a caller-owned Config.HTTPClient. Repeated calls are safe.
func (client *Client) Close() {
	if client == nil {
		return
	}
	client.closeOnce.Do(func() {
		if client.closeIdleConnections != nil {
			client.closeIdleConnections()
		}
	})
}

func validateBaseURL(raw string) (string, bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", false, errors.New(
			"balikobot: invalid configuration: base URL must be absolute without path, query or fragment",
		)
	}
	address := net.ParseIP(parsed.Hostname())
	loopback := address != nil && address.IsLoopback()
	switch parsed.Scheme {
	case schemeHTTPS:
	case schemeHTTP:
		if !loopback {
			return "", false, errors.New(
				"balikobot: invalid configuration: base URL must use https unless the host is loopback",
			)
		}
	default:
		return "", false, errors.New("balikobot: invalid configuration: base URL must use http or https")
	}
	return parsed.Scheme + "://" + parsed.Host, loopback, nil
}

func normalizeLabelHosts(hosts []string) ([]string, error) {
	normalized := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.ContainsAny(host, "/\\@?#") {
			return nil, errors.New("balikobot: invalid configuration: label hosts must be host names")
		}
		normalized = append(normalized, host)
	}
	return normalized, nil
}

func newHTTPClient(injected *http.Client, timeout time.Duration) (*http.Client, func()) {
	if injected != nil {
		clone := *injected
		clone.Timeout = timeout
		clone.CheckRedirect = refuseRedirect
		return &clone, nil
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("balikobot: default transport is not an *http.Transport")
	}
	transport := defaultTransport.Clone()
	transport.DialContext = (&net.Dialer{Timeout: min(timeout, dialTimeout)}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: refuseRedirect,
	}
	return client, client.CloseIdleConnections
}

func refuseRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (client *Client) newJSONRequest(
	ctx context.Context,
	method string,
	rawURL string,
	body any,
) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", contentTypeJSON)
	request.Header.Set("Authorization", client.authorization)
	if body != nil {
		request.Header.Set("Content-Type", contentTypeJSON)
	}
	return request, nil
}

func (client *Client) request(
	ctx context.Context,
	method string,
	path string,
	body any,
) (*http.Response, error) {
	if method != http.MethodGet {
		if _, err := client.verifiedWhoAmI(ctx, true); err != nil {
			return nil, errAccountUnverified
		}
	}
	request, err := client.newJSONRequest(ctx, method, client.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	return client.client.Do(request)
}

func (client *Client) verifiedWhoAmI(ctx context.Context, allowCached bool) (whoAmIWire, error) {
	var whoami whoAmIWire
	if allowCached && client.liveAccountExpected == nil {
		return whoami, nil
	}
	client.accountModeMu.Lock()
	defer client.accountModeMu.Unlock()
	age := time.Since(client.accountVerifiedAt)
	if allowCached && age >= 0 && age < accountModeCacheTTL {
		return whoami, nil
	}
	client.accountVerifiedAt = time.Time{}
	if err := client.capabilityGET(ctx, "/info/whoami", &whoami, false); err != nil {
		return whoAmIWire{}, err
	}
	if client.liveAccountExpected != nil {
		if whoami.LiveAccount == nil || *whoami.LiveAccount != *client.liveAccountExpected {
			return whoAmIWire{}, ErrInvalidResponse
		}
		client.accountVerifiedAt = time.Now()
	}
	return whoami, nil
}

// Branch describes one carrier branch or pickup point.
type Branch struct {
	// ID is the branch identifier used by the carrier.
	ID string `json:"ID"`
	// Type is the provider branch type, for example "branch" or "box".
	Type string `json:"Type"`
	// Name is the display name. It falls back to Zip when the provider sends
	// no name.
	Name string `json:"Name"`
	// Street is the street part of the address.
	Street string `json:"Street"`
	// City is the city part of the address.
	City string `json:"City"`
	// Zip is the postal code.
	Zip string `json:"Zip"`
	// Country is the ISO 3166-1 alpha-2 country code. It can be empty when
	// the provider omits it for a domestic branch.
	Country country.Code `json:"Country"`
	// Latitude is the GPS latitude when the provider sent a valid pair.
	Latitude *float64 `json:"Latitude"`
	// Longitude is the GPS longitude when the provider sent a valid pair.
	Longitude *float64 `json:"Longitude"`
}

type branchesResponse struct {
	Status   responseStatus `json:"status"`
	Branches branchList     `json:"branches"`
}

type branchList struct {
	entries []branchWire
}

// UnmarshalJSON accepts both documented BRANCHES shapes: a plain array and an
// object keyed by numeric strings.
func (list *branchList) UnmarshalJSON(raw []byte) error {
	if string(raw) == nullLiteral {
		return nil
	}
	if len(raw) > 0 && raw[0] == '[' {
		return json.Unmarshal(raw, &list.entries)
	}
	var keyed map[string]branchWire
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return errors.New("invalid Balíkobot branches list")
	}
	keys := make([]string, 0, len(keyed))
	for key := range keyed {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareBranchKeys)
	for _, key := range keys {
		list.entries = append(list.entries, keyed[key])
	}
	return nil
}

func compareBranchKeys(leftKey, rightKey string) int {
	left, leftErr := strconv.Atoi(leftKey)
	right, rightErr := strconv.Atoi(rightKey)
	switch {
	case leftErr == nil && rightErr == nil:
		return cmp.Compare(left, right)
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	default:
		return cmp.Compare(leftKey, rightKey)
	}
}

type branchWire struct {
	Type      string     `json:"type"`
	BranchID  branchID   `json:"branch_id"`
	ID        branchID   `json:"id"`
	Name      string     `json:"name"`
	Street    string     `json:"street"`
	City      string     `json:"city"`
	Zip       string     `json:"zip"`
	Country   string     `json:"country"`
	Lat       coordinate `json:"lat"`
	Lng       coordinate `json:"lng"`
	Latitude  coordinate `json:"latitude"`
	Longitude coordinate `json:"longitude"`
}

type coordinate struct {
	value float64
	set   bool
}

// UnmarshalJSON tolerates invalid optional GPS values instead of rejecting the
// whole branch list.
func (point *coordinate) UnmarshalJSON(raw []byte) error {
	point.set = false
	encoded := string(raw)
	var quoted string
	if json.Unmarshal(raw, &quoted) == nil {
		encoded = quoted
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(encoded), 64)
	if err == nil {
		point.value, point.set = value, true
	}
	return nil
}

type responseStatus struct {
	value int
	set   bool
}

// UnmarshalJSON accepts both documented status shapes: a number and a numeric
// string.
func (status *responseStatus) UnmarshalJSON(raw []byte) error {
	if string(raw) == nullLiteral {
		return errors.New("invalid Balíkobot response status")
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		var encoded string
		if json.Unmarshal(raw, &encoded) != nil || len(encoded) == 0 || len(encoded) > 3 {
			return errors.New("invalid Balíkobot response status")
		}
		for _, digit := range encoded {
			if digit < '0' || digit > '9' {
				return errors.New("invalid Balíkobot response status")
			}
		}
		parsed, atoiErr := strconv.Atoi(encoded)
		if atoiErr != nil {
			return errors.New("invalid Balíkobot response status")
		}
		value = parsed
	}
	status.value = value
	status.set = true
	return nil
}

type branchID struct {
	value string
	set   bool
}

// UnmarshalJSON accepts string and integer branch ids. An id that cannot be
// used as a branch reference leaves the value unset instead of failing the
// whole branch list.
func (id *branchID) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if jsonRawKind(raw) != '0' {
			return errors.New("invalid Balíkobot branch id")
		}
		encoded := string(raw)
		if strings.ContainsAny(encoded, ".eE") {
			return errors.New("invalid Balíkobot branch id")
		}
		text = encoded
	}
	if !branchIDPattern.MatchString(text) {
		return nil
	}
	id.value = text
	id.set = true
	return nil
}

// Branches calls the BRANCHES method and returns the branches of one carrier
// service in one country. The route depends on the carrier: the selected
// carriers use the combined service and country segments, Zásilkovna uses the
// country-only route, and the remaining carriers use the service-only route
// with a client-side country filter.
func (client *Client) Branches(
	ctx context.Context,
	carrierCode carrier.Code,
	service string,
	countryCode country.Code,
) ([]Branch, error) {
	if client == nil || client.client == nil ||
		!carrierCode.Valid() ||
		!servicePattern.MatchString(service) ||
		!countryCode.Valid() {
		return nil, ErrInvalidRequest
	}
	path, filterCountry := branchesPath(carrierCode, service, countryCode)
	response, err := client.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError {
		return nil, ErrUnavailable
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		return nil, ErrInvalidResponse
	}
	var result branchesResponse
	decodeErr := client.decodeResponse(ctx, response, &result)
	if errors.Is(decodeErr, errResponseRead) {
		return nil, ErrUnavailable
	}
	if decodeErr != nil || !result.Status.set {
		return nil, ErrInvalidResponse
	}
	switch result.Status.value {
	case http.StatusOK:
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return nil, ErrUnavailable
	default:
		return nil, ErrInvalidResponse
	}
	branches := make([]Branch, 0, len(result.Branches.entries))
	for _, wire := range result.Branches.entries {
		branch, ok := sanitizeBranch(wire)
		if !ok {
			continue
		}
		if filterCountry && branch.Country != "" && branch.Country != countryCode {
			continue
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

func branchesPath(carrierCode carrier.Code, service string, countryCode country.Code) (string, bool) {
	path := "/" + string(carrierCode) + "/branches/service/" + service
	switch carrierCode {
	case carrier.PPL, carrier.DPD, carrier.DPDCZ, carrier.DPDSK,
		carrier.GEIS, carrier.GLS, carrier.INTIME:
		return path + "/country/" + string(countryCode), false
	case carrier.CP, carrier.CESKAPOSTA, carrier.BALIKOVNA:
		return path + "/country/" + string(countryCode), true
	case carrier.ZASILKOVNA:
		return "/" + string(carrierCode) + "/branches/country/" + string(countryCode), false
	case carrier.SP, carrier.ULOZENKA:
		return path, true
	default:
		return path, true
	}
}

func sanitizeBranch(wire branchWire) (Branch, bool) {
	branch := Branch{
		Type:    wire.Type,
		Name:    wire.Name,
		Street:  wire.Street,
		City:    wire.City,
		Zip:     wire.Zip,
		Country: country.Code(wire.Country),
	}
	switch {
	case wire.BranchID.set:
		branch.ID = wire.BranchID.value
	case wire.ID.set:
		branch.ID = wire.ID.value
	default:
		return Branch{}, false
	}
	if !validBranchField(branch.Name, branchFieldLimit) ||
		!validBranchField(branch.Street, branchFieldLimit) ||
		!validBranchField(branch.City, branchFieldLimit) ||
		!validBranchField(branch.Zip, zipLimit) {
		return Branch{}, false
	}
	if branch.Name == "" {
		branch.Name = branch.Zip
	}
	if branch.Name == "" {
		return Branch{}, false
	}
	if branch.Country != "" && !branch.Country.Valid() {
		return Branch{}, false
	}
	branch.Latitude, branch.Longitude = branchCoordinates(wire)
	return branch, true
}

func branchCoordinates(wire branchWire) (*float64, *float64) {
	for _, pair := range [][2]coordinate{{wire.Lat, wire.Lng}, {wire.Latitude, wire.Longitude}} {
		latitude, longitude := pair[0], pair[1]
		if latitude.set && longitude.set && validCoordinates(latitude.value, longitude.value) {
			return &latitude.value, &longitude.value
		}
	}
	return nil, nil
}

func validCoordinates(latitude, longitude float64) bool {
	return latitude >= -90 && latitude <= 90 &&
		longitude >= -180 && longitude <= 180 &&
		(latitude != 0 || longitude != 0)
}

func validBranchField(value string, maximum int) bool {
	return utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= maximum &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func isJSONResponse(response *http.Response) bool {
	if response == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return err == nil && mediaType == contentTypeJSON
}

func validLabelURL(client *Client, raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	if parsed.RawQuery != "" && parsed.RawQuery != labelQueryZPL {
		return false
	}
	if len(client.labelHosts) > 0 {
		return labelHostAllowed(parsed, client.labelHosts)
	}
	if client.loopback {
		return parsed.Scheme+"://"+parsed.Host == client.origin
	}
	return parsed.Scheme == schemeHTTPS &&
		(parsed.Host == "pdf.balikobot.cz" ||
			strings.HasSuffix(parsed.Host, ".balikobot.cz"))
}

func labelHostAllowed(parsed *url.URL, allowedHosts []string) bool {
	if !labelSchemeAllowed(parsed) {
		return false
	}
	host := strings.ToLower(parsed.Host)
	hostname := strings.ToLower(parsed.Hostname())
	for _, allowed := range allowedHosts {
		if labelHostMatches(host, hostname, allowed) {
			return true
		}
	}
	return false
}

func labelHostMatches(host, hostname, allowed string) bool {
	if strings.HasPrefix(allowed, ".") {
		return strings.HasSuffix(hostname, allowed)
	}
	return host == allowed
}

func labelSchemeAllowed(parsed *url.URL) bool {
	if parsed.Scheme == schemeHTTPS {
		return true
	}
	if parsed.Scheme != schemeHTTP {
		return false
	}
	address := net.ParseIP(parsed.Hostname())
	return address != nil && address.IsLoopback()
}

func readResponseBody(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	if ctx == nil || reader == nil || limit <= 0 {
		return nil, ErrInvalidResponse
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(errResponseRead, ctxErr)
	}
	body, err := readLimitedBody(ctx, reader, limit)
	if err != nil {
		return nil, errors.Join(errResponseRead, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(errResponseRead, ctxErr)
	}
	return body, nil
}

func readLimitedBody(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	limited := io.LimitReader(reader, int64(limit)+1)
	buffer := make([]byte, min(limit+1, readBufferSize))
	body := make([]byte, 0, min(limit, readBufferSize))
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		count, readErr := limited.Read(buffer)
		body = append(body, buffer[:count]...)
		if len(body) > limit {
			return nil, errResponseLimit
		}
		switch {
		case errors.Is(readErr, io.EOF):
			return body, nil
		case readErr != nil:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, readErr
		}
	}
}

func (client *Client) decodeResponse(ctx context.Context, response *http.Response, target any) error {
	body, err := readResponseBody(ctx, response.Body, client.maxResponseBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func transientError(response *http.Response) error {
	return &TransientError{RetryAfter: retryAfterHeader(response)}
}

func retryAfterHeader(response *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After")))
	if err != nil || seconds < minRetryAfterSeconds {
		return 0
	}
	if seconds > maxRetryAfterSeconds {
		seconds = maxRetryAfterSeconds
	}
	return time.Duration(seconds) * time.Second
}

func topLevelStatusError(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var status responseStatus
	if json.Unmarshal(raw, &status) != nil || !status.set {
		return ErrInvalidResponse
	}
	switch status.value {
	case http.StatusOK, http.StatusAlreadyReported:
		return nil
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return &TransientError{}
	case http.StatusBadRequest,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusLocked,
		http.StatusNotImplemented:
		return ErrRejected
	default:
		return ErrInvalidResponse
	}
}

func jsonRawKind(raw []byte) byte {
	index := 0
	for index < len(raw) {
		char := raw[index]
		if char != ' ' && char != '\t' && char != '\r' && char != '\n' {
			break
		}
		index++
	}
	if index >= len(raw) {
		return 0
	}
	return jsonValueKind(raw[index])
}

func jsonValueKind(first byte) byte {
	switch {
	case first == '{' || first == '[' || first == '"' ||
		first == 't' || first == 'f' || first == 'n':
		return first
	case first == '-' || first >= '0' && first <= '9':
		return '0'
	default:
		return 0
	}
}
