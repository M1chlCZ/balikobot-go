//nolint:testpackage // White-box tests share the in-package helpers.
package balikobot

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestParseTrackingNotificationsAcceptsSingleObject(t *testing.T) {
	t.Parallel()

	notifications, err := ParseTrackingNotifications([]byte(`{
		"package_id": "add-ppl-8728035",
		"eid": "order-2026-000123-S1",
		"carrier_id": "DR1536622512M",
		"status_id": 1,
		"status_id_v2": 1.2,
		"name": "Zásilka byla doručena příjemci.",
		"status_text": "Doručeno"
	}`))
	if err != nil || len(notifications) != 1 {
		t.Fatalf("ParseTrackingNotifications = %#v, %v", notifications, err)
	}
	got := notifications[0]
	if got.PackageID != "add-ppl-8728035" ||
		got.EID != "order-2026-000123-S1" ||
		got.CarrierID != "DR1536622512M" ||
		got.StatusID != "1.2" ||
		got.StatusText != "Zásilka byla doručena příjemci." {
		t.Fatalf("notification = %#v", got)
	}
}

func TestParseTrackingNotificationsAcceptsArray(t *testing.T) {
	t.Parallel()

	notifications, err := ParseTrackingNotifications([]byte(`[
		{
			"carrier_id": "TRACK-1",
			"status_id": 2,
			"status_text": "Zásilka v přepravě."
		},
		{
			"package_id": 42,
			"eid": 12345678,
			"carrier_id": "TRACK-2",
			"status_id_v2": 2.2,
			"name": "Zásilka byla převzata do přepravy."
		},
		{
			"carrier_id": "TRACK-3",
			"status_id": -1,
			"name": "Zásilka zatím nebyla předána dopravci."
		}
	]`))
	if err != nil || len(notifications) != 3 {
		t.Fatalf("ParseTrackingNotifications = %#v, %v", notifications, err)
	}
	first := notifications[0]
	if first.CarrierID != "TRACK-1" || first.PackageID != "" || first.EID != "" ||
		first.StatusID != "2" || first.StatusText != "Zásilka v přepravě." {
		t.Fatalf("first notification = %#v", first)
	}
	second := notifications[1]
	if second.PackageID != "42" || second.EID != "12345678" ||
		second.CarrierID != "TRACK-2" || second.StatusID != "2.2" ||
		second.StatusText != "Zásilka byla převzata do přepravy." {
		t.Fatalf("second notification = %#v", second)
	}
	third := notifications[2]
	if third.StatusID != "-1" || third.StatusText != "Zásilka zatím nebyla předána dopravci." {
		t.Fatalf("third notification = %#v", third)
	}
}

func TestParseTrackingNotificationsToleratesNullIdentifiers(t *testing.T) {
	t.Parallel()

	notifications, err := ParseTrackingNotifications([]byte(`{
		"package_id": null,
		"eid": null,
		"carrier_id": "TRACK-1",
		"status_id": 2,
		"status_text": "Zásilka v přepravě."
	}`))
	if err != nil || len(notifications) != 1 {
		t.Fatalf("ParseTrackingNotifications = %#v, %v", notifications, err)
	}
	if notifications[0].PackageID != "" || notifications[0].EID != "" ||
		notifications[0].CarrierID != "TRACK-1" {
		t.Fatalf("notification = %#v", notifications[0])
	}
}

func TestParseTrackingNotificationsAcceptsEmptyArray(t *testing.T) {
	t.Parallel()

	notifications, err := ParseTrackingNotifications([]byte(`[]`))
	if err != nil || len(notifications) != 0 {
		t.Fatalf("ParseTrackingNotifications = %#v, %v", notifications, err)
	}
}

func TestParseTrackingNotificationsRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty body":                "",
		"whitespace only":           " \t\n ",
		"null body":                 "null",
		"scalar body":               `"status"`,
		"malformed json":            `{"carrier_id":`,
		"missing identity":          `{"status_id":1,"name":"Delivered"}`,
		"empty identity":            `{"package_id":"","eid":"","carrier_id":"","status_id":1,"name":"Delivered"}`,
		"null identity":             `{"package_id":null,"eid":null,"carrier_id":null,"status_id":1,"name":"Delivered"}`,
		"missing status":            `{"carrier_id":"TRACK-1","name":"Delivered"}`,
		"quoted status":             `{"carrier_id":"TRACK-1","status_id":"1.2","name":"Delivered"}`,
		"too precise status":        `{"carrier_id":"TRACK-1","status_id":1.2345,"name":"Delivered"}`,
		"null trailing status":      `{"carrier_id":"TRACK-1","status_id":1,"status_id_v2":null,"name":"Delivered"}`,
		"missing text":              `{"carrier_id":"TRACK-1","status_id":1}`,
		"empty text":                `{"carrier_id":"TRACK-1","status_id":1,"name":"","status_text":""}`,
		"overlong identity":         `{"carrier_id":"` + strings.Repeat("x", identifierLimit+1) + `","status_id":1,"name":"Delivered"}`,
		"overlong numeric identity": `{"carrier_id":` + strings.Repeat("1", identifierLimit+1) + `,"status_id":1,"name":"Delivered"}`,
		"control character":         "{\"carrier_id\":\"TRACK-1\",\"status_id\":1,\"name\":\"Delivered\\nNow\"}",
		"boolean identity":          `{"carrier_id":true,"status_id":1,"name":"Delivered"}`,
		"object identity":           `{"carrier_id":{},"status_id":1,"name":"Delivered"}`,
		"array entry not object":    `[1]`,
		"null array entry":          `[null]`,
		"one bad entry fails all": `[
			{"carrier_id":"TRACK-1","status_id":1,"name":"Delivered"},
			{"status_id":1,"name":"Delivered"}
		]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			notifications, err := ParseTrackingNotifications([]byte(body))
			if !errors.Is(err, ErrInvalidResponse) || notifications != nil {
				t.Fatalf("ParseTrackingNotifications(%q) = %#v, %v", body, notifications, err)
			}
		})
	}

	t.Run("too many entries", func(t *testing.T) {
		t.Parallel()
		var body strings.Builder
		body.WriteString("[")
		for index := 0; index <= trackingBatchLimit; index++ {
			if index > 0 {
				body.WriteString(",")
			}
			body.WriteString(`{"carrier_id":"TRACK-`)
			body.WriteString(strconv.Itoa(index))
			body.WriteString(`","status_id":2,"name":"Transit"}`)
		}
		body.WriteString("]")
		if _, err := ParseTrackingNotifications([]byte(body.String())); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("too many entries error = %v, want ErrInvalidResponse", err)
		}
	})
}
