import { createElement, useEffect, useState } from 'react'

const NS = 'dshDesktop'

const zh = { tab: '关于', title: '关于 DeepSeek Harness', version: '版本', loading: '正在读取版本…', unavailable: '版本信息暂不可用。' }
const en = { tab: 'About', title: 'About DeepSeek Harness', version: 'Version', loading: 'Reading version…', unavailable: 'Version information is unavailable.' }

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
    if (!item?.id || !item?.label || this.handlers.has(item.id)) throw new Error(`duplicate desktop menu id: ${item?.id}`)
    this.handlers.set(item.id, item.onSelect)
    window.dshboxMenuRegister?.({ id: item.id, label: item.label, parent: item.parent ?? '', order: item.order ?? 0 })
    return () => {
      this.handlers.delete(item.id)
      window.dshboxMenuUnregister?.(item.id)
    }
  }

  dispose() {
    window.removeEventListener('dshbox:menu-select', this.onSelect)
    this.handlers.clear()
  }
}

export const inject = ['connection', 'locale', 'slots']

export function apply(ctx) {
  ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'dsh-desktop: dictionaries')
  const t = ctx.locale.bind(NS)
  const desktopMenu = new DesktopMenu()
  ctx.desktopMenu = desktopMenu
  ctx.effect(() => () => desktopMenu.dispose(), 'dsh-desktop: menu bridge')
  const describe = async () => {
    const response = await ctx.connection.api.host.describe({})
    if (!response.result.ok) throw new Error(`${response.result.error.code}: ${response.result.error.message}`)
    return response.result.value.version
  }
  const showAbout = () => {
    const existing = document.getElementById('dsh-desktop-about-dialog')
    if (existing) return
    const dialog = document.createElement('dialog')
    dialog.id = 'dsh-desktop-about-dialog'
    dialog.innerHTML = `<form method="dialog"><h2>${t('title')}</h2><p>${t('loading')}</p><button>${t('tab')}</button></form>`
    document.body.append(dialog)
    dialog.showModal()
    void describe().then(
      version => { const p = dialog.querySelector('p'); if (p) p.textContent = `${t('version')}: ${version}` },
      () => { const p = dialog.querySelector('p'); if (p) p.textContent = t('unavailable') },
    )
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

  ctx.effect(() => desktopMenu.register({
    id: 'dsh-desktop.about',
    label: t('tab'),
    parent: 'help',
    onSelect: showAbout,
  }), 'dsh-desktop: about menu')
}
