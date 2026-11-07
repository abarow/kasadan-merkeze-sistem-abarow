// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Product and price management at the store (catalog-admin; the screen's catalog requests
// are forwarded to it through report). The shared product screen (ui/packages/admin)
// uses this client; they are the same endpoints as at headquarters. Every request
// that changes carries the version the product was at when editing started (version): if
// someone else changed it in the meantime 409 is returned, the change is not overwritten.

import { request } from './api.js'

const path = (code) => `/api/catalog/products/${encodeURIComponent(code)}`

export const catalogApi = {
  products: (q) => request('GET', `/api/catalog/products?${new URLSearchParams({ q: q ?? '' })}`).then((d) => d.products),
  product: (code) => request('GET', path(code)),
  createProduct: (p) => request('POST', '/api/catalog/products', p),
  updateProduct: (code, p) => request('PUT', path(code), p),
  // If from is empty the price takes effect immediately.
  schedulePrice: (code, version, unitPriceKurus, from) =>
    request('POST', `${path(code)}/prices`, { version, unit_price_kurus: unitPriceKurus, ...(from ? { from } : {}) }),
  cancelPrice: (code, version, from) => request('POST', `${path(code)}/prices/cancel`, { version, from }),
  setDiscontinued: (code, version, discontinued) => request('POST', `${path(code)}/discontinue`, { version, discontinued }),
  // Product image: data is base64 (the screen shrinks it before uploading).
  imageURL: (code, sha) => `${path(code)}/image?h=${sha}`,
  setImage: (code, version, data) => request('PUT', `${path(code)}/image`, { version, data }),
  removeImage: (code, version) => request('POST', `${path(code)}/image/remove`, { version }),
}
