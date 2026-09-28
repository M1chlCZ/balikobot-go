package balikobot

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
	"github.com/m1chlcz/balikobot-go/currency"
)

// WhoAmI is the account information returned by the WHOAMI method.
type WhoAmI struct {
	// Status is the top-level provider status.
	Status int
	// LiveAccount reports whether the credentials belong to a live account.
	// It is nil when the provider omits the flag.
	LiveAccount *bool
	// Carriers lists the carriers contracted by the account.
	Carriers []WhoAmICarrier
}

// WhoAmICarrier is one contracted carrier of the account.
type WhoAmICarrier struct {
	// Slug is the carrier code used in request paths.
	Slug carrier.Code
	// Name is the carrier display name. It can be empty.
	Name string
}

// Carrier aggregates the discovered services of one contracted carrier.
type Carrier struct {
	// CarrierCode is the carrier code used in request paths.
	CarrierCode carrier.Code
	// Services lists the activated services of the carrier.
	Services []Service
}

// Service describes one activated carrier service.
type Service struct {
	// Code is the provider service code.
	Code string
	// Name is the provider service name.
	Name string
	// HomeDelivery reports home delivery support. Nil means that the
	// provider did not declare the flag.
	HomeDelivery *bool
	// BoxDelivery reports box delivery support. Nil means that the provider
	// did not declare the flag.
	BoxDelivery *bool
	// PickupPointsDelivery reports pickup point delivery support. Nil means
	// that the provider did not declare the flag.
	PickupPointsDelivery *bool
	// Countries maps the destination country codes supported by the service.
	// CarrierCapabilities keeps only EU destinations, matching the reference
	// integration.
	Countries map[country.Code]bool
	// COD lists the supported cash-on-delivery destinations. The combined
	// discovery leaves it empty because it does not request the optional COD
	// dictionary.
	COD []CODCapability
}

// CODCapability is one cash-on-delivery destination of a service.
type CODCapability struct {
	// Country is the ISO 3166-1 alpha-2 destination country.
	Country country.Code
	// Currency is the three-letter currency code.
	Currency currency.Code
	// MaxAmountMinor is the maximum cash-on-delivery amount in minor units,
	// for example 149995 for 1499.95 CZK.
	MaxAmountMinor int64
}

// ActivatedServices is the ACTIVATEDSERVICES answer of one carrier.
type ActivatedServices struct {
	// ActiveParcel is the provider flag that marks active parcel shipping.
	// It is nil when the provider omits the flag.
	ActiveParcel *bool
	// Services lists the activated services. The Countries and COD fields are
	// empty; use CarrierCapabilities for the combined discovery.
	Services []Service
}

// ServiceCountries is one entry of the COUNTRIES4SERVICE answer.
type ServiceCountries struct {
	// ServiceType is the provider service code.
	ServiceType string
	// Countries lists the destination country codes exactly as sent, with
	// surrounding whitespace trimmed and letters upper-cased.
	Countries []country.Code
}

// ServiceCOD is one entry of the COD4SERVICES answer.
type ServiceCOD struct {
	// ServiceType is the provider service code.
	ServiceType string
	// Countries lists the normalized cash-on-delivery destinations.
	Countries []CODCapability
}

type whoAmIWire struct {
	Status      responseStatus          `json:"status"`
	LiveAccount *bool                   `json:"live_account"`
	Carriers    []capabilityCarrierWire `json:"carriers"`
}

type capabilityCarrierWire struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type activatedServicesCapabilityResponse struct {
	Status       responseStatus         `json:"status"`
	ActiveParcel *bool                  `json:"active_parcel"`
	ServiceTypes []activatedServiceWire `json:"service_types"`
}

type activatedServiceWire struct {
	Code                 serviceCode `json:"service_type"`
	Name                 string      `json:"name"`
	HomeDelivery         *bool       `json:"home_delivery"`
	BoxDelivery          *bool       `json:"box_delivery"`
	PickupPointsDelivery *bool       `json:"pickup_points_delivery"`
}

