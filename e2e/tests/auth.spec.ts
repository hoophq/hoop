import { test, expect } from "@playwright/test";

// Post-auth path of the control-plane mode (webapp_v2/src/modes/controlPlane.jsx).
const HOME_PATH = "/";

const admin = {
  name: "E2E Admin",
  email: "admin@e2e.hoop.test",
  password: "e2e-Admin-pass-123!",
};

test.describe.configure({ mode: "serial" });

test("first admin sets up a fresh control plane", async ({ page }) => {
  await page.goto("/login");
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole("heading", { name: "Set up your instance" })).toBeVisible();

  await page.getByLabel("Full name").fill(admin.name);
  await page.getByLabel("Work email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Create admin account" }).click();

  await expect(page).toHaveURL((url) => url.pathname === HOME_PATH);
});

test("admin logs in with local auth", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByRole("button", { name: "Login" })).toBeVisible();

  await page.getByLabel("Email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Login" }).click();

  await expect(page).toHaveURL((url) => url.pathname === HOME_PATH);
});
