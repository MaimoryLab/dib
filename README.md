# dib

DSH-in-Box packages DeepSeek Harness with a private Node.js runtime and a small Go launcher.

`gui` targets use the operating system WebView: WebView2 on Windows, WKWebView on macOS, and WebKitGTK on Linux. No browser engine is included. `serve` targets only start `dsh web`.

## Build

Requirements: Go, npm, and network access. GUI launchers also require a C/C++ toolchain for the target. Linux requires the GTK 3 and WebKitGTK 4.0 development packages while building, and their runtime libraries on the destination machine.

```sh
go run . -dry-run
go run . -target darwin/arm64
go run .                         # build every configured target
```

Because native WebViews use CGO, the simplest six-target release is a build matrix that runs `go run . -target "$TARGET"` on the matching Windows, macOS, and Linux architecture. Cross builds can set `cc` and `cxx` on a target in `dib.yaml` when a suitable cross-toolchain and target SDK are installed:

```yaml
targets:
  - os: windows
    arch: arm64
    cc: aarch64-w64-mingw32-gcc
    cxx: aarch64-w64-mingw32-g++
```

Each archive contains `dshbox`, Node.js, `@deepseek-ai/dsh`, and packages listed in `dsh.plugins`. npm lifecycle scripts are disabled during packaging; preset plugins should therefore be published as built packages.

`node.base_url` defaults to `https://nodejs.org/dist`; the example uses npmmirror for networks where nodejs.org is unavailable. The selected source must expose Node's normal `v<version>/SHASUMS256.txt` layout.
