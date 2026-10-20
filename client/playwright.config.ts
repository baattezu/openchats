import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  use: {
    baseURL: "http://127.0.0.1:5175",
    browserName: "chromium",
    launchOptions: process.env.PW_EXECUTABLE_PATH
      ? { executablePath: process.env.PW_EXECUTABLE_PATH }
      : undefined,
  },
  webServer: {
    command: "npm run dev",
    url: "http://127.0.0.1:5175",
    reuseExistingServer: !process.env.CI,
  },
  reporter: "list",
});
