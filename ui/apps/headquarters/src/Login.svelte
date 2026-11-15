<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import SignIn from '@abarow/admin/SignIn.svelte'
  import { api } from './lib/api.js'

  let { onlogin } = $props()
</script>

<SignIn heading="Merkez" loadMethods={api.authMethods} signIn={api.signIn} {onlogin} />
