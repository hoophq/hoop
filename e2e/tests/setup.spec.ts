import { test, expect } from "@playwright/test";
import { admin, ADMIN_STATE, login } from "./admin";

// Runs as the "setup" project; the control-plane project depends on it and
// starts from the session saved here.
test.describe.configure({ mode: "serial" });

const LICENSE_INTRO_PATH = "/onboarding/license";

test("first admin sets up a fresh control plane", async ({ page }) => {
  await page.goto("/login");
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole("heading", { name: "Set up your instance" })).toBeVisible();

  await page.getByLabel("Full name").fill(admin.name);
  await page.getByLabel("Work email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Create admin account" }).click();

  // A free-plan admin with no sidecar lands on the license intro once.
  await expect(page).toHaveURL((url) => url.pathname === LICENSE_INTRO_PATH);
  await expect(page.getByRole("heading", { name: "License" })).toBeVisible();
  await page.getByRole("button", { name: "I don't have a license" }).click();
  await expect(page).toHaveURL((url) => url.pathname === "/sidecars");

  await page.context().storageState({ path: ADMIN_STATE });
});

test("admin logs in with local auth", async ({ page }) => {
  // A fresh browser has no skip on file, so the intro shows again.
  await login(page);
  await expect(page).toHaveURL((url) => url.pathname === LICENSE_INTRO_PATH);
});
