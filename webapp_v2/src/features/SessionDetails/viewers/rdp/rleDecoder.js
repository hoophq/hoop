import { decodeBase64 } from './rdpLogic'

// /rdpclient/rle.js defines window.rdpRleDecompress (RDP interleaved RLE) and
// window.rdpBitmapToRGBA (bottom-up BGR to RGBA). It is shared with the
// ClojureScript app, so it stays a script tag rather than a module import.
const RLE_SRC = '/rdpclient/rle.js'
let loading = null

const ready = () => typeof window.rdpRleDecompress === 'function' && typeof window.rdpBitmapToRGBA === 'function'

// Resolves once both decoders exist. A failed load rejects, and the next call
// tries again.
export function loadRleDecoder() {
  if (ready()) return Promise.resolve()
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const script = document.createElement('script')
      script.src = RLE_SRC
      script.async = true
      script.onload = () => (ready() ? resolve() : reject(new Error(`${RLE_SRC} did not define the RDP decoders`)))
      script.onerror = () => reject(new Error(`Failed to load ${RLE_SRC}`))
      document.head.appendChild(script)
    }).catch((err) => {
      loading = null
      throw err
    })
  }
  return loading
}

// Paints one recorded bitmap at its place on the screen. Returns false when it
// could not be decoded.
export function drawBitmap(ctx, bitmap) {
  const { x, y, width, height, bits_per_pixel: bpp = 16, compressed, data } = bitmap || {}
  if (!(width > 0 && height > 0)) return true
  const bytes = typeof data === 'string' ? decodeBase64(data) : data
  if (!bytes) return false
  try {
    const pixels = compressed ? window.rdpRleDecompress(bytes, width, height, bpp) : bytes
    const rgba = window.rdpBitmapToRGBA(pixels, width, height, bpp)
    ctx.putImageData(new ImageData(rgba, width, height), x, y)
    return true
  } catch (err) {
    console.error('RDP bitmap could not be decoded', err)
    return false
  }
}
