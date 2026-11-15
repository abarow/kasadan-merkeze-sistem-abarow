// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// A product image is shrunk in the browser before uploading: the long side at most
// 640 px, JPEG. The server's limit is 512 KB; if exceeded it is tried again with lower quality.

const MAX_SIDE = 640
const MAX_BYTES = 512 * 1024

// shrink shrinks the file and returns base64 (without the data prefix).
export async function shrink(file) {
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error('Görsel açılamadı (JPEG, PNG ya da WebP seçin).')
  })
  const scale = Math.min(1, MAX_SIDE / Math.max(bitmap.width, bitmap.height))
  const canvas = document.createElement('canvas')
  canvas.width = Math.round(bitmap.width * scale)
  canvas.height = Math.round(bitmap.height * scale)
  const ctx = canvas.getContext('2d')
  // A transparent PNG gets a white background (JPEG keeps no transparency).
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, canvas.width, canvas.height)
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
  bitmap.close()
  for (const quality of [0.85, 0.7, 0.5]) {
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality))
    if (blob && blob.size <= MAX_BYTES) return toBase64(blob)
  }
  throw new Error('Görsel küçültülse de 512 KB\'ı aşıyor.')
}

function toBase64(blob) {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ''))
    r.onerror = () => reject(new Error('Görsel okunamadı.'))
    r.readAsDataURL(blob)
  })
}
