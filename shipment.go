package balikobot

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"unicode/utf8"
)

// AddPackageRequest is one package for the ADD method. The external reference
// EID makes ADD idempotent: a repeated request with an already stored EID
// returns the original record.
type AddPackageRequest struct {
	// EID is the external package reference. It must contain 8 to 40
	// alphanumeric or dash characters.
	EID string `json:"eid"`
	// ServiceType is the carrier service code, for example "1" or "VMCZ".
	ServiceType string `json:"service_type"`
	// RecName is the recipient name. RecName or RecFirm must be set.
	RecName string `json:"rec_name,omitempty"`
	// RecFirm is the recipient company. RecName or RecFirm must be set.
	RecFirm string `json:"rec_firm,omitempty"`
	// RecStreet is the recipient street. It is required.
	RecStreet string `json:"rec_street"`
	// RecCity is the recipient city. It is required.
	RecCity string `json:"rec_city"`
	// RecZip is the recipient postal code. It is required.
	RecZip string `json:"rec_zip"`
	// RecCountry is the ISO 3166-1 alpha-2 destination country. It is
	// required.
	RecCountry string `json:"rec_country"`
	// RecPhone is the recipient phone. RecPhone or RecEmail must be set.
	RecPhone string `json:"rec_phone,omitempty"`
	// RecEmail is the recipient email. RecPhone or RecEmail must be set.
	RecEmail string `json:"rec_email,omitempty"`
	// BranchID is the pickup branch reference for branch delivery.
	BranchID string `json:"branch_id,omitempty"`
	// WeightKG is the package weight in kilograms. It must be positive and at
	// most 10000.
	WeightKG float64 `json:"weight"`
	// LengthCM is the package length in centimeters. It must be positive and
	// at most 1000.
	LengthCM float64 `json:"length"`
	// WidthCM is the package width in centimeters. It must be positive and at
	// most 1000.
	WidthCM float64 `json:"width"`
	// HeightCM is the package height in centimeters. It must be positive and
	// at most 1000.
	HeightCM float64 `json:"height"`
	// Price is the declared value in the currency of CODCurrency. It must not
	// be negative and at most 100000000.
	Price float64 `json:"price"`
	// CODPrice is the cash-on-delivery amount. It must not be negative and at
	// most 100000000. A positive amount requires VS.
	CODPrice float64 `json:"cod_price,omitzero"`
	// CODCurrency is the cash-on-delivery currency. Balíkobot carriers
	// validate this field even without a COD amount, so "CZK" or "EUR" is
	// required on every ADD.
	CODCurrency string `json:"cod_currency,omitempty"`
	// VS is the cash-on-delivery variable symbol. It must be set exactly when
	// CODPrice is positive and must be below 10000000000.
	VS *int64 `json:"vs,omitempty"`
}

// AddPackageResult is the accepted package record returned by ADD.
type AddPackageResult struct {
	// PackageID is the Balíkobot package reference used by labels, ORDER and
	// DROP.
	PackageID string
	// CarrierID is the carrier tracking number.
	CarrierID string
	// LabelURL is the provider label URL for this package.
	LabelURL string
}

// OverviewPackage is one open package entry returned by OVERVIEW.
type OverviewPackage struct {
	// EID is the external package reference stored by the provider.
	EID string
	// PackageID is the Balíkobot package reference.
	PackageID string
	// CarrierID is the carrier tracking number.
	CarrierID string
	// LabelURL is the provider label URL for this package.
	LabelURL string
}

type addResponse struct {
	Status   json.RawMessage    `json:"status"`
	Packages []addPackageStatus `json:"packages"`
}

type addPackageStatus struct {
	EID       string         `json:"eid"`
	Status    responseStatus `json:"status"`
	PackageID packageID      `json:"package_id"`
	CarrierID string         `json:"carrier_id"`
	LabelURL  string         `json:"label_url"`
}

type overviewResponse struct {
	Status   json.RawMessage         `json:"status"`
	Packages []overviewPackageStatus `json:"packages"`
}

type overviewPackageStatus struct {
	EID       string    `json:"eid"`
	PackageID packageID `json:"package_id"`
	CarrierID string    `json:"carrier_id"`
	LabelURL  string    `json:"label_url"`
}

type labelsResponse struct {
	Status    json.RawMessage `json:"status"`
	LabelsURL string          `json:"labels_url"`
}

