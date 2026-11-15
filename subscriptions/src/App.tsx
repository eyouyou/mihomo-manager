import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Alert, App as AntApp, Button, Card, Empty, Form, Input, Modal, Skeleton, Space } from 'antd'
import { Download, Layers, Link2, Pencil, Plus, RefreshCw, Trash2 } from './icons'

type Subscription = { name: string; displayName?: string; source: 'remote' | 'local'; url?: string }
type Draft = { name: string; url: string }
type RefreshResult = { state: 'loading' | 'ok' | 'error'; count?: number; time?: string; detail?: string }
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

export default function App() {
  const { message, modal } = AntApp.useApp()
  const [items, setItems] = useState<Subscription[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [importOpen, setImportOpen] = useState(false)
  const [url, setUrl] = useState('')
  const [importError, setImportError] = useState('')
  const [importing, setImporting] = useState(false)
  const [results, setResults] = useState<Record<string, RefreshResult>>({})
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

  async function loadSelection() {
    setSelectionError('')
    try { setSelection(await api<Selection>('/api/active-subscription')) }
    catch {
      setSelection(null)
      setSelectionError('无法读取路由器的当前订阅。')
    }
  }

  useEffect(() => { void load(); void loadSelection() }, [])

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
      const nodes = await api<unknown[]>(`/download/${encodeURIComponent(item.name)}/JSON?noCache=true`)
      if (!Array.isArray(nodes) || nodes.length === 0) throw new Error('来源没有返回可用节点。')
      setResults(current => ({ ...current, [item.name]: { state: 'ok', count: nodes.length, time: new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) } }))
    } catch (error) {
      const detail = error instanceof Error && error.name === 'TimeoutError'
        ? '拉取超时。请稍后重试。'
        : item.source === 'remote'
          ? '订阅源未返回可用节点。请检查链接，或从服务商后台复制新链接。'
          : '无法读取本地订阅的节点。'
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
      setImportError(`已添加为「${existing.displayName || existing.name}」。`)
      void refresh(existing)
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

  function edit(item: Subscription) {
    form.setFieldsValue({ name: item.displayName || item.name, url: item.url || '' })
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
      void refresh(updated)
    } catch (error) { setEditError(error instanceof Error ? error.message : '保存失败。') }
    finally { setSaving(false) }
  }

  function remove(item: Subscription) {
    modal.confirm({
      title: `删除「${item.displayName || item.name}」？`,
      content: '删除后，需要重新导入才能恢复该订阅。',
      okText: '删除', cancelText: '取消', okButtonProps: { danger: true },
      async onOk() {
        try { await api(`/api/sub/${encodeURIComponent(item.name)}`, 'DELETE'); await load() }
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
              const busy = status?.state === 'loading'
              const title = item.displayName || item.name
              const active = selection?.current === item.name
              const selectable = selection?.available.includes(item.name)
              return <Card key={item.name} className={active ? 'subscription-card is-selected' : 'subscription-card'}>
                <div className="card-top">
                  <span className="source-icon"><Layers /></span>
                  <span className="identity"><strong>{title}</strong><small>{displayHost(item.url) || '本地节点列表'}</small></span>
                  <span className="selection-indicator" aria-live="polite">{active ? '当前使用' : item.source === 'remote' ? '远程订阅' : '本地订阅'}</span>
                </div>
                <div className="result" aria-live="polite">
                  {busy ? '正在拉取节点…' : status?.state === 'ok'
                    ? <span className="success">{status.count} 个节点 · {status.time} 刷新</span>
                    : status?.state === 'error'
                      ? <><strong className="error">拉取失败</strong><p>{status.detail}</p></>
                      : '尚未检查'}
                </div>
                <Space wrap>
                  {selectable && !active && <Button type="primary" loading={switching === item.name}
                    disabled={!!switching} onClick={() => modal.confirm({
                      title: `使用「${title}」？`,
                      content: '切换后，路由器通过此订阅策略组建立的新连接将使用所选订阅。',
                      okText: '切换订阅', cancelText: '取消', onOk: () => activate(item),
                    })}>使用此订阅</Button>}
                  <Button icon={<RefreshCw />} loading={busy} disabled={allBusy} onClick={() => void refresh(item)} aria-label={`刷新 ${title}`}>更新</Button>
                  <Button type="text" icon={<Pencil />} onClick={() => edit(item)} aria-label={`编辑 ${title}`}>编辑</Button>
                  <Button type="text" icon={<Download />} href={`/subscriptions/download/${encodeURIComponent(item.name)}/ClashMeta`} download={`${item.name}.yaml`}>导出节点</Button>
                  <Button type="text" danger disabled={busy || allBusy || selectable || !!switching} icon={<Trash2 />} onClick={() => remove(item)} aria-label={`删除 ${title}`}>删除</Button>
                </Space>
              </Card>
            })}
          </div>
        </section>
      </main>
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
