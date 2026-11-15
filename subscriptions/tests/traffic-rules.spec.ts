import { expect, test } from '@playwright/test'

const base = process.env.TRAFFIC_RULES_TEST_URL

test.skip(!base, 'Requires the rules test server')

test.use({
  launchOptions: { executablePath: process.env.SUBSCRIPTION_TEST_BROWSER_BIN },
})

test('lists every live rule and previews draft edits without writing to the gateway', async ({ page }) => {
  const apiMethods: string[] = []
  page.on('request', request => {
    if (request.url().endsWith('/traffic-rules/api/rules')) apiMethods.push(request.method())
  })
  await page.goto(base + '/management/?view=rules')
  await expect(page.getByRole('tab', { name: '流量规则' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByText('76 条规则')).toBeVisible()
  await expect(page.getByRole('row')).toHaveCount(77)
  await expect(page.getByText('Fixture-Download')).toBeVisible()
  await expect(page.getByRole('switch', { name: '规则 2 启用状态' })).not.toBeChecked()

  await page.getByRole('switch', { name: '规则 2 启用状态' }).click()
  await page.getByRole('button', { name: '上移规则 2', exact: true }).click()
  await page.getByRole('button', { name: '校验并预览变更' }).click()

  const preview = page.getByRole('dialog', { name: '变更预览' })
  await expect(preview.getByText('2 → 1')).toBeVisible()
  await expect(preview.getByText('停用', { exact: true })).toBeVisible()
  await preview.getByRole('button', { name: '返回草稿' }).click()
  await page.getByRole('button', { name: '重置草稿' }).click()
  await page.getByRole('button', { name: /^清\s*除$/, exact: true }).click()
  await expect(page.getByRole('switch', { name: '规则 2 启用状态' })).not.toBeChecked()
  await expect(page.getByRole('button', { name: '校验并预览变更', exact: true })).toBeDisabled()
  expect(apiMethods).toEqual(['GET', 'GET'])
})

test('adds, edits, searches and deletes in the draft, with a before and after preview', async ({ page }) => {
  const methods: string[] = []
  page.on('request', request => {
    if (request.url().endsWith('/traffic-rules/api/rules')) methods.push(request.method())
  })
  await page.goto(base + '/management/?view=rules')
  await expect(page.getByText('76 条规则')).toBeVisible()

  await page.getByRole('button', { name: '新增规则' }).click()
  const create = page.getByRole('dialog', { name: '新增规则' })
  await expect(create.getByLabel('类型')).toHaveValue('DOMAIN')
  await create.getByLabel('匹配条件').fill('draft.example.test')
  await create.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('77 条规则')).toBeVisible()

  await page.getByLabel('查找规则').fill('rule-0.example.test')
  await expect(page.getByRole('row')).toHaveCount(2)
  await page.getByRole('button', { name: '编辑规则 1', exact: true }).click()
  const edit = page.getByRole('dialog', { name: '编辑规则' })
  await edit.getByLabel('匹配条件').fill('edited.example.test')
  await edit.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('edited.example.test')).toBeVisible()

  await page.getByRole('button', { name: '删除规则 76', exact: true }).click()
  await page.getByRole('button', { name: /^删\s*除$/, exact: true }).click()
  await page.getByRole('button', { name: /^校验并预览/ }).click()
  const preview = page.getByRole('dialog', { name: '变更预览' })
  await expect(preview.getByText('draft.example.test')).toBeVisible()
  await expect(preview.getByText('rule-0.example.test')).toBeVisible()
  await expect(preview.getByText('edited.example.test')).toBeVisible()
  await expect(preview.getByText('删除', { exact: true })).toBeVisible()
  expect(methods).toEqual(['GET', 'GET'])
})

test('restores saved drafts after targeted reload and flags a stale router baseline', async ({ page }) => {
  let changed = false
  await page.route('**/traffic-rules/api/rules', async route => {
    const response = await route.fetch()
    const snapshot = await response.json()
    if (changed) snapshot.rules[0].proxy = 'updated-live-policy'
    await route.fulfill({ response, json: snapshot })
  })
  await page.goto(base + '/management/?view=rules')
  await expect(page.getByText('76 条规则')).toBeVisible()
  await page.getByRole('switch', { name: '规则 2 启用状态' }).click()
  await page.reload()
  await expect(page.getByRole('switch', { name: '规则 2 启用状态' })).toBeChecked()

  changed = true
  await page.getByRole('button', { name: /^校验并预览/ }).click()
  const preview = page.getByRole('dialog', { name: '变更预览' })
  await expect(preview.getByText('现网规则已变更。草稿仍保留，请对照现网重新编辑。')).toBeVisible()
  await preview.getByRole('button', { name: '返回草稿' }).click()
  await page.reload()
  await expect(page.getByRole('switch', { name: '规则 2 启用状态' })).toBeChecked()
  await expect(page.getByText('现网规则与草稿基线不同。草稿继续保留。')).toBeVisible()
})

test('asks for confirmation before applying a validated candidate', async ({ page }) => {
  let previews = 0
  let applications = 0
  await page.route('**/traffic-rules/api/rules', async route => {
    const current = await route.fetch()
    await route.fulfill({ response: current, json: { ...await current.json(), canPublish: true } })
  })
  await page.route('**/traffic-rules/api/preview', route => {
    previews++
    return route.fulfill({ json: { ticket: 'a'.repeat(64) } })
  })
  await page.route('**/traffic-rules/api/apply', route => {
    applications++
    expect(route.request().postDataJSON().ticket).toBe('a'.repeat(64))
    return route.fulfill({ json: { backup: 'fixture-backup' } })
  })
  await page.goto(base + '/management/?view=rules')
  await page.getByRole('switch', { name: '规则 2 启用状态' }).click()
  await page.getByRole('button', { name: '校验并预览变更' }).click()
  await expect(page.getByText('Mihomo 已通过候选配置校验。', { exact: false })).toBeVisible()
  expect(previews).toBe(1)
  expect(applications).toBe(0)
  await page.getByRole('button', { name: '应用到路由器' }).click()
  expect(applications).toBe(0)
  await page.getByRole('button', { name: '确认应用' }).click()
  await expect(page.getByText('热加载和启动配置写入均已完成。回滚备份：fixture-backup')).toBeVisible()
  expect(applications).toBe(1)
})
