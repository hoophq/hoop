import { test, expect } from "@playwright/test";

// One UI bundle serves both modes; the backend picks the mode. The same flow
// runs against each project, so a change in shared code that breaks one mode
// fails here. Paths come from webapp_v2/src/modes/{gateway,controlPlane}.jsx.
const POST_AUTH_PATHS: Record<string, { setup: string; login: string }> = {
  gateway: { setup: "/onboarding/setup", login: "/client" },
  "control-plane": { setup: "/", login: "/" },
};

const admin = {
  name: "E2E Admin",
  email: "admin@e2e.hoop.test",
  password: "e2e-Admin-pass-123!",
};

test.describe.configure({ mode: "serial" });

function pathsFor(projectName: string) {
  const paths = POST_AUTH_PATHS[projectName];
  if (!paths) throw new Error(`no post-auth paths for project "${projectName}"`);
  return paths;
}

test("first admin sets up a fresh instance", async ({ page }, testInfo) => {
  const paths = pathsFor(testInfo.project.name);

  await page.goto("/login");
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole("heading", { name: "Set up your instance" })).toBeVisible();

  await page.getByLabel("Full name").fill(admin.name);
  await page.getByLabel("Work email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Create admin account" }).click();

  await expect(page).toHaveURL((url) => url.pathname === paths.setup);
});

test("admin logs in with local auth", async ({ page }, testInfo) => {
  const paths = pathsFor(testInfo.project.name);

  await page.goto("/login");
  await expect(page.getByRole("button", { name: "Login" })).toBeVisible();

  await page.getByLabel("Email").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Login" }).click();

  await expect(page).toHaveURL((url) => url.pathname === paths.login);
});
