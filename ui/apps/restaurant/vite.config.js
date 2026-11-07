// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Restaurant screen. In development the API requests go to the gateway in the Docker
// simulation (the restaurant register). The shared parts of the register screens
// (sign-in, shift, payment, reports) are in ui/packages/core (@abarow/core);
// they are resolved from the svelte app's.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: { '@abarow/core': fileURLToPath(new URL('../../packages/core/src', import.meta.url)) },
    dedupe: ['svelte'],
  },
  server: {
    proxy: { '/api': 'http://localhost:8088' },
    fs: { allow: ['../..'] },
  },
})
