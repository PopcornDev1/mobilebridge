# Changelog

## v0.1.0

Initial public Android release.

Highlights:

- ADB device discovery and devtools socket resolution
- local CDP bridge with `/json/version`, `/json/list`, and page websocket proxying
- reconnect handling with serialized recovery and permanent-loss signaling
- synthetic `MobileBridge.*` gesture methods:
  - `tap`
  - `swipe`
  - `pinch`
  - `longPress`
- Go helpers for network emulation
- best-effort device enrichment for Android version, SDK, RAM, and battery
- optional screen recording and filtered logcat capture from the CLI
- hermetic test coverage including reconnect, proxy, cache, gesture, recording, and soak paths

Known scope limits:

- Android only
- single downstream client per proxied page
- first matching Chrome/WebView devtools socket only
