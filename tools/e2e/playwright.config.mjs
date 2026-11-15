// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from '@playwright/test'

// The scenarios open and close real Docker stacks and stop and start the containers;
// so they run one at a time with a single worker. The durations are real: events
// come through NATS sources (catching up can take tens of seconds), the receipt and cleanup
// rounds are shortened in the test (scripts/e2e.sh).
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 5 * 60_000,
  expect: { timeout: 30_000 },
  reporter: [['list'], ['html', { outputFolder: 'out/report', open: 'never' }]],
  outputDir: 'out/artifacts',
  use: {
    locale: 'tr-TR',
    timezoneId: 'Europe/Istanbul',
    viewport: { width: 1280, height: 900 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
})
