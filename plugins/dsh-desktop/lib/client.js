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

export const inject = ['connection', 'locale', 'slots']

export function apply(ctx) {
  ctx.effect(() => ctx.locale.register(NS, { zh, en }), 'dsh-desktop: dictionaries')
  const t = ctx.locale.bind(NS)
  const describe = async () => {
    const response = await ctx.connection.api.host.describe({})
    if (!response.result.ok) throw new Error(`${response.result.error.code}: ${response.result.error.message}`)
    return response.result.value.version
  }
  ctx.slots.inject('settings.plugins.tab', () => ctx.slots.register({
    name: 'settings.plugins.tab',
    id: 'about',
    order: 0,
    label: () => t('tab'),
    locale: NS,
    inject: () => ({ describe, t }),
  }, AboutPage))
}
