import { expect, type Page } from "@playwright/test";

// The first admin. setup.spec.ts creates it and saves its session to
// ADMIN_STATE; the control-plane project starts from that session.
export const admin = {
  name: "E2E Admin",
  email: "admin@e2e.hoop.test",
  password: "e2e-Admin-pass-123!",
};

export const ADMIN_STATE = ".auth/admin.json";

export async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Login" }).click();
  await expect(page).not.toHaveURL(/\/login$/);
}
