# Changelog

All notable changes to ShuffleMuse are documented in this file.

The project follows [Semantic Versioning](https://semver.org/).

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

[0.1.0]: https://github.com/ColderCoder/ShuffleMuse/tree/v0.1.0
