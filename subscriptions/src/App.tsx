import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { Alert, App as AntApp, Button, Card, Empty, Form, Input, InputNumber, Modal, Progress, Skeleton, Space } from 'antd'
import { Download, Layers, Link2, Pencil, Plus, RefreshCw, Trash2 } from './icons'

import { formatBytes, formatDate } from './subscriptionDetails'
import type { Subscription, SubscriptionDetails } from './subscriptionDetails'
type Draft = { name: string; url: string }
type RefreshResult = { state: 'loading' | 'idle' | 'error'; detail?: string }
const detailsPath = '/api/subscription-details'
const maximumInterval = 30 * 24 * 60

type Selection = { current: string; available: string[] }

async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`/subscriptions${path}`, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(80000),
  })
  if (response.status === 401) {
    window.location.assign('/sso/login?next=/management/')
    throw new Error('登录已过期，正在跳转。')
  }
  if (!response.ok) throw new Error(`请求失败（HTTP ${response.status}）`)
  if (response.status === 204) return undefined as T
  const result = await response.json()
  if (result?.status === 'failed' || result?.status === 'error') throw new Error('Sub-Store 拒绝了请求。')
  return (result?.status === 'success' ? result.data : result) as T
}

function parseUrl(value: string) {
  const url = new URL(value.trim())
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error('请输入有效的 HTTP 或 HTTPS 订阅地址。')
  return url
}

function displayHost(value?: string) {
  try { return value ? parseUrl(value).hostname : '' } catch { return '' }
}

function subscriptionTitle(item: Subscription) {
  return item.displayName || item['display-name'] || item.name
}

function Details({ item, details }: { item: Subscription; details?: SubscriptionDetails }) {
  const { count, updatedAt, used, total, expiresAt } = details?.snapshot ?? {}
  const nextUpdateAt = details?.nextUpdateAt
  const hasQuota = used !== undefined && total !== undefined
  const unknown = details?.snapshot ? '服务商未提供' : '等待更新'
  const expired = expiresAt !== undefined && expiresAt <= Date.now()

  return <dl className="subscription-details">
    {item.source === 'remote' && <div className="subscription-quota">
      <dt>流量用量</dt>
      <dd>
        {used === undefined && total === undefined ? unknown : <>
          <strong>{used === undefined ? '用量未知' : formatBytes(used)}</strong>
          <span> / {total === undefined ? '总量未知' : formatBytes(total)}</span>
        </>}
      </dd>
      {hasQuota && <Progress percent={Math.min(100, used / total * 100)} showInfo={false}
        aria-label="已用流量比例" strokeColor="var(--primary)" />}
    </div>}
    <div><dt>节点数量</dt><dd>{count ?? '等待更新'}</dd></div>
    {item.source === 'remote' && <div>
      <dt>到期时间</dt>
      <dd className={expired ? 'error' : undefined}>
        {expiresAt === undefined ? unknown : <><time dateTime={new Date(expiresAt).toISOString()}>{formatDate(expiresAt)}</time>{expired && '（已到期）'}</>}
      </dd>
    </div>}
    <div><dt>上次成功更新</dt><dd>
      {updatedAt === undefined ? '尚未检查' : <time dateTime={new Date(updatedAt).toISOString()}>{formatDate(updatedAt, true)}</time>}
    </dd></div>
    {item.source === 'remote' && <div>
      <dt>下次更新</dt>
      <dd>{details?.updating ? '正在更新' : nextUpdateAt !== undefined
        ? <><time dateTime={new Date(nextUpdateAt).toISOString()}>{formatDate(nextUpdateAt, true)}</time>
          <small className="subscription-interval">每 {details?.intervalMinutes} 分钟</small></>
        : details ? '自动更新已关闭' : '等待加载'}</dd>
    </div>}
  </dl>
}

