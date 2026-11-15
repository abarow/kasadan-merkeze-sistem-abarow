// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The restaurant screen's API: the shared register API (@abarow/core) and the restaurant
// module's endpoints (/api/module). There are no business rules on the screen; the waiter
// phone will use the same endpoints later (DESIGN section 20).

import { api as core, approvalHeader, currentToken, request } from '@abarow/core/lib/api.js'

export { hasToken, onAuthLost, setToken, watchDevice } from '@abarow/core/lib/api.js'

// newLineID is the line's id: even if the request is sent again a second line
// is not opened.
export const newLineID = () =>
  crypto.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`

const order = (id) => `/api/module/orders/${encodeURIComponent(id)}`

export const api = {
  ...core,
  menu: () => request('GET', '/api/module/menu').then((d) => d.categories),
  floor: () => request('GET', '/api/module/floor').then((d) => d.areas),
  // Manager or manager approval (edit_floor).
  setFloor: (areas, approval) => request('PUT', '/api/module/floor', { areas }, approvalHeader(approval)).then((d) => d.areas),
  openTable: (tableID) => request('POST', `/api/module/tables/${encodeURIComponent(tableID)}/open`),
  order: (id) => request('GET', order(id)),
  // choices: [{group, options: [option id]}]
  addLine: (id, line) => request('POST', `${order(id)}/lines`, line),
  // qty 0: the line is deleted (only a line not sent).
  setLine: (id, lineID, qty, note) => request('PATCH', `${order(id)}/lines/${encodeURIComponent(lineID)}`, { qty, note }),
  // Manager or manager approval (void_order_line).
  voidLine: (id, lineID, qty, reason, approval) =>
    request('POST', `${order(id)}/lines/${encodeURIComponent(lineID)}/void`, { qty, reason }, approvalHeader(approval)),
  send: (id) => request('POST', `${order(id)}/send`),
  bill: (id) => request('POST', `${order(id)}/bill`),
  // If parts is empty everything that is open; returns: {order, sale_id}.
  payOrder: (id, version, parts) => request('POST', `${order(id)}/pay`, { version, parts }),
  settle: (id) => request('POST', `${order(id)}/settle`),
  move: (id, version, tableID) => request('POST', `${order(id)}/move`, { version, table_id: tableID }),
  merge: (id, version, fromID, fromVersion) =>
    request('POST', `${order(id)}/merge`, { version, from_order_id: fromID, from_version: fromVersion }),
  closeEmpty: (id) => request('POST', `${order(id)}/close`),
}

// watchChanges listens to the restaurant's change stream (a change made on one terminal or
// phone is refreshed on the other screens): for every event
// onChange({type, order_id, table_id, version}). If the stream breaks it reconnects after 2 seconds
// and asks for everything to be refreshed with onChange(null).
export async function watchChanges(onChange, signal) {
  while (!signal.aborted) {
    try {
      const res = await fetch('/api/module/events', { headers: { Authorization: `Bearer ${currentToken()}` }, signal })
      if (res.status === 401 || res.status === 404) return
      if (res.ok && res.body) {
        onChange(null)
        const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
        let buf = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += value
          let end
          while ((end = buf.indexOf('\n\n')) >= 0) {
            const event = buf.slice(0, end)
            buf = buf.slice(end + 2)
            const data = event.match(/^data: (.*)$/m)?.[1]
            if (data) onChange(JSON.parse(data))
          }
        }
      }
    } catch {
      if (signal.aborted) return
    }
    await new Promise((resolve) => setTimeout(resolve, 2000))
  }
}
