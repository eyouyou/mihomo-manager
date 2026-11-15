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

test('shows server details in a new browser context and saves the next update time', async ({ page, browser }) => {
  await page.goto(base + '/management/')
  const first = page.locator('.subscription-card').filter({ hasText: '测试订阅 A' })
  await expect(first.getByText('自动更新已关闭')).toBeVisible()
  await expect(first.getByText('尚未检查')).toBeVisible()

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

  await first.getByRole('button', { name: '自动更新设置 测试订阅 A' }).click()
  await page.getByRole('spinbutton', { name: '更新间隔（分钟）' }).fill('60')
  await page.getByRole('dialog').getByRole('button', { name: /^保\s*存$/ }).click()
  await expect(first.getByText('每 60 分钟')).toBeVisible()

  const nextContext = await browser.newContext()
  try {
    const otherPage = await nextContext.newPage()
    await otherPage.goto(base + '/management/')
    const otherCard = otherPage.locator('.subscription-card').filter({ hasText: '测试订阅 A' })
    await expect(otherCard.getByText('1 GiB', { exact: true })).toBeVisible()
    await expect(otherCard.getByText('拉取失败。')).toBeVisible()
    await expect(otherCard.getByText('每 60 分钟')).toBeVisible()
    await expect(otherCard.locator('dd').filter({ hasText: '每 60 分钟' }).locator('time')).toHaveAttribute('datetime', /T/)
  } finally {
    await nextContext.close()
  }

  await refresh.click()
  await expect(first.getByText('服务商未提供', { exact: true })).toHaveCount(2)
  await expect(first.getByRole('progressbar')).toHaveCount(0)
})
