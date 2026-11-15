// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In development the API requests go to the reporting service in the simulation:
//   bash scripts/store-sim.sh
//   npm run dev
// The management screens' (store server, headquarters) shared parts
// are in ui/packages/admin (@abarow/admin); they are resolved from the
// svelte app's.
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: { '@abarow/admin': fileURLToPath(new URL('../../packages/admin/src', import.meta.url)) },
    dedupe: ['svelte'],
  },
  server: {
    fs: { allow: ['../..'] },
    proxy: { '/api': 'http://localhost:9090' },
  },
})
