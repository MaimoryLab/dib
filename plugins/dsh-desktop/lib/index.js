import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { defineTool } from '@deepseek-ai/dsh-tools'

const execFileAsync = promisify(execFile)

export const inject = ['tools']

function validateNotification({ title, body }) {
  if (typeof title !== 'string' || typeof body !== 'string' || !title.trim() || !body.trim() || title.length > 256 || body.length > 4096) {
    throw new TypeError('invalid notification text')
  }
}

function appleScriptString(value) {
  return `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`
}

function powerShellString(value) {
  return `'${value.replaceAll("'", "''")}'`
}

async function sendNotification({ title, body }) {
  validateNotification({ title, body })
  if (process.platform === 'darwin') {
    await execFileAsync('osascript', ['-e', `display notification ${appleScriptString(body)} with title ${appleScriptString(title)}`])
    return
  }
  if (process.platform === 'win32') {
    const script = `Add-Type -AssemblyName System.Windows.Forms; $n = New-Object System.Windows.Forms.NotifyIcon; $n.Icon = [System.Drawing.SystemIcons]::Information; $n.Visible = $true; $n.ShowBalloonTip(5000, ${powerShellString(title)}, ${powerShellString(body)}, [System.Windows.Forms.ToolTipIcon]::Info); Start-Sleep -Seconds 5; $n.Dispose()`
    await execFileAsync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script])
    return
  }
  await execFileAsync('notify-send', [title, body])
}

export function apply(ctx) {
  ctx.tools.register(defineTool({
    name: 'desktop_notify',
    description: 'Send a notification to the user desktop.',
    parameters: {
      title: { type: 'string', required: true, description: 'Notification title.' },
      body: { type: 'string', required: true, description: 'Notification body.' },
    },
    output: {
      schema: {
        type: 'object',
        additionalProperties: false,
        properties: { sent: { type: 'boolean', required: true } },
      },
      render: (_args, value) => [{ type: 'text', text: value.sent ? 'Notification sent.' : 'Notification not sent.' }],
    },
    execute: async ({ title, body }) => {
      await sendNotification({ title, body })
      return { sent: true }
    },
  }))
}
