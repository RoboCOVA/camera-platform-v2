import { test, expect } from '@playwright/test'

test('sign-in page loads', async ({ page }) => {
  await page.goto('/auth/signin')
  await expect(page.getByRole('heading', { name: 'Cam Platform' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign in with SSO' })).toBeVisible()
})
