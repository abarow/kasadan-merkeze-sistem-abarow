// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The market screen's API: the shared register API (@abarow/core) and the market
// module's basket and product endpoints (/api/module).

import { api as core, request } from '@abarow/core/lib/api.js'

export { hasToken, onAuthLost, setToken, watchDevice } from '@abarow/core/lib/api.js'

export const api = {
  ...core,
  newBasket: () => request('POST', '/api/module/baskets'),
  // The last basket the cashier can continue; 404 if none.
  currentBasket: () => request('GET', '/api/module/baskets/current'),
  addBarcode: (id, barcode) => request('POST', `/api/module/baskets/${id}/items`, { barcode }),
  setQty: (id, line, qtyMilli) => request('PATCH', `/api/module/baskets/${id}/lines/${line}`, { qty_milli: qtyMilli }),
  removeLine: (id, line) => request('DELETE', `/api/module/baskets/${id}/lines/${line}`),
  checkoutBasket: (id) => request('POST', `/api/module/baskets/${id}/checkout`),
  reopenBasket: (id) => request('POST', `/api/module/baskets/${id}/reopen`),
  products: (query = '') => request('GET', `/api/module/products?q=${encodeURIComponent(query)}`),
}
