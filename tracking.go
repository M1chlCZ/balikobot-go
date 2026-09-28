package balikobot

import (
	"context"
	"errors"
	"net/http"

	"github.com/m1chlcz/balikobot-go/carrier"
)

// TrackStatusResult is the latest provider tracking status of one package.
type TrackStatusResult struct {
	// StatusID is the raw provider status code, for example "1", "1.2" or
	// "-1".
	StatusID string
	// StatusText is the provider status description.
	StatusText string
}

// OrderResult is the batch reference returned by ORDER.
type OrderResult struct {
	// OrderID is the provider batch identifier.
	OrderID string
}

type trackStatusID struct {
	raw string
	set bool
}

// UnmarshalJSON keeps the raw provider status code and rejects every shape
// that is not a documented decimal status.
func (id *trackStatusID) UnmarshalJSON(raw []byte) error {
	if jsonRawKind(raw) != '0' || !trackIDPattern.MatchString(string(raw)) {
		return errors.New("invalid Balíkobot track status id")
	}
	id.raw = string(raw)
	id.set = true
	return nil
}

type trackStatusResponse struct {
	Status   responseStatus       `json:"status"`
	Packages []trackStatusPackage `json:"packages"`
}

type trackStatusPackage struct {
	CarrierID  string         `json:"carrier_id"`
	StatusID   trackStatusID  `json:"status_id"`
	StatusIDV2 trackStatusID  `json:"status_id_v2"`
	Name       string         `json:"name"`
	StatusText string         `json:"status_text"`
	Status     responseStatus `json:"status"`
}

type orderResponse struct {
	Status  responseStatus `json:"status"`
	OrderID string         `json:"order_id"`
}

type dropResponse struct {
	Status responseStatus `json:"status"`
}

// TrackStatus calls the TRACKSTATUS method for one carrier tracking number and
// returns the raw provider status. The documented response wraps per-package
// entries in a packages array. A package entry or HTTP answer with status 404
// means that the carrier has no tracking data yet and maps to ErrNotFound.
// The call is read-only, so transport failures are safe to retry.
func (client *Client) TrackStatus(
	ctx context.Context,
	carrierCode carrier.Code,
	carrierID string,
) (TrackStatusResult, error) {
	if client == nil || client.client == nil ||
		!carrierCode.Valid() ||
		!validPackageID(carrierID) {
		return TrackStatusResult{}, ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+string(carrierCode)+"/trackstatus",
		map[string]any{"carrier_ids": []string{carrierID}},
	)
	if err != nil {
		return TrackStatusResult{}, &TransientError{}
	}
	defer response.Body.Close()
	if err = trackHTTPStatus(response); err != nil {
		return TrackStatusResult{}, err
	}
	return client.decodeTrackStatus(ctx, response, carrierID)
}

func trackHTTPStatus(response *http.Response) error {
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return &TransientError{}
	}
	if statusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return &TransientError{}
		}
		return ErrInvalidResponse
	}
	return nil
}

func (client *Client) decodeTrackStatus(
	ctx context.Context,
	response *http.Response,
	carrierID string,
) (TrackStatusResult, error) {
	var result trackStatusResponse
	decodeErr := client.decodeResponse(ctx, response, &result)
	if errors.Is(decodeErr, errResponseRead) {
		return TrackStatusResult{}, &TransientError{}
	}
	if decodeErr != nil {
		return TrackStatusResult{}, ErrInvalidResponse
	}
	if result.Status.set {
		switch result.Status.value {
		case http.StatusOK:
		case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
			return TrackStatusResult{}, &TransientError{}
		case http.StatusNotFound:
			return TrackStatusResult{}, ErrNotFound
		default:
			return TrackStatusResult{}, ErrInvalidResponse
		}
	}
	if len(result.Packages) != 1 || result.Packages[0].CarrierID != carrierID {
		return TrackStatusResult{}, ErrInvalidResponse
	}
	entry := result.Packages[0]
	if !entry.Status.set && (!result.Status.set || entry.Name == "") {
		return TrackStatusResult{}, ErrInvalidResponse
	}
	status := entry.Status.value
	if !entry.Status.set {
		status = result.Status.value
	}
	switch status {
	case http.StatusOK:
		id, description := entry.StatusID, entry.StatusText
		if entry.StatusIDV2.set {
			id = entry.StatusIDV2
		}
		if entry.Name != "" {
			description = entry.Name
		}
		if !id.set || description == "" ||
			!validBranchField(description, branchFieldLimit) {
			return TrackStatusResult{}, ErrInvalidResponse
		}
		return TrackStatusResult{
			StatusID:   id.raw,
			StatusText: description,
		}, nil
	case http.StatusNotFound:
		return TrackStatusResult{}, ErrNotFound
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return TrackStatusResult{}, &TransientError{}
	case http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusLocked:
		return TrackStatusResult{}, ErrRejected
	default:
		return TrackStatusResult{}, ErrInvalidResponse
	}
}

