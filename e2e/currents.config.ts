import { CurrentsConfig } from "@currents/playwright";

// The record key is a secret: it comes only from the environment. The
// Currents reporter is enabled in playwright.config.ts only when it is set.
const config: CurrentsConfig = {
  recordKey: process.env.CURRENTS_RECORD_KEY ?? "",
  projectId: "VucJwl",
};

export default config;
