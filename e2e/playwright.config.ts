import { defineConfig, devices, type ReporterDescription } from "@playwright/test";
import { currentsReporter } from "@currents/playwright";

export const GATEWAY_URL = "http://127.0.0.1:8009";
export const CONTROL_PLANE_URL = "http://127.0.0.1:8019";

// Fork PRs and local runs have no record key. Report to Currents only when
// the key is set, so those runs still pass with the local reporters.
const reporters: ReporterDescription[] = [["list"], ["html", { open: "never" }]];
if (process.env.CURRENTS_RECORD_KEY) {
  reporters.push(currentsReporter());
}

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  forbidOnly: !!process.env.CI,
  // Each instance holds state (the first admin exists after the first test),
  // so tests run in file order on one worker and a retry cannot start clean.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: reporters,
  // Record every run, not only failures: the video and trace show the flow
  // on each PR.
  use: {
    trace: "on",
    video: "on",
    screenshot: "on",
  },
  projects: [
    {
      name: "gateway",
      use: { ...devices["Desktop Chrome"], baseURL: GATEWAY_URL },
    },
    {
      name: "control-plane",
      use: { ...devices["Desktop Chrome"], baseURL: CONTROL_PLANE_URL },
    },
  ],
  webServer: [
    {
      command: "./scripts/start-hoop.sh gateway",
      url: `${GATEWAY_URL}/api/healthz`,
      timeout: 180_000,
      reuseExistingServer: false,
      stdout: "pipe",
      stderr: "pipe",
    },
    {
      command: "./scripts/start-hoop.sh control-plane",
      url: `${CONTROL_PLANE_URL}/api/healthz`,
      timeout: 180_000,
      reuseExistingServer: false,
      stdout: "pipe",
      stderr: "pipe",
    },
  ],
});
