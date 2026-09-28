package balikobot

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"
)

// PickupRequest is one physical collection booking for ORDERPICKUP.
type PickupRequest struct {
	// Date is the collection date in the canonical "2006-01-02" format.
	Date string
	// WeightKG is the total collection weight in kilograms. It must be
	// positive and at most 100000. DPD and DPDCZ send it; PPL takes the
	// weight from its carrier configuration.
	WeightKG float64
	// PackageCount is the number of packages. It must be positive and at most
	// 10000. DPD and DPDCZ send it; PPL takes it from its carrier
	// configuration.
	PackageCount int
	// Note is the optional collection note. It must contain at most 255 valid
	// characters without line breaks. DPD and DPDCZ send it as "message";
	// PPL sends it as "note".
	Note string
}

// PickupResult is the confirmed collection booking.
type PickupResult struct {
	// ProviderID is the provider pickup reference. PPL returns it; DPD and
	// DPDCZ leave it empty.
	ProviderID string
	// Confirmed reports the provider confirmation. DPD and DPDCZ always
	// confirm on success.
	Confirmed bool
}

type pickupResponse struct {
	Status     responseStatus `json:"status"`
	ProviderID string         `json:"pickup_order_id"`
	Confirmed  *bool          `json:"confirmed"`
}

// OrderPickup calls the ORDERPICKUP method and books one physical collection,
// separately from the shipment data handover performed by ORDER. The call
// performs exactly one HTTP attempt. DPD and DPDCZ take the collection address
// from the carrier configuration; PPL also defaults its contact information to
// that configuration.
func (client *Client) OrderPickup(
	ctx context.Context,
	carrier string,
	request PickupRequest,
) (PickupResult, error) {
	if client == nil || client.client == nil {
		return PickupResult{}, ErrInvalidRequest
	}
	if !validPickupRequest(carrier, request) {
		return PickupResult{}, ErrRejected
	}
	response, err := client.request(
		ctx, http.MethodPost, "/"+carrier+"/orderpickup", pickupBody(carrier, request),
	)
	if err != nil {
		if errors.Is(err, errAccountUnverified) {
			return PickupResult{}, ErrRejected
		}
		return PickupResult{}, ErrAmbiguous
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PickupResult{}, pickupStatusError(response.StatusCode)
	}
	if !isJSONResponse(response) {
		return PickupResult{}, ErrAmbiguous
	}
	var result pickupResponse
	if client.decodeResponse(ctx, response, &result) != nil || !result.Status.set {
		return PickupResult{}, ErrAmbiguous
	}
	if result.Status.value != http.StatusOK {
		return PickupResult{}, pickupStatusError(result.Status.value)
	}
	if carrier != CarrierPPL {
		return PickupResult{Confirmed: true}, nil
	}
	if result.Confirmed == nil || result.ProviderID == "" ||
		!validBranchField(result.ProviderID, identifierLimit) {
		return PickupResult{}, ErrAmbiguous
	}
	return PickupResult{ProviderID: result.ProviderID, Confirmed: *result.Confirmed}, nil
}

func validPickupRequest(carrier string, request PickupRequest) bool {
	if carrier != CarrierDPDCZ && carrier != CarrierDPD && carrier != CarrierPPL {
		return false
	}
	date, err := time.Parse(time.DateOnly, request.Date)
	if err != nil || date.Format(time.DateOnly) != request.Date {
		return false
	}
	return request.PackageCount > 0 && request.PackageCount <= pickupPackageLimit &&
		request.WeightKG > 0 && request.WeightKG <= pickupWeightLimit &&
		!math.IsNaN(request.WeightKG) &&
		validBranchField(request.Note, pickupNoteLimit)
}

func pickupBody(carrier string, request PickupRequest) map[string]any {
	body := map[string]any{"date": request.Date}
	noteField := "note"
	if carrier != CarrierPPL {
		body["weight"] = request.WeightKG
		body["package_count"] = request.PackageCount
		noteField = "message"
	}
	if request.Note != "" {
		body[noteField] = request.Note
	}
	return body
}

func pickupStatusError(status int) error {
	switch status {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests:
		return ErrRejected
	default:
		return ErrAmbiguous
	}
}
