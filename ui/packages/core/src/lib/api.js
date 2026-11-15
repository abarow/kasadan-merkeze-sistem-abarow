// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The register screens' shared API: sign-in, shift, payment, cancellation and reports
// go to the core; the module's (market, restaurant) endpoints are added by the app with request
// (/api/module). All through the gateway. Requests other than sign-in carry
// the cashier's session key; sale requests ask for the cashier's open
// shift (409 if none).

const TOKEN_KEY = 'abarow.token'

// The session key is deleted when the tab closes (sessionStorage); if storage
// is unavailable it is kept only in memory.
let token = ''
try {
  token = sessionStorage.getItem(TOKEN_KEY) ?? ''
} catch {}

let authLost = () => {}

export function setToken(value) {
  token = value
  try {
    if (value) sessionStorage.setItem(TOKEN_KEY, value)
    else sessionStorage.removeItem(TOKEN_KEY)
  } catch {}
}

export const hasToken = () => token !== ''

// onAuthLost registers the function to be called when the session becomes invalid (401).
export function onAuthLost(fn) {
  authLost = fn
}

// The manager approval key is carried with this header (see ManagerApproval).
export const approvalHeader = (approval) => (approval ? { 'X-Abarow-Approval': approval } : {})

// token is the session key (for module screens that open streams themselves).
export const currentToken = () => token

export async function request(method, path, body, extraHeaders = {}) {
  const headers = { ...extraHeaders }
  if (body) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(path, { method, headers, body: body ? JSON.stringify(body) : undefined })
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}))
  if (!res.ok) {
    if (res.status === 401 && token) {
      setToken('')
      authLost()
    }
    const err = new Error(data.error || `İstek başarısız (${res.status})`)
    err.status = res.status
    // The body of the response (e.g. the latest state that came with the error).
    err.data = data
    throw err
  }
  return data
}

// watchDevice gives the peripherals' events: the barcode read
// onScan(code), the printer's state onPrinter({state, message}; first the current,
// then every change). The gateway's stream (/api/device/scans, Server-Sent
// Events) is opened with the session key; since EventSource cannot carry headers it is read
// with fetch. If the stream breaks it reconnects after 2 seconds; it stops when signal
// is aborted. It does not connect if device is not configured (404) or the session
// has ended (401).
export async function watchDevice({ onScan, onPrinter }, signal) {
  while (!signal.aborted) {
    try {
      const res = await fetch('/api/device/scans', { headers: { Authorization: `Bearer ${token}` }, signal })
      if (res.status === 404 || res.status === 401) return
      if (res.ok && res.body) {
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
            const name = event.match(/^event: (.*)$/m)?.[1]
            const data = event.match(/^data: (.*)$/m)?.[1]
            if (!data) continue
            if (name === 'scan') onScan(JSON.parse(data).code)
            if (name === 'printer') onPrinter(JSON.parse(data))
          }
        }
      }
    } catch {
      if (signal.aborted) return
    }
    await new Promise((resolve) => setTimeout(resolve, 2000))
  }
}

export const api = {
  config: () => request('GET', '/api/config'),
  cashiers: () => request('GET', '/api/auth/cashiers'),
  login: (cashierId, pin) => request('POST', '/api/auth/login', { cashier_id: cashierId, pin }),
  me: () => request('GET', '/api/auth/me'),
  logout: () => request('POST', '/api/auth/logout'),
  // action: 'close_day' | 'cash_out' | 'close_shift'; a single-use approval key is returned.
  approve: (managerId, pin, action) => request('POST', '/api/auth/approve', { manager_id: managerId, pin, action }),
  shift: () => request('GET', '/api/shift'),
  openShift: (openingCashKurus) => request('POST', '/api/shift/open', { opening_cash_kurus: openingCashKurus }),
  closeShift: (countedCashKurus, approval) =>
    request('POST', '/api/shift/close', { counted_cash_kurus: countedCashKurus }, approvalHeader(approval)),
  // kind: 'in' putting money into the register, 'out' taking money out (manager or manager approval).
  cashMovement: (kind, amountKurus, note, approval) =>
    request('POST', '/api/shift/cash', { kind, amount_kurus: amountKurus, note }, approvalHeader(approval)),
  // Opens the drawer without a sale (manager or manager approval); the opening is recorded in the shift.
  openDrawer: (reason, approval) => request('POST', '/api/shift/drawer', { reason }, approvalHeader(approval)),
  // The update agent's status and approval code (404 if not installed).
  updateStatus: () => request('GET', '/api/update/status'),
  // New version: op apply ({seq}), snooze ({minutes}) or skip; asks for a manager session or manager approval.
  updateAct: (op, body, approval) => request('POST', `/api/update/${op}`, body ?? {}, approvalHeader(approval)),
  shifts: (limit = 20) => request('GET', `/api/shifts?limit=${limit}`),
  shiftReport: (id) => request('GET', `/api/shifts/${id}/report`),
  // Sends the reports to the receipt printer; if zNo is 0 the X report of the open period.
  printDay: (zNo) => request('POST', zNo ? `/api/reports/z/${zNo}/print` : '/api/reports/x/print'),
  printShift: (id) => request('POST', `/api/shifts/${id}/print`),
  sale: (id) => request('GET', `/api/sales/${id}`),
  // status 'charged': sales whose payment was taken but whose receipt could not be printed.
  sales: (status) => request('GET', `/api/sales?status=${status}`),
  pay: (saleId, payments) => request('POST', `/api/sales/${saleId}/checkout`, { payments }),
  voidSale: (saleId) => request('POST', `/api/sales/${saleId}/void`),
  // Refunds the payment of a sale whose receipt could not be printed (manager or manager approval).
  refundCharged: (saleId, approval) => request('POST', `/api/sales/${saleId}/refund`, undefined, approvalHeader(approval)),
  xReport: () => request('GET', '/api/reports/x'),
  closeDay: (approval) => request('POST', '/api/reports/z', undefined, approvalHeader(approval)),
}
