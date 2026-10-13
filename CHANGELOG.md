# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Optional in-memory discovery cache with single-flight refresh
  de-duplication. `DiscoveryCache` provides `Get`, `Set` and `Invalidate`
  with a TTL; `NewCapabilityCache` caches `CarrierCapabilities` results and
  `NewWhoAmICache` caches `WhoAmI` results. The cache is a per-process
  accelerator and never replaces a durable cross-process store.
- Streaming label downloads through `DownloadLabelTo` and
  `DownloadBatchLabelsTo`. They keep the label host allowlist, the fixed
  4 MiB limit, the media-type and magic validation and the error
  classification of the buffered calls, and they validate the magic prefix
  before the first byte reaches the writer.
- Opt-in retry for read-only calls through `Config.RetryPolicy` (total
  attempts, base backoff, optional jitter). The policy applies to `Branches`,
  `Overview`, `Labels`, `OrderViewLabels`, `TrackStatus`, the capability
  discovery calls and the label downloads; `AddPackage`, `OrderBatch`,
  `DropPackage` and `OrderPickup` are never retried automatically.
  `TransientError.RetryAfter` is honored as the floor of the next delay.

## [0.3.0] - 2026-10-23

### Added

- Batch label export through `DownloadBatchLabels`. It requests fresh labels
  for one or more package ids, follows the returned label URL and validates
  the body. A single package id returns a PDF or ZPL body; several package
  ids return a ZIP archive.
- Typed tracking webhook parsing through `TrackingNotification` and
  `ParseTrackingNotifications`. The parser accepts a single status-change
  object or an array of objects and rejects malformed input with
  `ErrInvalidResponse`.

[0.3.0]: https://github.com/M1chlCZ/balikobot-go/releases/tag/v0.3.0