export default function App() {
  const { message, modal } = AntApp.useApp()
  const [items, setItems] = useState<Subscription[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [url, setUrl] = useState('')
  const [importError, setImportError] = useState('')
  const [importing, setImporting] = useState(false)
  const [results, setResults] = useState<Record<string, RefreshResult>>({})
  const [details, setDetails] = useState<Record<string, SubscriptionDetails>>({})
  const [detailsError, setDetailsError] = useState('')
  const detailsVersion = useRef(0)
  const [scheduledItem, setScheduledItem] = useState<Subscription | null>(null)
  const [interval, setIntervalMinutes] = useState<number | null>(0)
  const [scheduleSaving, setScheduleSaving] = useState(false)
  const [scheduleError, setScheduleError] = useState('')
  const [editing, setEditing] = useState<Subscription | null>(null)
  const [saving, setSaving] = useState(false)
  const [editError, setEditError] = useState('')
  const [form] = Form.useForm<Draft>()
  const [allBusy, setAllBusy] = useState(false)
  const [selection, setSelection] = useState<Selection | null>(null)
  const [selectionError, setSelectionError] = useState('')
  const [switching, setSwitching] = useState('')
  async function load() {
    setLoadError('')
    try { setItems(await api<Subscription[]>('/api/subs')) }
    catch (error) { setLoadError(error instanceof Error ? error.message : '无法加载订阅。') }
  }

  async function loadDetails() {
    const version = ++detailsVersion.current
    try {
      const records = await api<Record<string, SubscriptionDetails>>(detailsPath)
      if (version !== detailsVersion.current) return
      setDetails(records)
      setDetailsError('')
    } catch {
      if (version === detailsVersion.current) setDetailsError('无法读取服务端订阅信息。')
    }
  }

  async function loadSelection() {
    setSelectionError('')
    try { setSelection(await api<Selection>('/api/active-subscription')) }
    catch {
      setSelection(null)
      setSelectionError('无法读取路由器的当前订阅。')
    }
  }

  useEffect(() => {
    void load()
    void loadDetails()
    void loadSelection()
    const timer = window.setInterval(() => void loadDetails(), 15000)
    return () => {
      window.clearInterval(timer)
      detailsVersion.current++
    }
  }, [])

  async function activate(item: Subscription) {
    setSwitching(item.name)
    setSelectionError('')
    try {
      setSelection(await api<Selection>('/api/active-subscription', 'PUT', { id: item.name }))
    } catch {
      await loadSelection()
      setSelectionError('切换失败。请核对当前订阅后重试。')
    } finally {
      setSwitching('')
    }
  }

  async function refresh(item: Subscription) {
    setResults(current => ({ ...current, [item.name]: { state: 'loading' } }))
    try {
      const record = await api<SubscriptionDetails>(detailsPath + '/refresh', 'POST', { name: item.name })
      detailsVersion.current++
      setDetails(current => ({ ...current, [item.name]: record }))
      setResults(current => ({ ...current, [item.name]: { state: 'idle' } }))
    } catch (error) {
      const detail = error instanceof Error && error.name === 'TimeoutError'
        ? '拉取超时。请稍后重试。'
        : '无法完成更新，请稍后重试。'
      setResults(current => ({ ...current, [item.name]: { state: 'error', detail } }))
    }
  }

  async function importSubscription(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (importing || !items) return
    setImportError('')
    let source: URL
    try { source = parseUrl(url) } catch { setImportError('请输入有效的 HTTP 或 HTTPS 订阅地址。'); return }
    const existing = items.find(item => item.url?.trim() === url.trim())
    if (existing) {
      setImportError(`已添加为「${subscriptionTitle(existing)}」。`)
      if (!allBusy && results[existing.name]?.state !== 'loading') void refresh(existing)
      return
    }
    const label = (source.searchParams.get('filename')?.replace(/\.(yaml|yml)$/i, '') || source.hostname).slice(0, 60)
    const slug = label.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 45) || 'subscription'
    let name = slug
    for (let i = 2; items.some(item => item.name === name); i++) name = `${slug}-${i}`
    const item: Subscription = { name, displayName: label, source: 'remote', url: url.trim() }
    setImporting(true)
    try {
      await api('/api/subs', 'POST', item)
      setUrl('')
      setImportOpen(false)
      await load()
      void refresh(item)
    } catch (error) { setImportError(error instanceof Error ? error.message : '导入失败。') }
    finally { setImporting(false) }
  }

  async function refreshAll() {
    if (allBusy || !items) return
    setAllBusy(true)
    try { for (const item of items) if (results[item.name]?.state !== 'loading') await refresh(item) }
    finally { setAllBusy(false) }
  }

  function editSchedule(item: Subscription) {
    setScheduledItem(item)
    setIntervalMinutes(Object.hasOwn(details, item.name) ? details[item.name].intervalMinutes : 0)
    setScheduleError('')
  }

  async function saveSchedule() {
    if (!scheduledItem || interval === null || !Number.isInteger(interval) || interval < 0 || interval > maximumInterval) return
    setScheduleSaving(true)
    setScheduleError('')
    try {
      const record = await api<SubscriptionDetails>(detailsPath + '/schedule', 'PUT',
        { name: scheduledItem.name, intervalMinutes: interval })
      detailsVersion.current++
      setDetails(current => ({ ...current, [scheduledItem.name]: record }))
      setScheduledItem(null)
    } catch (error) {
      setScheduleError(error instanceof Error ? error.message : '无法保存自动更新设置。')
    } finally {
      setScheduleSaving(false)
    }
  }

  function edit(item: Subscription) {
    form.setFieldsValue({ name: subscriptionTitle(item), url: item.url || '' })
    setEditError('')
    setEditing(item)
  }

  async function save(draft: Draft) {
    if (!editing) return
    if (editing.source === 'remote') {
      try { parseUrl(draft.url) } catch { form.setFields([{ name: 'url', errors: ['请输入有效的 HTTP 或 HTTPS 订阅地址。'] }]); return }
    }
    setSaving(true)
    setEditError('')
    try {
      const path = `/api/sub/${encodeURIComponent(editing.name)}`
      const original = await api<Subscription>(path)
      const updated = { ...original, displayName: draft.name.trim(), 'display-name': draft.name.trim(),
        ...(original.source === 'remote' ? { url: draft.url.trim() } : {}) }
      await api(path, 'PATCH', updated)
      setEditing(null)
      await load()
      void loadDetails()
      if (original.url !== updated.url) void refresh(updated)
    } catch (error) { setEditError(error instanceof Error ? error.message : '保存失败。') }
    finally { setSaving(false) }
  }

  function remove(item: Subscription) {
    modal.confirm({
      title: `删除「${subscriptionTitle(item)}」？`,
      content: '删除后，需要重新导入才能恢复该订阅。',
      okText: '删除', cancelText: '取消', okButtonProps: { danger: true },
      async onOk() {
        try {
          await api(`/api/sub/${encodeURIComponent(item.name)}`, 'DELETE')
          setResults(current => ({ ...current, [item.name]: { state: 'idle' } }))
          await load()
          void loadDetails()
        }
        catch { message.error('删除失败，请稍后重试。'); throw new Error('删除失败') }
      },
    })
  }

  return (
    <>
      <main className="workspace">
        <section className="intro" aria-labelledby="page-title">
          <h1 id="page-title">订阅管理</h1>
          <p>管理订阅来源和节点。代理面板显示 Mihomo 当前运行的策略组。</p>
        </section>
        <section className="subscriptions" aria-labelledby="list-title">
          <div className="section-heading">
            <h2 id="list-title">全部订阅 {items && <span>{items.length}</span>}</h2>
            <Space wrap><Button icon={<RefreshCw />} loading={allBusy} disabled={!items?.length} onClick={() => void refreshAll()}>更新全部</Button><Button type="primary" icon={<Plus />} onClick={() => setImportOpen(true)} disabled={!items || importOpen}>导入订阅</Button></Space>
          </div>
        {importOpen && <Card className="import-card">
          <form onSubmit={importSubscription}>
            <label htmlFor="subscription-url">订阅链接</label>
            <div className="import-row">
              <Input id="subscription-url" autoFocus prefix={<Link2 />} value={url} onChange={event => setUrl(event.target.value)}
                autoComplete="off" spellCheck={false} placeholder="粘贴订阅 URL" status={importError ? 'error' : undefined}
                aria-describedby={importError ? 'import-error' : undefined} />
              <Button htmlType="submit" type="primary" icon={<Plus />} loading={importing} disabled={!url.trim() || !items}>导入</Button>
              <Button onClick={() => { setImportOpen(false); setImportError("") }} disabled={importing}>取消</Button>
            </div>
            {importError && <p id="import-error" className="error" role="alert">{importError}</p>}
          </form>
        </Card>}
          {loadError && <Alert type="error" showIcon title={loadError} action={<Button onClick={() => void load()}>重试</Button>} />}
          {detailsError && <Alert type="warning" showIcon title={detailsError} action={<Button onClick={() => void loadDetails()}>重试</Button>} />}
          {selectionError && <Alert type="error" showIcon title={selectionError}
            action={<Button disabled={!!switching} onClick={() => void loadSelection()}>重试</Button>} />}
          {selection && !selection.available.length && <Alert type="warning" showIcon title="路由器尚未绑定可切换的订阅。" />}
          {!!selection?.available.length && items && !items.some(item => item.name === selection.current) &&
            <Alert type="warning" showIcon title="路由器当前未选中列表中的订阅。" />}
          {!items && !loadError && <Skeleton active />}
          {items?.length === 0 && <Card><Empty description="还没有订阅。点击「导入订阅」添加链接。" /></Card>}
          <div className="cards">
            {items?.map(item => {
              const status = results[item.name]
              const record = Object.hasOwn(details, item.name) ? details[item.name] : undefined
              const busy = status?.state === 'loading' || !!record?.updating
              const title = subscriptionTitle(item)
              const active = selection?.current === item.name
              const selectable = selection?.available.includes(item.name)
              return <Card key={item.name} className={active ? 'subscription-card is-selected' : 'subscription-card'}>
                <div className="card-top">
                  <span className="source-icon"><Layers /></span>
                  <span className="identity"><strong>{title}</strong><small>{item.source === 'remote' ? displayHost(item.url) || '远程订阅' : '本地节点列表'}</small></span>
                  <span className="selection-indicator" aria-live="polite">{active ? '当前使用' : item.source === 'remote' ? '远程订阅' : '本地订阅'}</span>
                </div>
                <Details item={item} details={record} />
                <div className="subscription-status" aria-live="polite">
                  {busy && <p>正在拉取节点…</p>}
                  {!busy && (status?.detail || record?.lastError) && <p><strong className="error">拉取失败。</strong>{status?.detail || record?.lastError}</p>}
                </div>
                <Space wrap>
                  {selectable && !active && <Button type="primary" loading={switching === item.name}
                    disabled={!!switching} onClick={() => modal.confirm({
                      title: `使用「${title}」？`,
                      content: '切换后，路由器通过此订阅策略组建立的新连接将使用所选订阅。',
                      okText: '切换订阅', cancelText: '取消', onOk: () => activate(item),
                    })}>使用此订阅</Button>}
                  <Button icon={<RefreshCw />} loading={busy} disabled={busy || allBusy} onClick={() => void refresh(item)} aria-label={`刷新 ${title}`}>更新</Button>
                  {item.source === 'remote' && <Button type="text" onClick={() => editSchedule(item)} aria-label={`自动更新设置 ${title}`}>自动更新设置</Button>}
                  <Button type="text" icon={<Pencil />} disabled={busy || allBusy} onClick={() => edit(item)} aria-label={`编辑 ${title}`}>编辑</Button>
                  <Button type="text" icon={<Download />} href={`/subscriptions/download/${encodeURIComponent(item.name)}/ClashMeta`} download={`${item.name}.yaml`}>导出节点</Button>
                  <Button type="text" danger disabled={busy || allBusy || selectable || !!switching} icon={<Trash2 />} onClick={() => remove(item)} aria-label={`删除 ${title}`}>删除</Button>
                </Space>
              </Card>
            })}
          </div>
        </section>
      </main>
      <Modal title={scheduledItem ? `自动更新 · ${subscriptionTitle(scheduledItem)}` : '自动更新'} open={scheduledItem !== null}
        confirmLoading={scheduleSaving} onCancel={() => { if (!scheduleSaving) setScheduledItem(null) }}
        okText="保存" cancelText="取消" onOk={() => void saveSchedule()}
        okButtonProps={{ disabled: interval === null || !Number.isInteger(interval) || interval < 0 || interval > maximumInterval }}>
        <Form layout="vertical">
          <Form.Item label="更新间隔（分钟）" htmlFor="subscription-interval" extra="填 0 关闭自动更新。最大间隔为 30 天。">
            <InputNumber id="subscription-interval" min={0} max={maximumInterval} step={1} value={interval}
              onChange={setIntervalMinutes} disabled={scheduleSaving} />
          </Form.Item>
        </Form>
        {scheduleError && <Alert type="error" showIcon title={scheduleError} />}
      </Modal>
      <Modal title="编辑订阅" open={editing !== null} onCancel={() => setEditing(null)}
        onOk={() => form.submit()} okText="保存" cancelText="取消" confirmLoading={saving}>
        <Form form={form} layout="vertical" onFinish={save}>
          <Form.Item name="name" label="显示名称" rules={[{ required: true, whitespace: true }]}><Input maxLength={80} /></Form.Item>
          {editing?.source === 'remote' && <Form.Item name="url" label="订阅链接" rules={[{ required: true }]}><Input autoComplete="off" spellCheck={false} /></Form.Item>}
          {editError && <Alert type="error" showIcon title={editError} />}
        </Form>
      </Modal>
    </>
  )
}
