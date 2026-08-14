import type { Context } from '@deepseek-ai/cordis'

export interface DesktopMenuItem {
  id: string
  label: string
  parent?: string
  order?: number
  onSelect: () => void
}

export interface DesktopMenu {
  register(item: DesktopMenuItem): () => void
}

export interface DesktopService {
  readonly capabilities: readonly string[]
  supported(capability: string): boolean
  menu: DesktopMenu
  notify(options: { title: string; body: string }): Promise<void>
  tray: {
    set(options?: { title?: string; tooltip?: string }): Promise<void>
    clear(): Promise<void>
    show(): Promise<void>
    hide(): Promise<void>
    quit(): Promise<void>
  }
  files: {
    choose(options?: { multiple?: boolean }): Promise<string[]>
    onDrop(handler: (paths: string[]) => void): () => void
  }
  openExternal(url: string): Promise<void>
}

declare module '@deepseek-ai/cordis' {
  interface Context {
    desktop: DesktopService
  }
}

export const inject: string[]
export function apply(ctx: Context): void