type countriesCapabilityResponse struct {
	Status       responseStatus         `json:"status"`
	ServiceTypes []countriesServiceWire `json:"service_types"`
}

type countriesServiceWire struct {
	Code      serviceCode `json:"service_type"`
	Countries []string    `json:"countries"`
}

type codCapabilityResponse struct {
	Status       responseStatus   `json:"status"`
	ServiceTypes []codServiceWire `json:"service_types"`
}

type codServiceWire struct {
	Code      serviceCode      `json:"service_type"`
	Countries []codCountryWire `json:"countries"`
}

type codCountryWire struct {
	Country  string          `json:"country"`
	Currency string          `json:"currency"`
	MaxPrice json.RawMessage `json:"max_price"`
}

type serviceCode struct {
	value string
	set   bool
}

// UnmarshalJSON accepts string and integer service codes, which both occur in
// live provider answers.
func (code *serviceCode) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if jsonRawKind(raw) != '0' {
			return errors.New("invalid Balíkobot service code")
		}
		encoded := string(raw)
		if strings.ContainsAny(encoded, ".eE") {
			return errors.New("invalid Balíkobot service code")
		}
		text = encoded
	}
	if !utf8.ValidString(text) || text == "" || len(text) > 64 ||
		strings.ContainsAny(text, "\r\n\x00") {
		return errors.New("invalid Balíkobot service code")
	}
	code.value, code.set = text, true
	return nil
}