type orderViewResponse struct {
	Status     json.RawMessage `json:"status"`
	OrderID    string          `json:"order_id"`
	PackageIDs []packageID     `json:"package_ids"`
	LabelsURL  string          `json:"labels_url"`
}

type packageID struct {
	value string
	set   bool
}

// UnmarshalJSON accepts string and integer package ids. Any other shape or an
// unusable identifier fails the whole response.
func (id *packageID) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if jsonRawKind(raw) != '0' {
			return errors.New("invalid Balíkobot package id")
		}
		encoded := string(raw)
		if strings.ContainsAny(encoded, ".eE") {
			return errors.New("invalid Balíkobot package id")
		}
		text = encoded
	}
	if !utf8.ValidString(text) || len(text) == 0 || len(text) > identifierLimit ||
		strings.ContainsAny(text, "\r\n\x00") {
		return errors.New("invalid Balíkobot package id")
	}
	id.value = text
	id.set = true
	return nil
}

func validateAddPackage(request AddPackageRequest) error {
	if !eidPattern.MatchString(request.EID) {
		return ErrInvalidRequest
	}
	return validateAddSnapshot(request)
}

func validateAddSnapshot(request AddPackageRequest) error {
	if err := validateAddIdentity(request); err != nil {
		return err
	}
	if err := validateAddRecipient(request); err != nil {
		return err
	}
	return validateAddParcel(request)
}

func validateAddIdentity(request AddPackageRequest) error {
	switch {
	case !eidPattern.MatchString(request.EID):
		return ErrInvalidRequest
	case !servicePattern.MatchString(request.ServiceType):
		return ErrInvalidRequest
	case request.RecName == "" && request.RecFirm == "":
		return ErrInvalidRequest
	case request.RecPhone == "" && request.RecEmail == "":
		return ErrInvalidRequest
	case request.CODCurrency != "CZK" && request.CODCurrency != "EUR":
		return ErrInvalidRequest
	case request.CODPrice > 0 && request.VS == nil:
		return ErrInvalidRequest
	case request.CODPrice == 0 && request.VS != nil:
		return ErrInvalidRequest
	}
	return nil
}

func validateAddRecipient(request AddPackageRequest) error {
	for _, field := range []string{
		request.RecName, request.RecFirm, request.RecStreet, request.RecCity,
		request.RecZip, request.RecPhone, request.RecEmail,
	} {
		if !validBranchField(field, addFieldLimit) {
			return ErrInvalidRequest
		}
	}
	switch {
	case request.RecStreet == "" || request.RecCity == "" ||
		request.RecZip == "" || !countryPattern.MatchString(request.RecCountry):
		return ErrInvalidRequest
	case request.BranchID != "" && !branchIDPattern.MatchString(request.BranchID):
		return ErrInvalidRequest
	}
	return nil
}

func validateAddParcel(request AddPackageRequest) error {
	switch {
	case request.WeightKG <= 0 || request.WeightKG > 10_000 ||
		request.LengthCM <= 0 || request.LengthCM > 1_000 ||
		request.WidthCM <= 0 || request.WidthCM > 1_000 ||
		request.HeightCM <= 0 || request.HeightCM > 1_000:
		return ErrInvalidRequest
	case request.Price < 0 || request.Price > 100_000_000 ||
		request.CODPrice < 0 || request.CODPrice > 100_000_000:
		return ErrInvalidRequest
	case request.VS != nil && (*request.VS < 0 || *request.VS >= trackReferenceModulus):
		return ErrInvalidRequest
	}
	return nil
}

// ResolveBranchID derives the branch_id that ADD expects from a stored branch
// of a carrier. Česká pošta and Slovenská pošta use the branch ZIP without
// spaces, the Uloženka CP_NP service does the same, PPL strips the KM prefix,
// and every other carrier uses the stored branch id unchanged.
func ResolveBranchID(carrier, service, branchID, branchZip string) string {
	switch carrier {
	case "cp", "sp":
		return strings.ReplaceAll(branchZip, " ", "")
	case "ulozenka":
		if service == "CP_NP" {
			return strings.ReplaceAll(branchZip, " ", "")
		}
		return branchID
	case "ppl":
		return strings.TrimPrefix(branchID, "KM")
	default:
		return branchID
	}
}

