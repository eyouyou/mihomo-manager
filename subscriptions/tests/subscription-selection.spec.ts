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
