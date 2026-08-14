# dsh desktop capabilities

The plugin provides `ctx.desktop` to client plugins. Register a stable menu
item id and keep the disposer with the registering plugin:

It also registers the agent tool `desktop_notify`, which accepts `title` and
`body` and sends a native desktop notification without another plugin.

Declare `@maimorylab/dsh-desktop` in `dsh.client.inject` and export
`const inject = ['desktop']` from the consuming client plugin.

```js
const dispose = ctx.desktop.menu.register({
  id: 'my-plugin.settings',
  label: 'Settings',
  parent: 'help',
  order: 10,
  onSelect: () => openSettings(),
})
```

Other host capabilities use the same service:

```js
await ctx.desktop.notify({ title: 'Done', body: 'The task finished.' })
const files = await ctx.desktop.files.choose({ multiple: true })
const offDrop = ctx.desktop.files.onDrop(paths => console.log(paths))
await ctx.desktop.openExternal('https://example.com')
```

Use `ctx.desktop.supported(name)` before optional work. The current capability
names are `menu`, `files.drop`, `files.choose`, `external.open`, and
`notification`; `ctx.desktop.capabilities` exposes the same list for discovery.

The service does not emulate absent capabilities; calls to missing bridges
reject instead of silently doing nothing. Linux exposes menu and drag-and-drop;
`external.open` is added when `xdg-open` exists, and `notification` when
`notify-send` exists.
File selection is intentionally omitted because GTK4 has no portable
implementation here. The launcher owns its system tray lifecycle on Windows
and macOS; it is not exposed to plugins.

The Windows and macOS launchers render native menus. The Linux launcher renders
the same contributions in a GTK/WebKit menu bar because Linux desktop shells
choose different locations for application menus.
