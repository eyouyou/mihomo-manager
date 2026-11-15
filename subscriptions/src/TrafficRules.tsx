import { useEffect, useState } from 'react'
import { Alert, AutoComplete, Button, Card, Form, Input, Modal, Popconfirm, Space, Switch, Table, Tag, Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined } from '@ant-design/icons'
import type { TableColumnsType } from 'antd'
import { getChanges, insertRule, moveRule, sameFields, type Rule, type RuleFields } from './rules'

import { decodeSavedDraft, isRuleList, sameSnapshot, validateDraft, withoutCounters, type SavedDraft } from './rule-draft'
import './rules.css'

const { Title, Text } = Typography

const storageKey = 'mihomo-manager:rule-draft:v1'
const emptyRules: Rule[] = []

type PublishResult = { ticket?: string; backup?: string; error?: string }

async function publishRequest(action: 'preview' | 'apply', draft: SavedDraft, ticket?: string): Promise<PublishResult> {
  const response = await fetch('/traffic-rules/api/' + action, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ baseline: draft.baseline, rules: draft.rules, ticket }),
  })
  const result: PublishResult = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(result.error || '规则发布器无法完成请求。请重新读取现网确认结果。')
  return result
}

async function fetchRules(signal?: AbortSignal): Promise<{ rules: Rule[]; canPublish: boolean }> {
  const response = await fetch('/traffic-rules/api/rules', { signal })
  if (response.status === 401) throw new Error('登录已过期，请重新登录后读取规则。')
  if (!response.ok) throw new Error('暂时无法从 Mihomo 读取规则。')
  const snapshot = await response.json()
  if (!isRuleList(snapshot?.rules)) throw new Error('路由器返回了无效的规则列表。')
  return { rules: snapshot.rules, canPublish: snapshot.canPublish === true }
}

function status(enabled?: boolean) {
  if (enabled === undefined) return '—'
  return <Tag color={enabled ? 'green' : undefined}>{enabled ? '启用' : '停用'}</Tag>
}

function description(rule: RuleFields) {
  return <>{rule.type} <code>{rule.payload}</code> → {rule.proxy}</>
}

