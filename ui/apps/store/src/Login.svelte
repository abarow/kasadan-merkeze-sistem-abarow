<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import SignIn from '@abarow/admin/SignIn.svelte'
  import { api } from './lib/api.js'

  // onlogin(who): the person who signed in ({username, name, role}).
  let { onlogin } = $props()
</script>

<SignIn heading="Mağaza" loadMethods={api.authMethods} signIn={api.signIn} {onlogin} />
