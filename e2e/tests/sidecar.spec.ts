import { spawn, type ChildProcess } from "node:child_process";
import { createWriteStream, writeFileSync } from "node:fs";
import { test, expect } from "@playwright/test";
import { CONTROL_PLANE_URL } from "../urls";

// One listener: the control plane answers 412 to a sidecar with none, and
// the sidecar then imports this file. No database is needed to connect.
const SIDECAR_CONFIG = `audit: {file: "-"}
listeners:
  - {name: appdb, protocol: postgres, listen: 127.0.0.1:15432, upstream: 127.0.0.1:5432}
`;

let sidecar: ChildProcess | undefined;

test.afterEach(async () => {
  if (sidecar && sidecar.exitCode === null) {
    sidecar.kill("SIGTERM");
  }
  sidecar = undefined;
});

function startSidecar(token: string, configPath: string, logPath: string) {
  const hoopBin = process.env.HOOP_BIN;
  if (!hoopBin) throw new Error("HOOP_BIN is not set");

  const log = createWriteStream(logPath);
  const proc = spawn(hoopBin, ["start", "sidecar", "--config", configPath, "--token", token], {
    env: {
      ...process.env,
      HOOP_CONTROL_PLANE_URL: CONTROL_PLANE_URL,
      HOOP_SIDECAR_ANALYTICS: "off",
      DO_NOT_TRACK: "1",
    },
  });
  proc.stdout?.pipe(log);
  proc.stderr?.pipe(log);
  return proc;
}

test("a new sidecar connects to the control plane", async ({ page }, testInfo) => {
  const name = "e2e-sidecar";

  await page.goto("/sidecars");
  await expect(page.getByRole("heading", { name: "Sidecars" })).toBeVisible();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Create and deploy a new Sidecar" })).toBeVisible();

  // The token is shown once and lives only in page state; read it from the
  // create response.
  await page.getByLabel("Name").fill(name);
  const [created] = await Promise.all([
    page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/api/sidecars"),
    page.getByRole("button", { name: "Create sidecar" }).click(),
  ]);
  expect(created.status()).toBe(201);
  const { token } = (await created.json()) as { token?: string };
  expect(token, "POST /api/sidecars returned no token").toMatch(/^hsc_/);
  await expect(page.getByText(token!).first()).toBeVisible();

  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText("Waiting for the sidecar to connect")).toBeVisible();

  const configPath = testInfo.outputPath("sidecar.yaml");
  const logPath = testInfo.outputPath("sidecar.log");
  writeFileSync(configPath, SIDECAR_CONFIG);
  sidecar = startSidecar(token!, configPath, logPath);
  testInfo.attachments.push({ name: "sidecar.log", path: logPath, contentType: "text/plain" });

  // The wizard polls every 3s and moves to the overview on the first check-in.
  await expect(page.getByRole("button", { name: "Finish" })).toBeEnabled({ timeout: 45_000 });
  await expect(page.getByText("Last seen")).toBeVisible();

  await page.getByRole("button", { name: "Finish" }).click();
  await expect(page).toHaveURL((url) => url.pathname === "/sidecars");
  const row = page.getByRole("row").filter({ hasText: name });
  await expect(row.getByText("Connected")).toBeVisible();
});