// OrderBatch calls the ORDER method, which hands one package over to the
// carrier batch. ORDER is idempotent on package_ids: a repeated closure of the
// same dataset returns status 208 with the original order id, so an idempotent
// retry after an ambiguous answer replays the original record instead of
// closing the package twice.
func (client *Client) OrderBatch(
	ctx context.Context,
	carrierCode carrier.Code,
	packageID string,
) (OrderResult, error) {
	if client == nil || client.client == nil ||
		!carrierCode.Valid() ||
		!validPackageID(packageID) {
		return OrderResult{}, ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+string(carrierCode)+"/order",
		map[string]any{fieldPackageIDs: []string{packageID}},
	)
	if err != nil {
		return OrderResult{}, dispatchError(err)
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return OrderResult{}, transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return OrderResult{}, &TransientError{}
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			return OrderResult{}, ErrAmbiguous
		}
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return OrderResult{}, ErrRejected
		}
		return OrderResult{}, ErrInvalidResponse
	}
	var result orderResponse
	if client.decodeResponse(ctx, response, &result) != nil || !result.Status.set {
		return OrderResult{}, ErrAmbiguous
	}
	switch result.Status.value {
	case http.StatusOK, http.StatusAlreadyReported:
		if !validBranchField(result.OrderID, identifierLimit) || result.OrderID == "" {
			return OrderResult{}, ErrAmbiguous
		}
		return OrderResult{OrderID: result.OrderID}, nil
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return OrderResult{}, &TransientError{}
	case http.StatusBadRequest,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusLocked:
		return OrderResult{}, ErrRejected
	default:
		return OrderResult{}, ErrAmbiguous
	}
}

// DropPackage calls the DROP method for one package that has not entered
// ORDER. A body status 404 means that the package is already gone and the call
// succeeds. A body status 405 marks a package that was already handed to the
// batch and maps to ErrRejected. An ambiguous DROP answer must be reconciled
// through Overview before any retry.
func (client *Client) DropPackage(
	ctx context.Context,
	carrierCode carrier.Code,
	packageID string,
) error {
	if client == nil || client.client == nil ||
		!carrierCode.Valid() ||
		!validPackageID(packageID) {
		return ErrInvalidRequest
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+string(carrierCode)+"/drop",
		map[string]any{fieldPackageIDs: []string{packageID}},
	)
	if err != nil {
		return dispatchError(err)
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	if statusCode == http.StatusTooManyRequests {
		return transientError(response)
	}
	if statusCode >= http.StatusInternalServerError {
		return &TransientError{}
	}
	if statusCode != http.StatusOK || !isJSONResponse(response) {
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			return ErrAmbiguous
		}
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return ErrRejected
		}
		return ErrInvalidResponse
	}
	var result dropResponse
	if client.decodeResponse(ctx, response, &result) != nil || !result.Status.set {
		return ErrAmbiguous
	}
	switch result.Status.value {
	case http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusUpgradeRequired, http.StatusServiceUnavailable:
		return &TransientError{}
	case http.StatusBadRequest,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusLocked:
		return ErrRejected
	default:
		return ErrAmbiguous
	}
}