// AddPackage calls the ADD method with one package. ADD is idempotent on EID:
// a repeated request with an already stored EID returns status 208 together
// with the original record, which maps to a successful result.
func (client *Client) AddPackage(
	ctx context.Context,
	carrier string,
	request AddPackageRequest,
) (AddPackageResult, error) {
	if client == nil || client.client == nil ||
		!carrierPattern.MatchString(carrier) ||
		validateAddPackage(request) != nil {
		return AddPackageResult{}, ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+carrier+"/add",
		map[string]any{"packages": []AddPackageRequest{request}},
	)
	if err != nil {
		return AddPackageResult{}, dispatchError(err)
	}
	defer response.Body.Close()
	if err = addHTTPStatus(response); err != nil {
		return AddPackageResult{}, err
	}
	return client.decodeAddPackage(ctx, response, request.EID)
}

func addHTTPStatus(response *http.Response) error {
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return &TransientError{}
	}
	if statusCode != http.StatusOK {
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			return ErrAmbiguous
		}
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return ErrRejected
		}
		return ErrInvalidResponse
	}
	if !isJSONResponse(response) {
		return ErrAmbiguous
	}
	return nil
}

func (client *Client) decodeAddPackage(
	ctx context.Context,
	response *http.Response,
	expectedEID string,
) (AddPackageResult, error) {
	var result addResponse
	if client.decodeResponse(ctx, response, &result) != nil || len(result.Status) == 0 {
		return AddPackageResult{}, ErrAmbiguous
	}
	if err := topLevelStatusError(result.Status); err != nil {
		if errors.Is(err, ErrInvalidResponse) {
			return AddPackageResult{}, ErrAmbiguous
		}
		return AddPackageResult{}, err
	}
	if len(result.Packages) != 1 || result.Packages[0].EID != expectedEID {
		return AddPackageResult{}, ErrAmbiguous
	}
	entry := result.Packages[0]
	if !entry.Status.set {
		return AddPackageResult{}, ErrAmbiguous
	}
	switch entry.Status.value {
	case http.StatusOK, http.StatusAlreadyReported:
		if !entry.PackageID.set || entry.CarrierID == "" ||
			!validBranchField(entry.CarrierID, identifierLimit) ||
			!validLabelURL(client, entry.LabelURL) {
			return AddPackageResult{}, ErrAmbiguous
		}
		return AddPackageResult{
			PackageID: entry.PackageID.value,
			CarrierID: entry.CarrierID,
			LabelURL:  entry.LabelURL,
		}, nil
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return AddPackageResult{}, &TransientError{}
	case http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusLocked,
		http.StatusNotImplemented:
		return AddPackageResult{}, ErrRejected
	default:
		return AddPackageResult{}, ErrAmbiguous
	}
}

// Overview calls the OVERVIEW method, which lists the packages of a carrier
// that have not yet been closed by ORDER. matchEID names the single entry
// whose integrity is required for reconciliation: a malformed entry with a
// different EID is skipped, while a malformed matching entry fails the call.
func (client *Client) Overview(
	ctx context.Context,
	carrier string,
	matchEID string,
) ([]OverviewPackage, error) {
	if client == nil || client.client == nil ||
		!carrierPattern.MatchString(carrier) {
		return nil, ErrInvalidRequest
	}
	response, err := client.request(ctx, http.MethodGet, "/"+carrier+"/overview", nil)
	if err != nil {
		return nil, dispatchError(err)
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return nil, transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return nil, &TransientError{}
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return nil, ErrRejected
		}
		return nil, ErrInvalidResponse
	}
	var result overviewResponse
	decodeErr := client.decodeResponse(ctx, response, &result)
	if errors.Is(decodeErr, errResponseRead) {
		return nil, ErrAmbiguous
	}
	if decodeErr != nil {
		return nil, ErrInvalidResponse
	}
	if err := topLevelStatusError(result.Status); err != nil {
		return nil, err
	}
	packages := make([]OverviewPackage, 0, len(result.Packages))
	for _, entry := range result.Packages {
		valid := entry.EID != "" && entry.PackageID.set && entry.CarrierID != "" &&
			validBranchField(entry.CarrierID, identifierLimit) &&
			validLabelURL(client, entry.LabelURL)
		if !valid {
			if entry.EID == matchEID {
				return nil, ErrInvalidResponse
			}
			continue
		}
		packages = append(packages, OverviewPackage{
			EID:       entry.EID,
			PackageID: entry.PackageID.value,
			CarrierID: entry.CarrierID,
			LabelURL:  entry.LabelURL,
		})
	}
	return packages, nil
}

