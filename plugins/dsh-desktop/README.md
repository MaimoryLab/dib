# dsh desktop menu

The plugin provides `ctx.desktopMenu` to client plugins. Register a stable menu
item id and keep the disposer with the registering plugin:

```js
const dispose = ctx.desktopMenu.register({
  id: 'my-plugin.settings',
  label: 'Settings',
  parent: 'help',
  order: 10,
  onSelect: () => openSettings(),
})
```

The Windows and macOS launchers render native menus. The Linux launcher renders
the same contributions in a GTK/WebKit menu bar because Linux desktop shells
choose different locations for application menus.
