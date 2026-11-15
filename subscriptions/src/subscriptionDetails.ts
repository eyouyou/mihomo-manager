export type Subscription = {
  name: string
  displayName?: string
  'display-name'?: string
  source: 'remote' | 'local'
  url?: string
}

export type SubscriptionDetails = {
  snapshot?: {
    count: number
    updatedAt: number
    used?: number
    total?: number
    expiresAt?: number
  }
  intervalMinutes: number
  nextUpdateAt?: number
  lastError?: string
  updating?: boolean
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
