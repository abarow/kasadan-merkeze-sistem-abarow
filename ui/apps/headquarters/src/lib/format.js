// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The format helpers are in the management screens' shared package
// (ui/packages/admin); here only the ones specific to headquarters.
export * from '@abarow/admin/lib/format.js'

export const roleName = (r) => ({ admin: 'Yönetici', catalog: 'Katalog', report: 'Rapor' })[r] ?? r
