package balikobot

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// TrackingNotification is one package status change delivered to a Balíkobot
// tracking webhook. At least one of PackageID, EID and CarrierID identifies
// the package; StatusID and StatusText describe the new provider status.
type TrackingNotification struct {
	// PackageID is the Balíkobot package reference. It can be empty when the
	// notification identifies the package another way.
	PackageID string
	// EID is the external package reference. It can be empty when the
	// notification identifies the package another way.
	EID string
	// CarrierID is the carrier tracking number. It can be empty when the
	// notification identifies the package another way.
	CarrierID string
	// StatusID is the raw provider status code, for example "1", "1.2" or
	// "-1". The detailed status_id_v2 value wins when both are present.
	StatusID string
	// StatusText is the provider status description.
	StatusText string
}

type trackingNotificationWire struct {
	PackageID  trackingIdentifier `json:"package_id"`
	EID        trackingIdentifier `json:"eid"`
	CarrierID  trackingIdentifier `json:"carrier_id"`
	StatusID   trackStatusID      `json:"status_id"`
	StatusIDV2 trackStatusID      `json:"status_id_v2"`
	Name       string             `json:"name"`
	StatusText string             `json:"status_text"`
}

type trackingIdentifier struct {
	value string
	set   bool
}

// UnmarshalJSON accepts string and integer identifiers and treats null as
// absent. Any other shape or an unusable identifier fails the whole payload.
func (id *trackingIdentifier) UnmarshalJSON(raw []byte) error {
	if jsonRawKind(raw) == 'n' {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		if jsonRawKind(raw) != '0' {
			return errors.New("invalid Balíkobot tracking identifier")
		}
		encoded := string(raw)
		if strings.ContainsAny(encoded, ".eE") {
			return errors.New("invalid Balíkobot tracking identifier")
		}
		text = encoded
	}
	if text == "" {
		return nil
	}
	if !validBranchField(text, identifierLimit) {
		return errors.New("invalid Balíkobot tracking identifier")
	}
	id.value, id.set = text, true
	return nil
}

// ParseTrackingNotifications parses the JSON body of a Balíkobot tracking
// webhook and returns one notification per status change. The body can be a
// single notification object or an array of objects; an empty array returns an
// empty result. Every entry must identify the package through package_id, eid
// or carrier_id. Every entry must also carry a decimal status_id or
// status_id_v2 and a status description in name or status_text. A malformed
// body, a missing required field or more than trackingBatchLimit entries
// returns ErrInvalidResponse.
func ParseTrackingNotifications(body []byte) ([]TrackingNotification, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, ErrInvalidResponse
	}
	var entries []trackingNotificationWire
	switch jsonValueKind(trimmed[0]) {
	case '{':
		var single trackingNotificationWire
		if err := json.Unmarshal(trimmed, &single); err != nil {
			return nil, ErrInvalidResponse
		}
		entries = append(entries, single)
	case '[':
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, ErrInvalidResponse
		}
	default:
		return nil, ErrInvalidResponse
	}
	if len(entries) > trackingBatchLimit {
		return nil, ErrInvalidResponse
	}
	notifications := make([]TrackingNotification, 0, len(entries))
	for _, entry := range entries {
		notification, ok := normalizeTrackingNotification(entry)
		if !ok {
			return nil, ErrInvalidResponse
		}
		notifications = append(notifications, notification)
	}
	return notifications, nil
}

func normalizeTrackingNotification(wire trackingNotificationWire) (TrackingNotification, bool) {
	if !wire.CarrierID.set && !wire.PackageID.set && !wire.EID.set {
		return TrackingNotification{}, false
	}
	status := wire.StatusID
	if wire.StatusIDV2.set {
		status = wire.StatusIDV2
	}
	if !status.set {
		return TrackingNotification{}, false
	}
	text := wire.Name
	if text == "" {
		text = wire.StatusText
	}
	if text == "" || !validBranchField(text, branchFieldLimit) {
		return TrackingNotification{}, false
	}
	return TrackingNotification{
		PackageID:  wire.PackageID.value,
		EID:        wire.EID.value,
		CarrierID:  wire.CarrierID.value,
		StatusID:   status.raw,
		StatusText: text,
	}, true
}
