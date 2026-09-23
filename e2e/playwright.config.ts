import { defineConfig, devices, type ReporterDescription } from "@playwright/test";
import { currentsReporter } from "@currents/playwright";

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
  // The instance holds state (the first admin exists after the first test),
  // so tests run in file order on one worker and a retry cannot start clean.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: reporters,
  // Record every run, not only failures: the video and trace show the flow
  // on each PR.
  use: {
    ...devices["Desktop Chrome"],
    baseURL: CONTROL_PLANE_URL,
    trace: "on",
    video: "on",
    screenshot: "on",
  },
  webServer: {
    command: "./scripts/start-hoop.sh",
    url: `${CONTROL_PLANE_URL}/api/healthz`,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: "pipe",
    stderr: "pipe",
  },
});
