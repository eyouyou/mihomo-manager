export type Rule = {
  index: number
  type: string
  payload: string
  proxy: string
  size: number
  extra?: {
    disabled?: boolean
    hitCount?: number
    hitAt?: string
    missCount?: number
    missAt?: string
  }
}

export function moveRule(rows: Rule[], index: number, offset: -1 | 1) {
  const position = rows.findIndex(row => row.index === index)
  const target = position + offset
  if (position < 0 || target < 0 || target >= rows.length) return rows
  const reordered = [...rows]
  ;[reordered[position], reordered[target]] = [reordered[target], reordered[position]]
  return reordered
}

export type RuleFields = Pick<Rule, 'type' | 'payload' | 'proxy'>

type RuleChange = Rule & {
  before?: number
  after?: number
  previousRule?: Rule
  wasEnabled?: boolean
  enabled?: boolean
  action: '新增' | '编辑' | '删除' | '调整'
}

export function sameFields(a: RuleFields, b: RuleFields) {
  return a.type === b.type && a.payload === b.payload && a.proxy === b.proxy
}

export function insertRule(baseline: Rule[], draft: Rule[], fields: RuleFields) {
  const maxIndex = (rows: Rule[]) => rows.reduce((max, rule) => Math.max(max, rule.index), -1)
  const index = Math.max(maxIndex(baseline), maxIndex(draft)) + 1
  const fallback = draft.findIndex(rule => rule.type.toUpperCase() === 'MATCH' && !rule.extra?.disabled)
  const position = fallback < 0 ? draft.length : fallback
  return [...draft.slice(0, position), { ...fields, index, size: -1 }, ...draft.slice(position)]
}

export function getChanges(baseline: Rule[], draft: Rule[]) {
  const original = new Map(baseline.map((rule, index) => [rule.index, { rule, position: index + 1 }]))
  const changes = draft.flatMap<RuleChange>((rule, index) => {
    const previous = original.get(rule.index)
    const after = index + 1
    const enabled = !rule.extra?.disabled
    const wasEnabled = previous ? !previous.rule.extra?.disabled : undefined
    const edited = previous && !sameFields(previous.rule, rule)
    if (previous && !edited && previous.position === after && wasEnabled === enabled) return []
    return [{
      ...rule, before: previous?.position, after,
      previousRule: previous?.rule, wasEnabled, enabled,
      action: previous ? (edited ? '编辑' : '调整') : '新增',
    }]
  })
  const remaining = new Set(draft.map(rule => rule.index))
  baseline.forEach((rule, index) => {
    if (remaining.has(rule.index)) return
    changes.push({ ...rule, before: index + 1, after: undefined, previousRule: rule,
      wasEnabled: !rule.extra?.disabled, enabled: undefined, action: '删除' })
  })
  return changes
}
