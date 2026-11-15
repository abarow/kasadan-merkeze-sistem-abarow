// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

import { mount } from 'svelte'
import Customer from './Customer.svelte'
import './fonts.css'
import '@abarow/core/register.css'
import './app.css'

mount(Customer, { target: document.getElementById('app') })
