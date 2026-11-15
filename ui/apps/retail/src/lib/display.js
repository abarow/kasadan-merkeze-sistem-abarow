// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Customer screen (HDMI second screen): the cashier screen sends the state to be shown
// to the customer window (/customer.html) in the same browser with BroadcastChannel.
// No server or session is needed; the customer window only shows
// what the cashier screen shows. If the customer window is opened later
// it asks for the last state ("hello").
//
// Durum: {status, fiscal_mode, lines: [{name, qty, amount_kurus}],
// total_kurus, paid_kurus, change_kurus}; status 'closed' (register closed),
// 'idle' (empty basket), 'basket', 'payment' or 'done' (the sale is completed).

const channel = 'abarow-customer'
const hello = 'merhaba'

// displayPublisher is the cashier screen's publisher.
export function displayPublisher() {
  const ch = new BroadcastChannel(channel)
  let last = null
  ch.onmessage = (e) => {
    if (e.data === hello && last) ch.postMessage(last)
  }
  return {
    publish(state) {
      last = state
      ch.postMessage(state)
    },
    close: () => ch.close(),
  }
}

// watchDisplay gives the states to onState in the customer window; the function it returns
// stops listening.
export function watchDisplay(onState) {
  const ch = new BroadcastChannel(channel)
  ch.onmessage = (e) => {
    if (e.data && e.data !== hello) onState(e.data)
  }
  ch.postMessage(hello)
  return () => ch.close()
}
