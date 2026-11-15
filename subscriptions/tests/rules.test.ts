import assert from 'node:assert/strict'
import test from 'node:test'
import { getChanges, moveRule, type Rule } from '../src/rules.ts'

test('preview tracks identical rules independently and preserves the live snapshot', () => {
  const common = { type: 'DOMAIN', payload: 'example.test', proxy: 'DIRECT', size: -1 }
  const live: Rule[] = [{ ...common, index: 0 }, { ...common, index: 1, extra: { disabled: true } }]
  assert.equal(moveRule(live, 0, -1), live)
  const draft = moveRule(live, 0, 1)
  draft[0] = { ...draft[0], extra: { disabled: false } }
  assert.deepEqual(getChanges(live, draft).map(({ index, before, after, wasEnabled, enabled }) =>
    ({ index, before, after, wasEnabled, enabled })), [
    { index: 1, before: 2, after: 1, wasEnabled: false, enabled: true },
    { index: 0, before: 1, after: 2, wasEnabled: true, enabled: true },
  ])
  assert.equal(live[1].extra?.disabled, true)
  assert.deepEqual(getChanges(live, moveRule(moveRule(live, 0, 1), 0, -1)), [])
})
