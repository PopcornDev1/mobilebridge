# mobilebridge

A CDP (Chrome DevTools Protocol) bridge for Android Chrome. It lets any CDP client — Puppeteer, Playwright, browser-use, OpenClaw, or your own automation — drive a real Chrome instance running on a physical Android device over ADB, as if it were a local Chrome on port 9222.

On top of standard CDP, mobilebridge adds synthetic **touch gesture** commands (tap, swipe, pinch, long-press) that are translated to `Input.dispatchTouchEvent` calls so agents can interact with mobile-first web experiences properly.

## What it does

```
  Your CDP client (Puppeteer, OpenClaw, etc.)
         │  ws://localhost:9222/devtools/page/<id>
         ▼
  ┌──────────────────────┐
  │   mobilebridge       │
  │   - /json/* HTTP API │
  │   - CDP WebSocket    │
  │   - Touch gestures   │
  └──────────────────────┘
         │  adb -s <serial> forward tcp:<port> localabstract:chrome_devtools_remote
         ▼
  Android Chrome on device
```

1. Discovers connected Android devices via `adb devices -l`.
2. Locates the Chrome devtools abstract socket (`chrome_devtools_remote` or `webview_devtools_remote_<pid>`) via `/proc/net/unix`.
3. Sets up an ADB port forward to it.
4. Serves Chrome's `/json/version`, `/json/list`, `/json/new` endpoints and proxies `/devtools/page/<id>` WebSockets through to the device.
5. Intercepts synthetic `MobileBridge.*` gesture methods and dispatches real CDP touch events.

## Requirements

- `adb` on `$PATH` with the device authorized (`adb devices` shows `device`, not `unauthorized`).
- Chrome for Android with USB debugging enabled and at least one tab open (or use Chrome's remote debugging flag on rooted builds).
- Go 1.22+ to build from source.

## Install

```
go install github.com/PopcornDev1/mobilebridge/cmd/mobilebridge@latest
```

Or clone and build:

```
git clone https://github.com/PopcornDev1/mobilebridge
cd mobilebridge
go build ./cmd/mobilebridge
```

## CLI usage

List attached devices:

```
mobilebridge --list
```

Run the bridge for a specific device on a specific local port:

```
mobilebridge --device R58N12ABCDE --port 9222
```

If only one device is attached you can omit `--device`:

```
mobilebridge --port 9222
```

Point any CDP client at `http://localhost:9222` exactly as you would for a local desktop Chrome:

```
const browser = await puppeteer.connect({ browserURL: 'http://localhost:9222' });
```

## Touch gesture extensions

Standard CDP has `Input.dispatchTouchEvent`, but it's fiddly to drive interactive gestures by hand. mobilebridge exposes higher-level helpers as Go functions in `pkg/mobilebridge`:

```go
p, _ := mobilebridge.NewProxy("R58N12ABCDE", 9222)
defer p.Close()

mobilebridge.Tap(p, 200, 400)
mobilebridge.Swipe(p, 500, 1200, 500, 300, 300)         // scroll up
mobilebridge.Pinch(p, 540, 960, 0.5)                    // pinch out
mobilebridge.LongPress(p, 200, 400, 800)
```

Each helper builds the correct sequence of `Input.dispatchTouchEvent` payloads (`touchStart` → `touchMove`s → `touchEnd`) and sends them over the proxied CDP connection.

## Design notes

- **No magic.** mobilebridge is a thin proxy. Everything it does could be scripted with `adb forward` + a raw WebSocket; the point is that it handles device discovery, reconnection, multi-device selection, and gesture ergonomics so you don't have to.
- **Stateless per connection.** The proxy keeps one upstream WebSocket to Chrome and pumps frames bidirectionally. Closing the client closes the upstream and tears down the forward.
- **Hotplug.** `WatchDevices` polls `adb devices` so tools built on top can react to devices appearing and disappearing.

## iOS

mobilebridge is Android-only. iOS Safari support is provided as part of the broader VulpineOS commercial offering; Apple's WebKit Remote Inspector Protocol is undocumented and version-fragile, so it lives behind that ecosystem rather than in this repo.

## License

MIT. See [LICENSE](LICENSE).
