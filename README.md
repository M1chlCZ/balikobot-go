# balikobot-go

A zero-dependency Go client for the Balíkobot shipping API v2. The module uses
only the Go standard library and the `balikobot` package. The client covers
branches, packages, labels, tracking, pickup orders and account capabilities.

## Install

```sh
go get github.com/m1chlcz/balikobot-go
```

Go 1.27 or later is required. Only a loopback test server may use the `http`
scheme; every other base URL must use `https`.

## Quick start

Create a client with your API user and API key. The client sends the
credentials as HTTP Basic authentication. Then add a package, get a label URL
and read the tracking status.

```go
package main

import (
	"context"
	"log"
	"time"

	balikobot "github.com/m1chlcz/balikobot-go"
	"github.com/m1chlcz/balikobot-go/carrier"
	"github.com/m1chlcz/balikobot-go/country"
	"github.com/m1chlcz/balikobot-go/currency"
)

func main() {
	client, err := balikobot.New(balikobot.Config{
		User:    "api-user",
		APIKey:  "api-key",
		Timeout: 15 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.AddPackage(ctx, carrier.PPL, balikobot.AddPackageRequest{
		EID:         "order-2026-000123-S1",
		ServiceType: "1",
		RecName:     "Example Recipient",
		RecStreet:   "Example 1",
		RecCity:     "Praha",
		RecZip:      "11000",
		RecCountry:  country.CZ,
		RecPhone:    "+420777000000",
		WeightKG:    1.5,
		LengthCM:    30,
		WidthCM:     20,
		HeightCM:    10,
		Price:       1000,
		CODCurrency: currency.CZK,
	})
	if err != nil {
		log.Fatal(err)
	}

	labelURL, err := client.Labels(ctx, carrier.PPL, result.PackageID)
	if err != nil {
		log.Fatal(err)
	}
	log.Print(labelURL)

	status, err := client.TrackStatus(ctx, carrier.PPL, result.CarrierID)
	if err != nil {
		log.Fatal(err)
	}
	log.Print(status.StatusText)
}
```

## Enums

Carrier, currency and country values are typed, not plain strings. Three
subpackages hold the types:

| Package | Type | Format | Common constants |
| --- | --- | --- | --- |
| `carrier` | `carrier.Code` | `^[a-z0-9]{2,32}$` | `PPL`, `DPD`, `DPDCZ`, `DPDSK`, `GEIS`, `GLS`, `INTIME`, `CP`, `CESKAPOSTA`, `BALIKOVNA`, `ZASILKOVNA`, `SP`, `ULOZENKA` |
| `currency` | `currency.Code` | ISO 4217 `^[A-Z]{3}$` | `CZK`, `EUR`, `USD`, `GBP`, `PLN`, `HUF`, `RON`, `BGN`, `HRK`, `CHF`, `NOK`, `SEK`, `DKK` |
| `country` | `country.Code` | ISO 3166-1 alpha-2 `^[A-Z]{2}$` | EU member states plus `GB`, `CH`, `NO`, `IS`, `LI`, `UA`, `RS`, `BA`, `ME`, `MK`, `AL`, `TR`, `US`, `CA` |

Every type has a `Valid` and a `String` method. Use `FromString` for a value
that has no constant. The function trims whitespace, normalizes the case and
accepts any well-formed code, so custom carriers, currencies and countries
work:

```go
custom, err := carrier.FromString("MyCarrier99")
if err != nil {
	return err
}
result, err := client.AddPackage(ctx, custom, request)
```

The types keep the wire values compile-time safe: a function that expects a
`carrier.Code` rejects a bare string, and a mistyped constant fails the build.
The JSON form stays a plain string, so the wire contract does not change.

ADD still accepts only `currency.CZK` and `currency.EUR` as `cod_currency`,
because the carriers require one of those two values. Other well-formed
currency codes are rejected before the request.

### Version 0.2.0

