import { expect, test } from '@playwright/test'

const base = process.env.SUBSCRIPTION_TEST_URL!

test.use({
  launchOptions: { executablePath: process.env.SUBSCRIPTION_TEST_BROWSER_BIN },
})

test('switches the router subscription and reads it back on reload', async ({ page }) => {
  await page.goto(base + '/management/')
  const first = page.locator('.subscription-card').filter({ hasText: '测试订阅 A' })
  const second = page.locator('.subscription-card').filter({ hasText: '测试订阅 B' })
  await expect(first.getByText('当前使用')).toBeVisible()
  await second.getByRole('button', { name: '使用此订阅' }).click()
  await page.getByRole('dialog').getByRole('button', { name: /^取\s*消$/ }).click()
  await expect(first.getByText('当前使用')).toBeVisible()
  await second.getByRole('button', { name: '使用此订阅' }).click()
  await page.getByRole('button', { name: '切换订阅', exact: true }).click()
  await expect(second.getByText('当前使用')).toBeVisible()
  await page.reload()
  await expect(second.getByText('当前使用')).toBeVisible()
  await expect(first.getByRole('button', { name: '使用此订阅' })).toBeVisible()

  await page.route('**/api/active-subscription', route => {
    return route.request().method() === 'PUT' ? route.abort() : route.continue()
  })
  await first.getByRole('button', { name: '使用此订阅' }).click()
  await page.getByRole('button', { name: '切换订阅', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: '切换失败' })).toBeVisible()
  await expect(second.getByText('当前使用')).toBeVisible()
})

test('shows unavailable state when the router has no bindings', async ({ page }) => {
  await page.goto(process.env.SUBSCRIPTION_UNBOUND_URL! + '/management/')
  await expect(page.getByText('路由器尚未绑定可切换的订阅。')).toBeVisible()
  await expect(page.getByText('当前使用')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '使用此订阅' })).toHaveCount(0)
})

test('shows subscription usage and keeps the last successful details after a failed refresh', async ({ page }) => {
  let refreshes = 0
  await page.route('**/download/**/JSON?noCache=true', async route => {
    refreshes++
    if (refreshes === 2) {
      await route.fulfill({ status: 502 })
      return
    }
    await route.fulfill({
      json: [{ name: 'node-1' }, { name: 'node-2' }],
      headers: { 'subscription-userinfo': 'upload=0; download=1073741824; total=4294967296; expire=946684800' },
    })
  })
  await page.goto(base + '/management/')
  const first = page.locator('.subscription-card').filter({ hasText: '测试订阅 A' })
  await expect(first.getByText('尚未检查')).toBeVisible()
  expect(refreshes).toBe(0)

  const refresh = first.getByRole('button', { name: '刷新 测试订阅 A' })
  await refresh.click()
  await expect(first.getByText('1 GiB', { exact: true })).toBeVisible()
  await expect(first.getByText('/ 4 GiB')).toBeVisible()
  await expect(first.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
  await expect(first.getByText('已到期')).toBeVisible()
  await expect(first.getByText('2', { exact: true })).toBeVisible()

  await refresh.click()
  await expect(first.getByText('拉取失败。')).toBeVisible()
  await expect(first.getByText('1 GiB', { exact: true })).toBeVisible()
  await page.reload()
  await expect(first.getByText('1 GiB', { exact: true })).toBeVisible()
  await expect(first.getByText('尚未检查')).toHaveCount(0)
  expect(refreshes).toBe(2)

  await page.unroute('**/download/**/JSON?noCache=true')
  await page.route('**/download/**/JSON?noCache=true', route => route.fulfill({ json: [{ name: 'node-1' }] }))
  await refresh.click()
  await expect(first.getByText('服务商未提供', { exact: true })).toHaveCount(2)
  await expect(first.getByRole('progressbar')).toHaveCount(0)
})
