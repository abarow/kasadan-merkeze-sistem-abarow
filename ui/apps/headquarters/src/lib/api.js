// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The API the headquarters screen uses (the reporting service). Each person signs in
// with their own account; the session is carried in a cookie JavaScript cannot read.

async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
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
  // The user of the open session (401 if none): {username, name, role,
  // all_stores, stores}. The role is admin (administrator), catalog or report.
  session: () => request('GET', '/api/auth/session'),
  authMethods: () => request('GET', '/api/auth/methods'),
  signIn: (body) => request('POST', '/api/auth/login', body),
  logout: () => request('POST', '/api/auth/logout', {}),
  // 404 if the corporate sign-in (OIDC) is not installed. The person's own identity link.
  oidcStatus: () => request('GET', '/api/auth/oidc'),
  oidcUnlink: () => request('POST', '/api/auth/oidc/unlink', {}),
  // The person's own password; their sessions on other devices close.
  changePassword: (current, next) => request('POST', '/api/auth/password', { current_password: current, new_password: next }),
  // The stores' connection state and today's sales; today is the date of today
  // in store time (the server determines it).
  stores: () => request('GET', '/api/stores'),
  // Period summary; dates in the form 2026-10-27, both included. An empty from is
  // today; an empty store is all the stores.
  summary: ({ from, to, store }) =>
    request('GET', `/api/reports/summary?${new URLSearchParams({ from: from ?? '', to: to ?? '', store: store ?? '' })}`),

  // Catalog management (catalog-admin). Every request that changes carries the version
  // the product was at when editing started (version): if someone else changed it in the
  // meantime 409 is returned, the change is not overwritten.
  products: (q) => request('GET', `/api/catalog/products?${new URLSearchParams({ q: q ?? '' })}`).then((d) => d.products),
  product: (code) => request('GET', `/api/catalog/products/${encodeURIComponent(code)}`),
  createProduct: (p) => request('POST', '/api/catalog/products', p),
  updateProduct: (code, p) => request('PUT', `/api/catalog/products/${encodeURIComponent(code)}`, p),
  // If from is empty the price takes effect immediately.
  schedulePrice: (code, version, unitPriceKurus, from) =>
    request('POST', `/api/catalog/products/${encodeURIComponent(code)}/prices`, {
      version,
      unit_price_kurus: unitPriceKurus,
      ...(from ? { from } : {}),
    }),
  cancelPrice: (code, version, from) =>
    request('POST', `/api/catalog/products/${encodeURIComponent(code)}/prices/cancel`, { version, from }),
  // The products priced differently from the headquarters price at the stores (now or at a future
  // date); if product is given only that product.
  storePrices: (product) =>
    request('GET', `/api/reports/store-prices?${new URLSearchParams({ product: product ?? '' })}`).then((d) => d.items),
  setDiscontinued: (code, version, discontinued) =>
    request('POST', `/api/catalog/products/${encodeURIComponent(code)}/discontinue`, { version, discontinued }),
  // Product image: data is base64 (the screen shrinks it before uploading); when the digest
  // in the address changes the browser asks for the new one.
  imageURL: (code, sha) => `/api/catalog/products/${encodeURIComponent(code)}/image?h=${sha}`,
  setImage: (code, version, data) => request('PUT', `/api/catalog/products/${encodeURIComponent(code)}/image`, { version, data }),
  removeImage: (code, version) => request('POST', `/api/catalog/products/${encodeURIComponent(code)}/image/remove`, { version }),

  // Users (administrator only). The password is only sent, never given back
  // okunmaz.
  users: () => request('GET', '/api/users').then((d) => d.users),
  user: (username) => request('GET', `/api/users/${encodeURIComponent(username)}`),
  addUser: (u) => request('POST', '/api/users', u),
  updateUser: (username, u) => request('POST', `/api/users/${encodeURIComponent(username)}`, u),
  setUserPassword: (username, password) => request('POST', `/api/users/${encodeURIComponent(username)}/password`, { password }),
  unlinkUserIdentity: (username) => request('POST', `/api/users/${encodeURIComponent(username)}/identity/unlink`, {}),
  setUserDisabled: (username, disabled) => request('POST', `/api/users/${encodeURIComponent(username)}/disabled`, { disabled }),

  // Stock (the inventory module; 404 if not installed): the headquarters warehouse and the stores.
  // Quantities are in thousandths of a unit (1000 = 1 piece or 1 kg).
  stock: (q) => request('GET', `/api/stock?${new URLSearchParams({ q: q ?? '' })}`),
  // stockSearch is a search by name when entering stock (a shared component;
  // ui/packages/admin): with the quantities in the headquarters warehouse.
  stockSearch: (q) => api.stock(q).then((d) => d.items.map((m) => ({ ...m, qty_milli: m.qty_milli.headquarters ?? 0 }))),
  stockByBarcode: (code) => request('GET', `/api/stock/barcode/${encodeURIComponent(code)}`),
  stockMovements: (id, location) =>
    request('GET', `/api/stock/products/${encodeURIComponent(id)}/movements?${new URLSearchParams({ location })}`).then((d) => d.movements),
  // Merkez deposu: lines [{product_id, qty_milli}] / [{product_id, counted_milli}]
  receive: (supplier, ref, lines) => request('POST', '/api/stock/receipts', { supplier, ref, lines }),
  count: (ref, lines) => request('POST', '/api/stock/counts', { ref, lines }),
  // Transferler: {here, locations [{id, name}], transfers, can_edit}.
  transfers: () => request('GET', '/api/stock/transfers'),
  // From the headquarters warehouse to a store: lines [{product_id, qty_milli}]
  sendTransfer: (to, lines) => request('POST', '/api/stock/transfers', { to, lines }),
  // A transfer coming to the headquarters warehouse: the quantities taken delivery of
  receiveTransfer: (id, lines) => request('POST', `/api/stock/transfers/${encodeURIComponent(id)}/receive`, { lines }),

  // Update (404 if not installed): the status of this headquarters' own agent and
  // the units' registration approval (administrator only).
  updateStatus: () => request('GET', '/api/update/status'),
  // New version: op apply ({seq}), snooze ({minutes}) or skip; administrator only.
  updateAct: (op, body) => request('POST', `/api/update/${op}`, body ?? {}),
  updateRegistrations: (status) => request('GET', `/api/update/registrations?${new URLSearchParams({ status: status ?? '' })}`),
  approveRegistration: (device, code) => request('POST', `/api/update/registrations/${encodeURIComponent(device)}/approve`, { code }),
  rejectRegistration: (device) => request('POST', `/api/update/registrations/${encodeURIComponent(device)}/reject`, {}),
  revokeRegistration: (device) => request('POST', `/api/update/registrations/${encodeURIComponent(device)}/revoke`, {}),
}
