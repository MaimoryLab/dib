window.__ModuleLoader__.load({
  id: '@maimorylab/dsh-desktop',
  factory: require => {
const { createElement, useEffect, useState } = require('react')
const { Service } = require('@deepseek-ai/cordis')

const NS = 'dshDesktop'

const zh = { tab: '关于', title: '关于 DeepSeek Harness', subtitle: '桌面客户端', version: '版本', loading: '正在读取版本…', unavailable: '版本信息暂不可用。', close: '关闭' }
const en = { tab: 'About', title: 'About DeepSeek Harness', subtitle: 'Desktop client', version: 'Version', loading: 'Reading version…', unavailable: 'Version information is unavailable.', close: 'Close' }

function AboutPage({ describe, t }) {
  const [state, setState] = useState({ status: 'loading' })
  useEffect(() => {
    let current = true
    void describe().then(
      value => { if (current) setState({ status: 'ready', version: value }) },
      () => { if (current) setState({ status: 'error' }) },
    )
    return () => { current = false }
  }, [describe])

  return createElement('section', { 'aria-labelledby': 'dsh-desktop-about-title' },
    createElement('h2', { id: 'dsh-desktop-about-title' }, t('title')),
    state.status === 'loading' ? createElement('p', null, t('loading')) : null,
    state.status === 'error' ? createElement('p', { role: 'alert' }, t('unavailable')) : null,
    state.status === 'ready' ? createElement('p', null, `${t('version')}: ${state.version}`) : null,
  )
}

class DesktopMenu {
  constructor() {
    this.handlers = new Map()
    this.onSelect = event => this.handlers.get(event.detail)?.()
    window.addEventListener('dshbox:menu-select', this.onSelect)
  }

  register(item) {
    if (typeof window.dshboxMenuRegister !== 'function' || typeof window.dshboxMenuUnregister !== 'function') throw new Error('desktop menu is unavailable')
    if (typeof item?.id !== 'string' || typeof item.label !== 'string' || (item.parent !== undefined && typeof item.parent !== 'string') || typeof item.onSelect !== 'function') throw new Error(`invalid desktop menu id: ${item?.id}`)
    const id = item.id.trim()
    const label = item.label.trim()
    const parent = item.parent?.trim() ?? ''
    const order = item.order ?? 0
    const invalid = !id || !label || id.length > 127 || label.length > 256 || parent.length > 64
      || /[\u0000-\u001f\u007f]/u.test(id) || /[\u0000-\u001f\u007f]/u.test(label) || /[\u0000-\u001f\u007f]/u.test(parent)
      || !Number.isInteger(order) || order < -2147483648 || order > 2147483647 || this.handlers.has(id)
    if (invalid) throw new Error(`invalid or duplicate desktop menu id: ${item.id}`)
    this.handlers.set(id, item.onSelect)
    window.dshboxMenuRegister?.({ id, label, parent, order })
    return () => {
      this.handlers.delete(id)
      window.dshboxMenuUnregister?.(id)
    }
  }

  dispose() {
    if (typeof window.dshboxMenuUnregister === 'function') {
      for (const id of this.handlers.keys()) window.dshboxMenuUnregister(id)
    }
    window.removeEventListener('dshbox:menu-select', this.onSelect)
    this.handlers.clear()
  }
}

function bridge(name, value) {
  const fn = window[name]
  if (typeof fn !== 'function') return Promise.reject(new Error(`${name} is unavailable`))
  return Promise.resolve(value === undefined ? fn() : fn(value))
}

class DesktopRuntime extends Service {
  constructor(ctx) {
    super(ctx, 'desktop')
    this.menu = new DesktopMenu()
    const hasBridges = (...names) => names.every(name => typeof window[name] === 'function')
    this.capabilities = Object.freeze([
      ['menu', () => hasBridges('dshboxMenuRegister', 'dshboxMenuUnregister')],
      ['files.drop', () => window.dshboxFilesDrop === true],
      ['notification', () => hasBridges('dshboxNotify')],
      ['tray', () => hasBridges('dshboxTraySet', 'dshboxTrayClear', 'dshboxTrayShow', 'dshboxTrayHide', 'dshboxTrayQuit')],
      ['files.choose', () => hasBridges('dshboxChooseFiles')],
      ['external.open', () => hasBridges('dshboxOpenExternal')],
    ].flatMap(([capability, available]) => available() ? [capability] : []))
    this.dropHandlers = new Set()
    this.onDrop = event => {
      const files = Array.isArray(event.detail) ? event.detail : []
      for (const handler of this.dropHandlers) handler(files)
    }
    if (this.supported('files.drop')) window.addEventListener('dshbox:files-dropped', this.onDrop)
  }

  notify(options) {
    if (typeof options?.title !== 'string' || typeof options.body !== 'string' || !options.title.trim() || !options.body.trim() || options.title.length > 256 || options.body.length > 4096) return Promise.reject(new TypeError('invalid notification text'))
    return bridge('dshboxNotify', options)
  }

  tray = {
    set: options => bridge('dshboxTraySet', options ?? {}),
    clear: () => bridge('dshboxTrayClear'),
    show: () => bridge('dshboxTrayShow'),
    hide: () => bridge('dshboxTrayHide'),
    quit: () => bridge('dshboxTrayQuit'),
  }

  files = {
    choose: options => bridge('dshboxChooseFiles', Boolean(options?.multiple)),
    onDrop: handler => {
      if (typeof handler !== 'function') throw new TypeError('drop handler must be a function')
      this.dropHandlers.add(handler)
      return () => this.dropHandlers.delete(handler)
    },
  }

  openExternal(url) {
    if (typeof url !== 'string' || !/^https?:\/\//i.test(url)) return Promise.reject(new TypeError('only http(s) URLs may be opened'))
    return bridge('dshboxOpenExternal', url)
  }

  supported(capability) {
    return this.capabilities.includes(capability)
  }

  dispose() {
    this.menu.dispose()
    this.dropHandlers.clear()
    if (this.supported('files.drop')) window.removeEventListener('dshbox:files-dropped', this.onDrop)
  }
}

const inject = ['connection', 'locale', 'slots']

function apply(ctx) {
  ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'dsh-desktop: dictionaries')
  const t = ctx.locale.bind(NS)
  const desktop = new DesktopRuntime(ctx)
  ctx.effect(() => () => desktop.dispose(), 'dsh-desktop: capability bridge')
  const describe = async () => {
    if (typeof window.dshboxVersion === 'string' && window.dshboxVersion) return window.dshboxVersion
    const response = await ctx.connection.api.host.describe({})
    if (!response.result.ok) throw new Error(`${response.result.error.code}: ${response.result.error.message}`)
    return response.result.value.version
  }
  const showAbout = () => {
    const existing = document.getElementById('dsh-desktop-about-dialog')
    if (existing) return
    const dialog = document.createElement('dialog')
    dialog.id = 'dsh-desktop-about-dialog'
    dialog.setAttribute('aria-labelledby', 'dsh-desktop-dialog-title')
    dialog.innerHTML = `<style>
      #dsh-desktop-about-dialog{color-scheme:light dark;width:min(360px,calc(100vw - 32px));padding:0;border:1px solid #3f4145;border-radius:8px;color:#f4f4f5;background:#202124;box-shadow:0 18px 48px #0009}
      #dsh-desktop-about-dialog::backdrop{background:#0008;backdrop-filter:blur(2px)}
      #dsh-desktop-about-dialog form{padding:24px}
      #dsh-desktop-about-dialog header{display:flex;align-items:flex-start;gap:14px}
      #dsh-desktop-about-dialog .mark{display:grid;flex:0 0 40px;height:40px;place-items:center;border-radius:8px;color:#fff;background:#087cfa;font-weight:700;font-size:15px}
      #dsh-desktop-about-dialog h2{margin:1px 40px 3px 0;font-size:19px;line-height:1.35;letter-spacing:0}
      #dsh-desktop-about-dialog .product{margin:0;color:#a7a9ad;font-size:13px}
      #dsh-desktop-about-dialog .close{position:absolute;top:14px;right:14px;display:grid;width:32px;height:32px;padding:0;place-items:center;border:0;border-radius:6px;color:#a7a9ad;background:transparent;font:22px/1 system-ui;cursor:pointer}
      #dsh-desktop-about-dialog .close:hover{color:#fff;background:#ffffff12}
      #dsh-desktop-about-dialog .close:focus-visible{outline:2px solid #409cff;outline-offset:2px}
      #dsh-desktop-about-dialog .version{display:flex;align-items:center;justify-content:space-between;margin:22px 0 0;padding:12px 14px;border-radius:6px;color:#a7a9ad;background:#ffffff0a;font-size:13px}
      #dsh-desktop-about-dialog .version strong{color:#f4f4f5;font-weight:600;font-variant-numeric:tabular-nums}
      @media(prefers-color-scheme:light){#dsh-desktop-about-dialog{border-color:#d8d9dc;color:#202124;background:#fff;box-shadow:0 18px 48px #0003}#dsh-desktop-about-dialog::backdrop{background:#0005}#dsh-desktop-about-dialog .product,#dsh-desktop-about-dialog .close,#dsh-desktop-about-dialog .version{color:#686b70}#dsh-desktop-about-dialog .close:hover{color:#202124;background:#0000000a}#dsh-desktop-about-dialog .version{background:#f5f6f7}#dsh-desktop-about-dialog .version strong{color:#202124}}
    </style><form method="dialog"><header><span class="mark" aria-hidden="true">DS</span><div><h2 id="dsh-desktop-dialog-title">DeepSeek Harness</h2><p class="product">${t('subtitle')}</p></div><button class="close" aria-label="${t('close')}" title="${t('close')}">×</button></header><p class="version"><span>${t('version')}</span><strong>${t('loading')}</strong></p></form>`
    document.body.append(dialog)
    dialog.showModal()
    void describe().then(
      version => { const value = dialog.querySelector('.version strong'); if (value) value.textContent = version },
      () => { const value = dialog.querySelector('.version strong'); if (value) value.textContent = t('unavailable') },
    )
    dialog.addEventListener('click', event => { if (event.target === dialog) dialog.close() })
    dialog.addEventListener('close', () => dialog.remove(), { once: true })
  }
  window.addEventListener('dshbox:open-about', showAbout)
  ctx.effect(() => () => window.removeEventListener('dshbox:open-about', showAbout), 'dsh-desktop: about dialog')
  ctx.slots.inject('settings.plugins.tab', () => ctx.slots.register({
    name: 'settings.plugins.tab',
    id: 'about',
    order: 0,
    label: () => t('tab'),
    locale: NS,
    inject: () => ({ describe, t }),
  }, AboutPage))

  if (desktop.supported('menu')) {
    ctx.effect(() => desktop.menu.register({
      id: 'dsh-desktop.about',
      label: t('tab'),
      parent: 'help',
      onSelect: showAbout,
    }), 'dsh-desktop: about menu')
  }
}

    return { inject, apply }
  },
})
