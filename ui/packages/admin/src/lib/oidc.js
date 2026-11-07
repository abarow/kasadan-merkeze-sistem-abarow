// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// When the corporate sign-in (OIDC) returns from the IdP it adds the result to the screen's address
// (pkg/login/oidc): oidc_error=<kod> ya da oidc_linked=1. Adreste ileti
// it carries no message, only a code: the screen writes the text itself; random
// text written in the address is not shown on the screen.
const errors = {
  denied: 'Kurumsal girişte kimlik doğrulanamadı ya da giriş reddedildi.',
  expired: 'Kurumsal giriş zaman aşımına uğradı ya da bu tarayıcıdan başlatılmamıştı; yeniden deneyin.',
  refused:
    'Bu kurumsal kimlikle giriş yapılamaz: kimliğe bağlı açık bir hesap yok. Yöneticiden e-posta adresinizi hesabınıza tanımlamasını isteyin.',
  unavailable: 'Kurumsal girişe ya da kayıtlara ulaşılamadı; biraz sonra yeniden deneyin.',
  taken: 'Bu kurumsal kimlik başka bir hesaba bağlı.',
}

function take() {
  const q = new URLSearchParams(location.search)
  const code = q.get('oidc_error')
  const linked = q.get('oidc_linked') === '1'
  if (code === null && !linked) return {}
  q.delete('oidc_error')
  q.delete('oidc_linked')
  const rest = q.toString()
  history.replaceState(null, '', location.pathname + (rest ? `?${rest}` : '') + location.hash)
  return { error: code === null ? '' : (errors[code] ?? 'Kurumsal giriş başarısız.'), linked }
}

// It is read once when the page opens and deleted from the address: the sign-in screen and the
// screen after the session is opened see the same result.
export const oidcNotice = take()