// Labels asks for a fresh aggregate label URL of one package that has not
// entered ORDER yet.
func (client *Client) Labels(
	ctx context.Context,
	carrier string,
	packageID string,
) (string, error) {
	if client == nil || client.client == nil ||
		!carrierPattern.MatchString(carrier) ||
		!validPackageID(packageID) {
		return "", ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+carrier+"/labels",
		map[string]any{fieldPackageIDs: []string{packageID}},
	)
	if err != nil {
		return "", &TransientError{}
	}
	defer response.Body.Close()
	if err := labelLookupResponseError(response); err != nil {
		return "", err
	}
	var result labelsResponse
	decodeErr := client.decodeResponse(ctx, response, &result)
	if errors.Is(decodeErr, errResponseRead) {
		return "", &TransientError{}
	}
	if decodeErr != nil || len(result.Status) == 0 {
		return "", ErrInvalidResponse
	}
	if err := topLevelStatusError(result.Status); err != nil {
		return "", err
	}
	if !validLabelURL(client, result.LabelsURL) {
		return "", ErrInvalidResponse
	}
	return result.LabelsURL, nil
}

// OrderViewLabels retrieves the label URL of an already closed ORDER. The
// returned URL is accepted only when the response confirms both the requested
// order id and the membership of the requested package id.
func (client *Client) OrderViewLabels(
	ctx context.Context,
	carrier string,
	orderID string,
	packageID string,
) (string, error) {
	if client == nil || client.client == nil ||
		!carrierPattern.MatchString(carrier) ||
		!validPackageID(orderID) ||
		!validPackageID(packageID) {
		return "", ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodGet, "/"+carrier+"/orderview/"+url.PathEscape(orderID), nil,
	)
	if err != nil {
		return "", &TransientError{}
	}
	defer response.Body.Close()
	if err := labelLookupResponseError(response); err != nil {
		return "", err
	}
	var result orderViewResponse
	decodeErr := client.decodeResponse(ctx, response, &result)
	if errors.Is(decodeErr, errResponseRead) {
		return "", &TransientError{}
	}
	if decodeErr != nil {
		return "", ErrInvalidResponse
	}
	if err := topLevelStatusError(result.Status); err != nil {
		return "", err
	}
	member := false
	for _, candidate := range result.PackageIDs {
		if candidate.set && candidate.value == packageID {
			member = true
			break
		}
	}
	if result.OrderID != orderID || !member ||
		!validLabelURL(client, result.LabelsURL) {
		return "", ErrInvalidResponse
	}
	return result.LabelsURL, nil
}

func labelLookupResponseError(response *http.Response) error {
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return &TransientError{}
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return ErrRejected
		}
		return ErrInvalidResponse
	}
	return nil
}

// DownloadLabel fetches a provider label URL server-to-server. The body is
// read once with a hard byte limit and validated against the declared label
// media type. The URL host must pass the client label allowlist. The returned
// body is a copy owned by the caller.
func (client *Client) DownloadLabel(
	ctx context.Context,
	labelURL string,
) ([]byte, string, error) {
	if client == nil || client.client == nil || !validLabelURL(client, labelURL) {
		return nil, "", ErrInvalidRequest
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, labelURL, nil)
	if err != nil {
		return nil, "", ErrInvalidRequest
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, "", &TransientError{}
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError {
		return nil, "", &TransientError{}
	}
	if statusCode != http.StatusOK {
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return nil, "", ErrRejected
		}
		return nil, "", ErrInvalidResponse
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", ErrInvalidResponse
	}
	body, err := readResponseBody(ctx, response.Body, labelResponseLimit)
	if err != nil {
		if errors.Is(err, errResponseLimit) {
			return nil, "", ErrInvalidResponse
		}
		return nil, "", &TransientError{}
	}
	if len(body) == 0 {
		return nil, "", ErrInvalidResponse
	}
	switch mediaType {
	case mimeTypePDF:
		if !strings.HasPrefix(string(body), pdfMagicPrefix) {
			return nil, "", ErrInvalidResponse
		}
	case mimeTypeZPL:
		if !strings.HasPrefix(string(body), zplMagicPrefix) {
			return nil, "", ErrInvalidResponse
		}
	default:
		return nil, "", ErrInvalidResponse
	}
	return append([]byte(nil), body...), mediaType, nil
}

func validPackageID(packageID string) bool {
	return packageID != "" && validBranchField(packageID, identifierLimit)
}

func dispatchError(err error) error {
	if errors.Is(err, errAccountUnverified) {
		return ErrUnavailable
	}
	var dnsError *net.DNSError
	if errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, &dnsError) {
		return ErrUnavailable
	}
	return ErrAmbiguous
}
