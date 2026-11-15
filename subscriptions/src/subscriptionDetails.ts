export type Subscription = {
  name: string
  displayName?: string
  'display-name'?: string
  source: 'remote' | 'local'
  url?: string
  content?: string
}

export type SubscriptionDetails = {
  count: number
  updatedAt: number
  used?: number
  total?: number
  expiresAt?: number
}

export function parseUserInfo(header: string | null) {
  const fields = new Map<string, number>()
  for (const entry of header?.split(/[;,]/) ?? []) {
    const match = /^\s*([a-z]+)\s*=\s*(\d+)\s*$/i.exec(entry)
    if (!match) continue
    const value = Number(match[2])
    if (Number.isSafeInteger(value)) fields.set(match[1].toLowerCase(), value)
  }
  const upload = fields.get('upload')
  const download = fields.get('download')
  const used = upload === undefined || download === undefined ? undefined : upload + download
  const total = fields.get('total')
  const expiresAt = (fields.get('expire') ?? 0) * 1000
  return {
    used: Number.isSafeInteger(used) ? used : undefined,
    total: total && total > 0 ? total : undefined,
    expiresAt: expiresAt > 0 && expiresAt <= 8.64e15 ? expiresAt : undefined,
  }
}

async function detailsKey(item: Subscription) {
  // Use a fingerprint to invalidate metadata when the source changes. Never store the URL or nodes.
  const source = JSON.stringify([item.name, item.source, item.url, item.content])
  const encoded = new TextEncoder().encode(source)
  const hash = await crypto.subtle.digest('SHA-256', encoded)
  return 'mihomo-manager:subscription-details:' +
    Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, '0')).join('')
}

export async function readDetails(item: Subscription): Promise<SubscriptionDetails | undefined> {
  try {
    const value = localStorage.getItem(await detailsKey(item))
    if (value) return JSON.parse(value) as SubscriptionDetails
  } catch {
    // Storage can be disabled. Refresh results are still available on the current page.
  }
}

export async function writeDetails(item: Subscription, details?: SubscriptionDetails) {
  try {
    const key = await detailsKey(item)
    if (details) localStorage.setItem(key, JSON.stringify(details))
    else localStorage.removeItem(key)
  } catch {
    // The browser can deny writes or run out of storage.
  }
}

const numberFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })
const dateTimeFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })
const dateFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium' })

export function formatBytes(bytes: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let unit = 0
  while (bytes >= 1024 && unit < units.length - 1) {
    bytes /= 1024
    unit++
  }
  return `${numberFormat.format(bytes)} ${units[unit]}`
}

export function formatDate(timestamp: number, includeTime = false) {
  return (includeTime ? dateTimeFormat : dateFormat).format(timestamp)
}