Version 0.2.0 changes every carrier, currency and country parameter from
`string` to the typed codes. This is a breaking change from v0.1.0. Update
every call to use the new types, for example
`client.Labels(ctx, carrier.PPL, packageID)`.

## Methods

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `Branches` | `GET /{carrier}/branches/...` | Lists the branches of a service and country |
| `AddPackage` | `POST /{carrier}/add` | Creates one package. ADD is idempotent on `eid` |
| `Overview` | `GET /{carrier}/overview` | Lists the packages that ORDER has not closed |
| `Labels` | `POST /{carrier}/labels` | Gets a fresh label URL for one package |
| `OrderViewLabels` | `GET /{carrier}/orderview/{order_id}` | Gets the label URL of a closed order |
| `DownloadLabel` | `GET` the label URL | Downloads the label body |
| `TrackStatus` | `POST /{carrier}/trackstatus` | Reads the tracking status of one package |
| `OrderBatch` | `POST /{carrier}/order` | Hands one package to the carrier batch |
| `DropPackage` | `POST /{carrier}/drop` | Removes one package before ORDER |
| `OrderPickup` | `POST /{carrier}/orderpickup` | Books one physical collection |
| `WhoAmI` | `GET /info/whoami` | Reads the account and carrier data |
| `ActivatedServices` | `GET /{carrier}/activatedservices` | Lists the activated services |
| `Countries` | `GET /{carrier}/countries4service` | Lists the destination countries |
| `COD` | `GET /{carrier}/cod4services` | Lists the cash-on-delivery destinations |
| `CarrierCapabilities` | `GET` the discovery endpoints | Discovers the contracted carriers and services |
| `ResolveBranchID` | none | Chooses the branch id or the branch zip for an ADD request |

## Errors

The client returns six sentinel errors. Use `errors.Is` to test them.

| Error | Meaning | Action |
| --- | --- | --- |
| `ErrInvalidRequest` | The arguments are not valid. The client sent no request. | Correct the input. Do not retry. |
| `ErrRejected` | The provider refused the data permanently. | Correct the data. Do not retry. |
| `ErrUnavailable` | The provider is unavailable, or the request never left the client. | Retry later. |
| `ErrNotFound` | The carrier has no tracking data yet. | Poll again later. |
| `ErrAmbiguous` | A mutating call can have reached the provider. | Reconcile with `Overview`. Then retry. |
| `ErrInvalidResponse` | The answer violates the protocol. | Inspect the provider. Do not retry blindly. |

When a JSON endpoint answer carries a `Retry-After` header, `ErrUnavailable`
wraps a `TransientError` with the `RetryAfter` field. Use `errors.As` to read
it. `OrderPickup` classifies an HTTP 429 answer as `ErrRejected`. The branch,
label download and capability calls return the bare `ErrUnavailable`.

```go
var transient *balikobot.TransientError
if errors.As(err, &transient) && transient.RetryAfter > 0 {
	time.Sleep(transient.RetryAfter)
}
```

The client sends no automatic retry. The caller controls the retry policy.

## Response limits

The client reads every JSON body with a hard limit of 8 MiB. Set
`Config.MaxResponseBytes` to change the limit. Label downloads use a fixed
limit of 4 MiB. The client refuses redirects. It compares the response
`Content-Type` with the expected media type before it decodes the body.

## Account mode

Set `Config.LiveAccount` to `true` or `false` to verify the account before each
mutating call. The client calls WHOAMI and compares the `live_account` flag. A
mismatch blocks the write before the client sends it. A successful result stays
valid for five minutes. If `Config.LiveAccount` is nil, the client skips this
check.

## Label hosts

The client accepts label URLs only from the Balíkobot label hosts, or from the
base URL origin for a loopback test server. Set `Config.LabelHosts` to replace
the default allowlist with other hosts. A leading dot selects a subdomain
suffix match; it does not match the bare domain.

## Development

Run the checks from the module root:

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

The tests use `httptest` servers. They use no real credentials and no external
network.

## License

MIT. See [LICENSE](LICENSE).
