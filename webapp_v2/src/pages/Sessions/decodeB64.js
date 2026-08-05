/**
 * Port of `webapp.utilities/decode-b64` (utilities.cljs:43-49).
 *
 * Session event streams arrive base64-encoded. The `escape`/`decodeURIComponent`
 * dance is how v1 recovers UTF-8 from `atob`'s latin1 output; when that fails
 * the raw decoded string is used, and when even `atob` fails the result is an
 * empty string. The `∞` replacement is a column separator the gateway
 * substitutes for tabs.
 *
 * `escape` is deprecated but is exactly what v1 calls, and swapping in a
 * TextDecoder would change behaviour on malformed input — which is the case
 * this function exists to survive.
 */
export function decodeB64(data) {
  if (!data) return ''
  let decoded
  try {
    decoded = atob(data)
  } catch {
    return ''
  }
  try {
    return decodeURIComponent(escape(decoded)).replace(/∞/g, '\t')
  } catch {
    return decoded
  }
}