// UnmarshalJSON accepts both documented COUNTRIES4SERVICE shapes: a plain
// array and a sparse object indexed by numeric positions that are unrelated to
// the service code inside each entry.
func (response *countriesCapabilityResponse) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Status       responseStatus  `json:"status"`
		ServiceTypes json.RawMessage `json:"service_types"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	services, err := decodeCountriesServices(wire.ServiceTypes)
	if err != nil {
		return err
	}
	*response = countriesCapabilityResponse{Status: wire.Status, ServiceTypes: services}
	return nil
}

func decodeCountriesServices(raw json.RawMessage) ([]countriesServiceWire, error) {
	if len(raw) == 0 || jsonRawKind(raw) == 'n' {
		return nil, nil
	}
	if jsonRawKind(raw) == '[' {
		var services []countriesServiceWire
		if err := json.Unmarshal(raw, &services); err != nil {
			return nil, err
		}
		return services, nil
	}
	if jsonRawKind(raw) != '{' {
		return nil, ErrInvalidResponse
	}
	var keyed map[string]countriesServiceWire
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil, err
	}
	if len(keyed) > capabilityServiceLimit {
		return nil, ErrInvalidResponse
	}
	keys := make([]string, 0, len(keyed))
	for key := range keyed {
		if key == "" || strings.Trim(key, "0123456789") != "" {
			return nil, ErrInvalidResponse
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	services := make([]countriesServiceWire, 0, len(keys))
	for _, key := range keys {
		services = append(services, keyed[key])
	}
	return services, nil
}

type capabilityStatusResponse interface {
	statusValue() (int, bool)
}

func (response *whoAmIWire) statusValue() (int, bool) {
	return response.Status.value, response.Status.set
}

func (response *activatedServicesCapabilityResponse) statusValue() (int, bool) {
	return response.Status.value, response.Status.set
}

func (response *countriesCapabilityResponse) statusValue() (int, bool) {
	return response.Status.value, response.Status.set
}

func (response *codCapabilityResponse) statusValue() (int, bool) {
	return response.Status.value, response.Status.set
}

// WhoAmI calls the WHOAMI method and returns the account information.
func (client *Client) WhoAmI(ctx context.Context) (WhoAmI, error) {
	if client == nil || client.client == nil {
		return WhoAmI{}, ErrInvalidRequest
	}
	var wire whoAmIWire
	if err := client.capabilityGET(ctx, "/info/whoami", &wire, false); err != nil {
		return WhoAmI{}, err
	}
	carriers := make([]WhoAmICarrier, 0, len(wire.Carriers))
	for _, entry := range wire.Carriers {
		carriers = append(carriers, WhoAmICarrier{
			Slug: carrier.Code(entry.Slug),
			Name: entry.Name,
		})
	}
	return WhoAmI{
		Status:      wire.Status.value,
		LiveAccount: wire.LiveAccount,
		Carriers:    carriers,
	}, nil
}

// ActivatedServices calls the ACTIVATEDSERVICES method of one carrier and
// returns the normalized activated services. When the provider reports that
// parcel shipping is inactive, the service list is empty.
func (client *Client) ActivatedServices(ctx context.Context, carrierCode carrier.Code) (ActivatedServices, error) {
	if client == nil || client.client == nil || !carrierCode.Valid() {
		return ActivatedServices{}, ErrInvalidRequest
	}
	var wire activatedServicesCapabilityResponse
	if err := client.capabilityGET(
		ctx, "/"+string(carrierCode)+"/activatedservices", &wire, false,
	); err != nil {
		return ActivatedServices{}, err
	}
	services, _, err := normalizeActivatedServices(wire)
	if err != nil {
		return ActivatedServices{}, err
	}
	return ActivatedServices{ActiveParcel: wire.ActiveParcel, Services: services}, nil
}

// Countries calls the COUNTRIES4SERVICE method of one carrier and returns the
// supported destination countries per service. Every country sent by the
// provider is kept.
func (client *Client) Countries(ctx context.Context, carrierCode carrier.Code) ([]ServiceCountries, error) {
	if client == nil || client.client == nil || !carrierCode.Valid() {
		return nil, ErrInvalidRequest
	}
	var wire countriesCapabilityResponse
	if err := client.capabilityGET(
		ctx, "/"+string(carrierCode)+"/countries4service", &wire, false,
	); err != nil {
		return nil, err
	}
	if len(wire.ServiceTypes) > capabilityServiceLimit {
		return nil, ErrInvalidResponse
	}
	result := make([]ServiceCountries, 0, len(wire.ServiceTypes))
	for _, entry := range wire.ServiceTypes {
		if !entry.Code.set {
			return nil, ErrInvalidResponse
		}
		code := strings.TrimSpace(entry.Code.value)
		if !validCapabilityServiceCode(code) || len(entry.Countries) > capabilityServiceLimit {
			return nil, ErrInvalidResponse
		}
		countries := make([]country.Code, 0, len(entry.Countries))
		for _, rawCountry := range entry.Countries {
			countries = append(countries, country.Code(strings.ToUpper(strings.TrimSpace(rawCountry))))
		}
		result = append(result, ServiceCountries{ServiceType: code, Countries: countries})
	}
	return result, nil
}

// COD calls the COD4SERVICES method of one carrier and returns the normalized
// cash-on-delivery destinations per service. Every country sent by the
// provider is kept. A carrier without the optional dictionary returns an empty
// list.
func (client *Client) COD(ctx context.Context, carrierCode carrier.Code) ([]ServiceCOD, error) {
	if client == nil || client.client == nil || !carrierCode.Valid() {
		return nil, ErrInvalidRequest
	}
	var wire codCapabilityResponse
	if err := client.capabilityGET(
		ctx, "/"+string(carrierCode)+"/cod4services", &wire, true,
	); err != nil {
		return nil, err
	}
	if len(wire.ServiceTypes) > capabilityServiceLimit {
		return nil, ErrInvalidResponse
	}
	result := make([]ServiceCOD, 0, len(wire.ServiceTypes))
	for _, entry := range wire.ServiceTypes {
		if !entry.Code.set {
			return nil, ErrInvalidResponse
		}
		code := strings.TrimSpace(entry.Code.value)
		if !validCapabilityServiceCode(code) || len(entry.Countries) > capabilityServiceLimit {
			return nil, ErrInvalidResponse
		}
		countries, err := normalizeCODCountries(entry.Countries)
		if err != nil {
			return nil, err
		}
		result = append(result, ServiceCOD{ServiceType: code, Countries: countries})
	}
	return result, nil
}

// CarrierCapabilities discovers the contracted carriers and their activated
// services in one run. Without an argument it discovers every carrier of the
// account; an explicit empty scope discovers none. At most one scope slice may
// be passed, and every requested carrier must belong to the account. The
// returned destinations are restricted to EU countries, matching the reference
// integration.
func (client *Client) CarrierCapabilities(
	ctx context.Context,
	carrierScope ...[]carrier.Code,
) ([]Carrier, error) {
	if client == nil || client.client == nil {
		return nil, ErrInvalidRequest
	}
	whoami, err := client.verifiedWhoAmI(ctx, false)
	if err != nil {
		return nil, err
	}
	carriers, err := scopedCapabilityCarriers(whoami.Carriers, carrierScope)
	if err != nil {
		return nil, err
	}
	for index := range carriers {
		activated := activatedServicesCapabilityResponse{}
		countries := countriesCapabilityResponse{}
		carrierCode := carriers[index].CarrierCode
		if fetchErr := client.capabilityGET(
			ctx, "/"+string(carrierCode)+"/activatedservices", &activated, false,
		); fetchErr != nil {
			return nil, fetchErr
		}
		if fetchErr := client.capabilityGET(
			ctx, "/"+string(carrierCode)+"/countries4service", &countries, false,
		); fetchErr != nil {
			return nil, fetchErr
		}
		services, normalizeErr := normalizeCapabilities(
			activated, countries, codCapabilityResponse{},
		)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		carriers[index].Services = services
	}
	return carriers, nil
}

func (client *Client) capabilityGET(
	ctx context.Context,
	path string,
	target capabilityStatusResponse,
	allowUnsupported bool,
) error {
	response, err := client.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if allowUnsupported && response.StatusCode == http.StatusNotImplemented {
		return nil
	}
	if response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= http.StatusInternalServerError {
		return ErrUnavailable
	}
	if response.StatusCode != http.StatusOK || !isJSONResponse(response) {
		return ErrInvalidResponse
	}
	decodeErr := client.decodeResponse(ctx, response, target)
	if decodeErr != nil {
		if errors.Is(decodeErr, errResponseRead) {
			return ErrUnavailable
		}
		return ErrInvalidResponse
	}
	status, ok := target.statusValue()
	if !ok || status != http.StatusOK {
		if allowUnsupported && ok && status == http.StatusNotImplemented {
			return nil
		}
		return ErrInvalidResponse
	}
	return nil
}

func scopedCapabilityCarriers(
	contracted []capabilityCarrierWire,
	scope [][]carrier.Code,
) ([]Carrier, error) {
	if len(contracted) > capabilityCarrierLimit || len(scope) > 1 {
		return nil, ErrInvalidResponse
	}
	available := make(map[carrier.Code]bool, len(contracted))
	for _, entry := range contracted {
		code, err := carrier.FromString(entry.Slug)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		available[code] = true
	}
	requested := available
	if len(scope) == 1 {
		requested = make(map[carrier.Code]bool, len(scope[0]))
		for _, candidate := range scope[0] {
			code, err := carrier.FromString(string(candidate))
			if err != nil || !available[code] {
				return nil, ErrInvalidResponse
			}
			requested[code] = true
		}
	}
	carriers := make([]Carrier, 0, len(requested))
	for _, entry := range contracted {
		code, err := carrier.FromString(entry.Slug)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		if requested[code] {
			carriers = append(carriers, Carrier{CarrierCode: code})
			delete(requested, code)
		}
	}
	return carriers, nil
}

func normalizeCapabilities(
	activated activatedServicesCapabilityResponse,
	countries countriesCapabilityResponse,
	cod codCapabilityResponse,
) ([]Service, error) {
	services, index, err := normalizeActivatedServices(activated)
	if err != nil {
		return nil, err
	}
	if countriesErr := mergeCapabilityCountries(services, index, countries); countriesErr != nil {
		return nil, countriesErr
	}
	if codErr := mergeCapabilityCOD(services, index, cod); codErr != nil {
		return nil, codErr
	}
	return services, nil
}

func normalizeActivatedServices(
	activated activatedServicesCapabilityResponse,
) ([]Service, map[string]int, error) {
	if len(activated.ServiceTypes) > capabilityServiceLimit {
		return nil, nil, ErrInvalidResponse
	}
	services := make([]Service, 0, len(activated.ServiceTypes))
	index := make(map[string]int, len(activated.ServiceTypes))
	for _, wire := range activated.ServiceTypes {
		service, err := normalizeActivatedService(wire)
		if err != nil {
			return nil, nil, err
		}
		if activated.ActiveParcel != nil && !*activated.ActiveParcel {
			continue
		}
		if previous, ok := index[service.Code]; ok {
			if !sameService(services[previous], service) {
				return nil, nil, ErrInvalidResponse
			}
			continue
		}
		index[service.Code] = len(services)
		services = append(services, service)
	}
	return services, index, nil
}

func mergeCapabilityCountries(
	services []Service,
	index map[string]int,
	countries countriesCapabilityResponse,
) error {
	if len(countries.ServiceTypes) > capabilityServiceLimit {
		return ErrInvalidResponse
	}
	for _, wire := range countries.ServiceTypes {
		if !wire.Code.set {
			return ErrInvalidResponse
		}
		code := strings.TrimSpace(wire.Code.value)
		if !validCapabilityServiceCode(code) || len(wire.Countries) > capabilityServiceLimit {
			return ErrInvalidResponse
		}
		serviceIndex, ok := index[code]
		if !ok {
			continue
		}
		if services[serviceIndex].Countries == nil {
			services[serviceIndex].Countries = make(map[country.Code]bool)
		}
		for _, rawCountry := range wire.Countries {
			normalized := country.Code(strings.ToUpper(strings.TrimSpace(rawCountry)))
			if isEUCountryCode(normalized) {
				services[serviceIndex].Countries[normalized] = true
			}
		}
	}
	return nil
}

func mergeCapabilityCOD(
	services []Service,
	index map[string]int,
	cod codCapabilityResponse,
) error {
	if len(cod.ServiceTypes) > capabilityServiceLimit {
		return ErrInvalidResponse
	}
	for _, wire := range cod.ServiceTypes {
		if !wire.Code.set {
			return ErrInvalidResponse
		}
		code := strings.TrimSpace(wire.Code.value)
		if !validCapabilityServiceCode(code) || len(wire.Countries) > capabilityServiceLimit {
			return ErrInvalidResponse
		}
		serviceIndex, ok := index[code]
		if !ok {
			continue
		}
		entries, err := mergeCODCountries(services[serviceIndex].COD, wire.Countries)
		if err != nil {
			return err
		}
		services[serviceIndex].COD = entries
	}
	return nil
}

func mergeCODCountries(
	entries []CODCapability,
	countries []codCountryWire,
) ([]CODCapability, error) {
	for _, entry := range countries {
		capability, err := normalizeCODCapability(entry)
		if err != nil {
			return nil, err
		}
		if !isEUCountryCode(capability.Country) {
			continue
		}
		if existing := findCODCapability(entries, capability.Country, capability.Currency); existing != nil {
			if *existing != capability {
				return nil, ErrInvalidResponse
			}
			continue
		}
		entries = append(entries, capability)
	}
	return entries, nil
}

func normalizeCODCountries(countries []codCountryWire) ([]CODCapability, error) {
	entries := make([]CODCapability, 0, len(countries))
	for _, entry := range countries {
		capability, err := normalizeCODCapability(entry)
		if err != nil {
			return nil, err
		}
		if existing := findCODCapability(entries, capability.Country, capability.Currency); existing != nil {
			if *existing != capability {
				return nil, ErrInvalidResponse
			}
			continue
		}
		entries = append(entries, capability)
	}
	return entries, nil
}

func normalizeActivatedService(wire activatedServiceWire) (Service, error) {
	if !wire.Code.set {
		return Service{}, ErrInvalidResponse
	}
	code := strings.TrimSpace(wire.Code.value)
	name := strings.TrimSpace(wire.Name)
	if !validCapabilityServiceCode(code) || name == "" ||
		len([]rune(name)) > capabilityNameLimit ||
		strings.ContainsFunc(name, unicode.IsControl) {
		return Service{}, ErrInvalidResponse
	}
	return Service{
		Code:                 code,
		Name:                 name,
		HomeDelivery:         wire.HomeDelivery,
		BoxDelivery:          wire.BoxDelivery,
		PickupPointsDelivery: wire.PickupPointsDelivery,
		Countries:            make(map[country.Code]bool),
	}, nil
}

func validCapabilityServiceCode(code string) bool {
	return code != "" && len([]rune(code)) <= capabilityServiceLimit &&
		!strings.ContainsAny(code, "/\\\x00\r\n") &&
		!strings.ContainsFunc(code, unicode.IsControl)
}

func sameService(left, right Service) bool {
	return left.Code == right.Code && left.Name == right.Name &&
		boolPointerEqual(left.HomeDelivery, right.HomeDelivery) &&
		boolPointerEqual(left.BoxDelivery, right.BoxDelivery) &&
		boolPointerEqual(left.PickupPointsDelivery, right.PickupPointsDelivery)
}

func boolPointerEqual(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func normalizeCODCapability(wire codCountryWire) (CODCapability, error) {
	countryCode, err := country.FromString(wire.Country)
	if err != nil {
		return CODCapability{}, ErrInvalidResponse
	}
	currencyCode, err := currency.FromString(wire.Currency)
	if err != nil {
		return CODCapability{}, ErrInvalidResponse
	}
	minor, ok := majorPriceToMinor(wire.MaxPrice)
	if !ok {
		return CODCapability{}, ErrInvalidResponse
	}
	return CODCapability{
		Country:        countryCode,
		Currency:       currencyCode,
		MaxAmountMinor: minor,
	}, nil
}

func majorPriceToMinor(raw json.RawMessage) (int64, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || len(text) > 64 {
		return 0, false
	}
	if exponent := strings.IndexAny(text, "eE"); exponent >= 0 {
		encodedExponent := text[exponent+1:]
		value, err := strconv.ParseInt(encodedExponent, 10, 64)
		if err != nil || value < -decimalExponentLimit || value > decimalExponentLimit {
			return 0, false
		}
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(text)
	if !ok || rational.Sign() < 0 {
		return 0, false
	}
	rational.Mul(rational, big.NewRat(minorUnitsPerCurrency, 1))
	if rational.Denom().Cmp(big.NewInt(1)) != 0 ||
		rational.Num().Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return 0, false
	}
	return rational.Num().Int64(), true
}

func findCODCapability(entries []CODCapability, target country.Code, targetCurrency currency.Code) *CODCapability {
	for index := range entries {
		if entries[index].Country == target && entries[index].Currency == targetCurrency {
			return &entries[index]
		}
	}
	return nil
}

func isEUCountryCode(code country.Code) bool {
	switch code {
	case country.AT, country.BE, country.BG, country.HR, country.CY, country.CZ, country.DK,
		country.EE, country.FI, country.FR, country.DE, country.GR, country.HU, country.IE,
		country.IT, country.LV, country.LT, country.LU, country.MT, country.NL, country.PL,
		country.PT, country.RO, country.SK, country.SI, country.ES, country.SE:
		return true
	}
	return false
}
