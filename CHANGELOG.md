# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
