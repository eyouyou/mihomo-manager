import { sameFields, type Rule } from './rules'

export type SavedDraft = { baseline: Rule[]; rules: Rule[] }

export function sameSnapshot(a: Rule[], b: Rule[]) {
  return a.length === b.length && a.every((rule, i) => {
    const other = b[i]
    return rule.index === other.index && sameFields(rule, other) &&
      Boolean(rule.extra?.disabled) === Boolean(other.extra?.disabled)
  })
}

function isRule(value: unknown): value is Rule {
  if (!value || typeof value !== 'object') return false
  const row = value as Partial<Rule>
  return Number.isSafeInteger(row.index) && typeof row.type === 'string' &&
    typeof row.payload === 'string' && typeof row.proxy === 'string' && typeof row.size === 'number'
}

export function isRuleList(value: unknown): value is Rule[] {
  return Array.isArray(value) && value.every(isRule) &&
    new Set(value.map(rule => rule.index)).size === value.length
}

export function withoutCounters(rules: Rule[]): Rule[] {
  return rules.map(({ index, type, payload, proxy, size, extra }) => ({
    index, type, payload, proxy, size, extra: { disabled: Boolean(extra?.disabled) },
  }))
}

export function decodeSavedDraft(raw: string | null): SavedDraft | null {
  if (raw === null) return null
  const value = JSON.parse(raw)
  if (value?.version !== 1 || !isRuleList(value.baseline) || !isRuleList(value.rules)) {
    throw new Error('Invalid saved rule draft')
  }
  return { baseline: withoutCounters(value.baseline), rules: withoutCounters(value.rules) }
}

export function validateDraft(saved: SavedDraft, live: Rule[]) {
  if (!sameSnapshot(saved.baseline, live)) return '现网规则已变更。草稿仍保留，请对照现网重新编辑。'
  if (saved.rules.some(rule => !rule.type.trim() || !rule.proxy.trim() ||
    (rule.type.trim().toUpperCase() !== 'MATCH' && !rule.payload.trim()))) {
    return '草稿中有规则缺少类型、匹配条件或策略。'
  }
  return ''
}
