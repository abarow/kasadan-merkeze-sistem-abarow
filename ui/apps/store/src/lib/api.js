// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The API the store server screen uses (the report service). The session is opened with the manager code
// and PIN or with the store password and is carried in a cookie
// JavaScript cannot read.

export async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    // Write requests go as JSON even without a body (the server accepts only JSON).
    headers: method === 'GET' ? {} : { 'Content-Type': 'application/json' },
    body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
  })
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.error || `İstek başarısız (${res.status})`)
    err.status = res.status
    throw err
  }
  return data
}

export const api = {
  // The person who opened the session: {username, name, role, ...} (401 if none); username is empty for one who signed in with the store password.
  session: () => request('GET', '/api/auth/session'),
  // The open sign-in routes: the manager PIN if there is a manager with an open account, otherwise the store password.
  authMethods: () => request('GET', '/api/auth/methods'),
  signIn: (body) => request('POST', '/api/auth/login', body),
  logout: () => request('POST', '/api/auth/logout'),
  // 404 if the corporate sign-in (OIDC) is not installed. The person's own identity link.
  oidcStatus: () => request('GET', '/api/auth/oidc'),
  oidcUnlink: () => request('POST', '/api/auth/oidc/unlink', {}),
  // Whether headquarters counts as connected (should the products come from headquarters, be read-only
  // at the store or be managed at the store): {installed, connected,
  // catalog_writable}. The manager changes the setting; applied in the set response says
  // the catalog stream was applied immediately.
  headquarters: () => request('GET', '/api/settings/headquarters'),
  setHeadquarters: (connected) => request('POST', '/api/settings/headquarters', { connected }),
  // The registers' connection state, the receipts that came and the missing receipts.
  registers: () => request('GET', '/api/registers').then((d) => d.registers),
  // The daily store report (the same endpoint and format as headquarters' summary); date
  // is in the form 2026-10-26. If empty today: the day is determined in store time
  // (at the server), not by the browser's clock.
  day: (date) => request('GET', `/api/reports/summary?from=${encodeURIComponent(date ?? '')}`),

  // Store prices (exceptions): a price different from the headquarters price, immediate or
  // future-dated. 404 if the store server is not connected to headquarters.
  prices: (q) => request('GET', `/api/prices?${new URLSearchParams({ q: q ?? '' })}`).then((d) => d.products),
  price: (id) => request('GET', `/api/prices/${encodeURIComponent(id)}`),
  // If from is empty immediately.
  setPrice: (id, unitPriceKurus, from) =>
    request('POST', `/api/prices/${encodeURIComponent(id)}`, { unit_price_kurus: unitPriceKurus, ...(from ? { from } : {}) }),
  clearPrice: (id, from) => request('POST', `/api/prices/${encodeURIComponent(id)}/clear`, from ? { from } : {}),
  cancelPrice: (id, from) => request('POST', `/api/prices/${encodeURIComponent(id)}/cancel`, { from }),

  // Cashiers: the changes go down to the registers. The PIN is only sent, never
  // geri okunmaz.
  cashiers: () => request('GET', '/api/cashiers').then((d) => d.cashiers),
  cashier: (id) => request('GET', `/api/cashiers/${encodeURIComponent(id)}`),
  addCashier: (c) => request('POST', '/api/cashiers', c),
  updateCashier: (id, name, role, email) => request('POST', `/api/cashiers/${encodeURIComponent(id)}`, { name, role, email }),
  unlinkCashierIdentity: (id) => request('POST', `/api/cashiers/${encodeURIComponent(id)}/identity/unlink`, {}),
  setCashierPIN: (id, pin) => request('POST', `/api/cashiers/${encodeURIComponent(id)}/pin`, { pin }),
  setCashierDisabled: (id, disabled) => request('POST', `/api/cashiers/${encodeURIComponent(id)}/disabled`, { disabled }),

  // Store stock (the inventory module; 404 if not installed). Quantities are in thousandths
  // birimdir (1000 = 1 adet ya da 1 kg).
  // stock is the store's stock: the response carries quantities per place (the same
  // service as headquarters); here this store's quantity is taken.
  stock: (q) =>
    request('GET', `/api/stock?${new URLSearchParams({ q: q ?? '' })}`).then((d) =>
      d.items.map((it) => ({ ...it, qty_milli: it.qty_milli[d.here] ?? 0 })),
    ),
  // stockSearch is a search by name when entering stock (a shared component; ui/packages/admin).
  stockSearch: (q) => api.stock(q),
  stockByBarcode: (code) => request('GET', `/api/stock/barcode/${encodeURIComponent(code)}`),
  stockMovements: (id) => request('GET', `/api/stock/products/${encodeURIComponent(id)}/movements`).then((d) => d.movements),
  // lines: [{product_id, qty_milli}]
  receive: (supplier, ref, lines) => request('POST', '/api/stock/receipts', { supplier, ref, lines }),
  // lines: [{product_id, counted_milli}]
  count: (ref, lines) => request('POST', '/api/stock/counts', { ref, lines }),
  // Transfers: {here, transfers}; the places exclude this store (headquarters publishes).
  transfers: () => request('GET', '/api/stock/transfers'),
  stockLocations: () => request('GET', '/api/stock/locations').then((d) => d.locations),
  // lines: [{product_id, qty_milli}]
  sendTransfer: (to, lines) => request('POST', '/api/stock/transfers', { to, lines }),
  // lines: the quantities taken delivery of [{product_id, qty_milli}]
  receiveTransfer: (id, lines) => request('POST', `/api/stock/transfers/${encodeURIComponent(id)}/receive`, { lines }),
  // The update agent's status and approval code (404 if not installed).
  updateStatus: () => request('GET', '/api/update/status'),
  // New version: op apply ({seq}), snooze ({minutes}) or skip; manager only.
  updateAct: (op, body) => request('POST', `/api/update/${op}`, body ?? {}),
}
