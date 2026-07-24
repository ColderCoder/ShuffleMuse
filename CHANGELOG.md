# Changelog

All notable changes to ShuffleMuse are documented in this file.

The project follows [Semantic Versioning](https://semver.org/).

## [0.1.1] - 2026-07-24

### Added

- Support for case-insensitive `folder.jpg` and `folder.png` directory artwork
  after the existing `cover.jpg` and `cover.png` candidates.
- Lazy display of embedded TITLE metadata in Now Playing while preserving the
  original relative file path.
- Deployment guidance for trusted real-IP headers and combined LAN plus
  Cloudflare Tunnel access.

### Changed

- Metadata and embedded-cover discovery now share one bounded ffprobe,
  file-identity cache, and in-flight request.

### Fixed

- Prevented the playlist navigation strip from exposing list content through a
  spacing seam.
- Kept playback and stream-mode controls from overlapping at intermediate
  viewport widths.
- Made the codec-side file path in the bottom player link to its Browse
  directory.

## [0.1.0] - 2026-07-23

### Added

- Initial public release.
- Self-hosted Go and Vue music library with original-file and Opus playback.
- Randomized server-side queues, tag filtering, favorite-only continuous play,
  search, paginated browsing, tag export, and missing-file management.
- Read-only music access, real-time cover conversion, metadata inspection, and
  bounded FFmpeg concurrency.
- Hardened Docker deployment with a read-only root filesystem and persistent
  tag database volume.
- Multi-architecture GHCR image publication for `linux/amd64` and
  `linux/arm64`.

[0.1.1]: https://github.com/ColderCoder/ShuffleMuse/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ColderCoder/ShuffleMuse/tree/v0.1.0
