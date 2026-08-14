# dib

DSH-in-Box packages DeepSeek Harness with a private Node.js runtime and a small Go launcher.

`gui` targets use the operating system WebView: WebView2 on Windows, WKWebView on macOS, and WebKitGTK on Linux. No browser engine is included. `serve` targets only start `dsh web`.

## Build

Requirements: Go, npm, and network access. GUI launchers also require a C/C++ toolchain for the target. Linux requires the GTK 4 and WebKitGTK 6.0 development packages while building, and their runtime libraries on the destination machine.

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

`node.base_url` defaults to `https://nodejs.org/dist`; the example uses npmmirror for networks where nodejs.org is unavailable. The selected source must expose Node's normal `v<version>/SHASUMS256.txt` layout. Node downloads, npm package tarballs, and installed DSH runtimes are reused from `cache`.

## macOS DMG and signing

`macos.format: dmg` creates an HFS+/LZMA DMG containing `DeepSeek Harness.app` and an Applications shortcut. HFS+ avoids APFS padding differences between macOS runners. DMG creation requires macOS. Use `tar.gz` to keep the portable archive layout.

Unsigned builds need no identity:

```yaml
macos:
  format: dmg
  sign:
    enabled: false
```

Signed builds sign and verify both the app bundle and final DMG:

```yaml
macos:
  format: dmg
  sign:
    enabled: true
    identity: "Developer ID Application: Example Corp (TEAMID)"
```

## Windows NSIS

`windows.format: nsis` creates a compressed installer with user or machine scope, architecture and Windows version checks, shortcuts, and an uninstaller. It requires `makensis`; use `zip` to keep the portable archive.

Install the packager with `brew install nsis` on macOS or `apt install nsis` on Debian/Ubuntu build hosts.

```yaml
windows:
  format: nsis
  app_name: DeepSeek Harness
  publisher: DeepSeek AI
  install_scope: user
```

The installer uses the system WebView2 runtime and does not bundle a browser engine.

## Linux packages

Linux can emit either or both nFPM-backed formats. The packages install the runtime under `/opt/dsh`, link `dshbox` into `/usr/bin`, and add a desktop entry. Building requires the `nfpm` command.

```sh
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
```

```yaml
linux:
  formats: [deb, rpm]
  package_name: dsh
  maintainer: DeepSeek Harness <noreply@deepseek.com>
  depends:
    deb: [libgtk-4-1, libwebkitgtk-6.0-4]
    rpm: [gtk4, webkitgtk6.0]
```

Dependency package names vary between distributions; set both lists for the distributions you publish to.

## Official releases

Run the `Release official DSH` workflow manually and enter an exact published DSH version without a leading `v`. It builds unsigned macOS DMGs, Windows NSIS installers, and Linux DEB/RPM packages for amd64 and arm64, then publishes them under the matching `v<dsh-version>` GitHub Release. Official release builds always exclude preset plugins.