export default function TrafficRules() {
  const [live, setLive] = useState<Rule[] | null>(null)
  const [saved, setSaved] = useState<SavedDraft | null>(null)
  const [storageError, setStorageError] = useState('')
  const [canPublish, setCanPublish] = useState(false)
  const [review, setReview] = useState<{ draft: SavedDraft; ticket: string } | null>(null)
  const [applying, setApplying] = useState(false)
  const [applyMessage, setApplyMessage] = useState('')
  const [checking, setChecking] = useState(false)
  const [validationError, setValidationError] = useState('')
  const baseline = saved?.baseline ?? live ?? emptyRules
  const draft = saved?.rules ?? baseline
  const stale = saved && live && !sameSnapshot(saved.baseline, live)
  const readyToApply = review && saved === review.draft && !stale ? review : null
  const validation = validationError || (saved && live ? validateDraft(saved, live) : '当前没有可检查的草稿。')
  const [load, setLoad] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [preview, setPreview] = useState(false)
  const [query, setQuery] = useState('')
  const [editor, setEditor] = useState<{ index?: number } | null>(null)
  const [form] = Form.useForm<RuleFields>()
  const changes = getChanges(baseline, draft)
  const positions = new Map(draft.map((rule, index) => [rule.index, index]))
  const search = query.trim().toLowerCase()
  const visible = draft.filter(rule => [rule.type, rule.payload, rule.proxy].some(value => value.toLowerCase().includes(search)))
  const options = (field: 'type' | 'proxy') => Array.from(new Set([...baseline, ...draft].map(rule => rule[field])))
    .sort().map(value => ({ value }))

  function writeDraft(next: SavedDraft | null) {
    setReview(null)
    setApplyMessage('')
    setSaved(next)
    try {
      if (next) localStorage.setItem(storageKey, JSON.stringify({ version: 1, ...next }))
      else localStorage.removeItem(storageKey)
      setStorageError('')
    } catch {
      setStorageError('浏览器无法保存草稿。当前编辑仍在页面内，请保持页面打开。')
    }
  }

  function updateDraft(edit: (rules: Rule[]) => Rule[]) {
    const rules = edit(draft)
    writeDraft(getChanges(baseline, rules).length > 0
      ? { baseline: withoutCounters(baseline), rules: withoutCounters(rules) }
      : null)
  }

  async function validateAndPreview() {
    if (!saved) return
    setChecking(true)
    setReview(null)
    try {
      const current = await fetchRules(AbortSignal.timeout(10000))
      setLive(current.rules)
      setCanPublish(current.canPublish)
      setValidationError('')
      if (current.canPublish && !validateDraft(saved, current.rules)) {
        const { ticket } = await publishRequest('preview', saved)
        if (!ticket) throw new Error('路由器没有生成可确认的候选配置。')
        setReview({ draft: saved, ticket })
      }
    } catch (problem) {
      setValidationError(problem instanceof Error ? problem.message : '无法检查现网规则。')
    } finally {
      setChecking(false)
      setPreview(true)
    }
  }

  async function applyReviewed() {
    if (!readyToApply || applying) return
    setApplying(true)
    setValidationError('')
    try {
      const result = await publishRequest('apply', readyToApply.draft, readyToApply.ticket)
      if (!result.backup) throw new Error('未收到发布结果，请重新读取现网。')
      writeDraft(null)
      setPreview(false)
      setApplyMessage('热加载和启动配置写入均已完成。回滚备份：' + result.backup)
      setLoading(true)
      setLoad(value => value + 1)
    } catch (problem) {
      setReview(null)
      setValidationError(problem instanceof Error ? problem.message : '发布失败，请重新读取现网。')
    } finally {
      setApplying(false)
    }
  }

  function openEditor(rule?: Rule) {
    form.setFieldsValue(rule ? { type: rule.type, payload: rule.payload, proxy: rule.proxy } : {
      type: 'DOMAIN', payload: '', proxy: 'DIRECT',
    })
    setEditor({ index: rule?.index })
  }

  function saveRule(values: RuleFields) {
    if (!editor) return
    const fields = { type: values.type.trim(), payload: (values.payload ?? '').trim(), proxy: values.proxy.trim() }
    updateDraft(rows => editor.index === undefined
      ? insertRule(baseline, rows, fields)
      : rows.map(rule => rule.index === editor.index ? { ...rule, ...fields } : rule))
    setQuery('')
    setEditor(null)
  }

  useEffect(() => {
    function restore() {
      try {
        setSaved(decodeSavedDraft(localStorage.getItem(storageKey)))
        setStorageError('')
      } catch {
        setStorageError('浏览器中的历史草稿无法读取。')
      }
    }
    restore()
    function sync(event: StorageEvent) {
      if (event.key === storageKey || event.key === null) restore()
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])

  useEffect(() => {
    const request = new AbortController()
    fetchRules(request.signal)
      .then(({ rules, canPublish }) => {
        if (request.signal.aborted) return
        setLive(rules)
        setCanPublish(canPublish)
      })
      .catch(problem => {
        if (!request.signal.aborted) setError(problem.message)
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false)
      })
    return () => request.abort()
  }, [load])

  const liveByIndex = new Map(live?.map(rule => [rule.index, rule]))
  const columns: TableColumnsType<Rule> = [
    { title: '顺序', render: (_, rule) => positions.get(rule.index)! + 1, width: 70 },
    { title: '类型', dataIndex: 'type', width: 114 },
    { title: '匹配条件', dataIndex: 'payload', render: value => <code>{value || '—'}</code> },
    { title: '策略', dataIndex: 'proxy', width: 176 },
    { title: '条目数', dataIndex: 'size', render: size => size < 0 ? '—' : size, width: 86 },
    { title: '命中 / 未命中', render: (_, rule) => {
      const current = liveByIndex.get(rule.index)
      const stats = !stale && current && sameFields(current, rule) ? current.extra : undefined
      return <span title={`最后命中 ${stats?.hitAt ?? '—'}，最后未命中 ${stats?.missAt ?? '—'}`}>
        {stats?.hitCount ?? '—'} / {stats?.missCount ?? '—'}
      </span>
    }, width: 130 },
    { title: '草稿状态', render: (_, rule) => (
      <Switch checked={!rule.extra?.disabled} checkedChildren="启用" unCheckedChildren="停用"
        aria-label={`规则 ${rule.index + 1} 启用状态`}
        onChange={checked => updateDraft(rows => rows.map(row =>
          row.index === rule.index ? { ...row, extra: { ...row.extra, disabled: !checked } } : row))} />
    ), width: 106 },
    { title: '排序', render: (_, rule) => (
      <Space size={4}>
        <Button icon={<ArrowUpOutlined />} aria-label={`上移规则 ${rule.index + 1}`}
          disabled={!!search || positions.get(rule.index) === 0} onClick={() => updateDraft(rows => moveRule(rows, rule.index, -1))} />
        <Button icon={<ArrowDownOutlined />} aria-label={`下移规则 ${rule.index + 1}`}
          disabled={!!search || positions.get(rule.index) === draft.length - 1} onClick={() => updateDraft(rows => moveRule(rows, rule.index, 1))} />
      </Space>
    ), width: 96 },
    { title: '操作', render: (_, rule) => (
      <Space size={4}>
        <Button onClick={() => openEditor(rule)} aria-label={`编辑规则 ${rule.index + 1}`}>编辑</Button>
        <Popconfirm title="从草稿删除这条规则？" okText="删除" cancelText="取消"
          onConfirm={() => updateDraft(rows => rows.filter(row => row.index !== rule.index))}>
          <Button danger aria-label={`删除规则 ${rule.index + 1}`}>删除</Button>
        </Popconfirm>
      </Space>
    ), width: 128 },
  ]

  return (
    <main className="rules-page">
      <Title level={1}>流量规则</Title>
      <p><Text type="secondary">Mihomo</Text></p>
      <Alert type="info" showIcon title="草稿自动保存在当前浏览器，刷新后可继续编辑。" />
      {applyMessage && <Alert className="rules-error" type="success" showIcon title={applyMessage} />}
      {storageError && <Alert className="rules-error" type="warning" showIcon title={storageError} />}
      {stale && <Alert className="rules-error" type="warning" showIcon title="现网规则与草稿基线不同。草稿继续保留。" />}
      {error && <Alert className="rules-error" type="error" showIcon title={error}
        action={<Button href="/sso/login?next=%2Fmanagement%2F%3Fview%3Drules">重新登录</Button>} />}
      <Card className="rules-card">
        <div className="rules-toolbar">
          <Text strong>{loading ? '读取规则中' : search ? `${visible.length} / ${draft.length} 条规则` : `${draft.length} 条规则`}</Text>
          <Space wrap>
            <Input aria-label="查找规则" placeholder="查找类型、条件或策略" allowClear value={query}
              onChange={event => setQuery(event.target.value)} />
            <Button disabled={loading || (!live && !saved)} onClick={() => openEditor()}>新增规则</Button>
            <Button disabled={loading || checking} onClick={() => {
              setLoading(true)
              setError('')
              setLoad(value => value + 1)
            }}>重新读取</Button>
            <Popconfirm title="清除已保存的草稿并恢复现网视图？" okText="清除" cancelText="取消"
              onConfirm={() => writeDraft(null)}>
              <Button disabled={!saved || checking}>重置草稿</Button>
            </Popconfirm>
            <Button type="primary" aria-label="校验并预览变更" disabled={!changes.length || loading} loading={checking} onClick={() => void validateAndPreview()}>
              {changes.length ? `校验并预览（${changes.length}）` : '校验并预览'}
            </Button>
          </Space>
        </div>
        <Table rowKey="index" columns={columns} dataSource={visible} loading={loading}
          pagination={false} size="small" scroll={{ x: 1140 }} />
      </Card>
      <Modal title={editor?.index === undefined ? '新增规则' : '编辑规则'} open={!!editor} forceRender
        onCancel={() => setEditor(null)} okText="保存草稿" cancelText="取消" onOk={() => form.submit()}>
        <Form form={form} layout="vertical" onFinish={saveRule}>
          <Form.Item name="type" label="类型" rules={[{ required: true, whitespace: true }, { pattern: /^[a-z0-9_-]+$/i, message: '类型只能包含字母、数字、短横线和下划线。' }]}>
            <AutoComplete options={options('type')} />
          </Form.Item>
          <Form.Item name="payload" label="匹配条件" dependencies={['type']}
            rules={[({ getFieldValue }) => ({
              validator: (_, value) => String(getFieldValue('type') ?? '').trim().toUpperCase() === 'MATCH' || value?.trim()
                ? Promise.resolve() : Promise.reject(new Error('请输入匹配条件。')),
            })]}>
            <Input />
          </Form.Item>
          <Form.Item name="proxy" label="策略" rules={[{ required: true, whitespace: true }]}>
            <AutoComplete options={options('proxy')} />
          </Form.Item>
        </Form>
      </Modal>
      <Modal title="变更预览" open={preview} closable={!applying} keyboard={!applying}
        onCancel={() => { if (!applying) setPreview(false) }}
        width={860} footer={<Space>
          <Button disabled={applying} onClick={() => setPreview(false)}>返回草稿</Button>
          {readyToApply && <Popconfirm title="确认将预览中的规则热加载并写入启动配置？"
            okText="确认应用" cancelText="取消" onConfirm={() => void applyReviewed()}>
            <Button type="primary" loading={applying}>应用到路由器</Button>
          </Popconfirm>}
        </Space>}>
        <Alert showIcon type={validation ? 'warning' : 'success'}
          title={validation || '草稿字段完整，现网规则与草稿基线一致。'}
          description={readyToApply ? 'Mihomo 已通过候选配置校验。确认后会备份启动文件、热加载并读回验证。'
            : canPublish ? '通过路由器校验后即可确认应用。' : '当前只有草稿检查，路由器发布器尚未配置。'} />
        <Table rowKey="index" dataSource={changes} size="small" pagination={false} scroll={{ x: 560, y: 440 }}
          columns={[
            { title: '操作', dataIndex: 'action', width: 72 },
            { title: '规则', render: (_, rule) => <>
              {rule.previousRule && !sameFields(rule.previousRule, rule) && <div><Text type="secondary">原规则</Text> {description(rule.previousRule)}</div>}
              {description(rule)}
            </> },
            { title: '顺序（基线 → 草稿）', render: (_, rule) => `${rule.before ?? '—'} → ${rule.after ?? '—'}`, width: 168 },
            { title: '状态（基线 → 草稿）', render: (_, rule) => <>{status(rule.wasEnabled)} → {status(rule.enabled)}</>, width: 170 },
          ]} />
      </Modal>
    </main>
  )
}
