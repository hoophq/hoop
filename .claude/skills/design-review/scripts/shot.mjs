// Photograph one web app issue before and after its fix, on a running hoop.
//
//   node shot.mjs <baseURL> <path> <target> <fix> <outDir> [options]
//
// <target>  a JS expression that finds the element with the issue, e.g.
//           [...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Select All')
// <fix>     JS run in the page with `el` set to the target. It only changes the
//           page, never the code: el.textContent = 'Select all'
// Options:
//   --token <file>  the hoop session token (a file), set before the page loads
//   --steps <js>    JS awaited after the page loads and before the photo, to
//                   reach the state (open a modal, a tab); `sleep(ms)` exists
//   --pad <px>      space kept around the element (default 80)
//   --name <name>   file prefix (default "issue")
//
// Writes <outDir>/<name>-before.png (red box) and <name>-after.png (green box).
// Join them with pair.sh. Needs Node >= 20.10 and Chrome
// (CHROME_PATH, else Google Chrome on macOS, else /opt/pw-browsers/chromium).
import { spawn, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Node 20 has WebSocket only behind a flag (global from Node 22): run again with it.
if (!globalThis.WebSocket) {
  if (process.execArgv.includes('--experimental-websocket')) throw new Error('this Node has no WebSocket: use Node >= 20.10')
  const r = spawnSync(process.execPath, ['--experimental-websocket', ...process.argv.slice(1)], { stdio: 'inherit' })
  process.exit(r.status ?? 1)
}

const argv = process.argv.slice(2)
const opt = {}
const pos = []
for (let i = 0; i < argv.length; i++) {
  if (argv[i].startsWith('--')) opt[argv[i].slice(2)] = argv[++i]
  else pos.push(argv[i])
}
const [base, path, target, fix, outDir] = pos
if (!outDir) {
  console.error('usage: node shot.mjs <baseURL> <path> <target> <fix> <outDir> [--token f] [--steps js] [--pad px] [--name n]')
  process.exit(1)
}
const pad = Number(opt.pad ?? 80)
const name = opt.name ?? 'issue'
mkdirSync(outDir, { recursive: true })

const chrome = process.env.CHROME_PATH || process.env.CHROME ||
  ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/opt/pw-browsers/chromium'].find(existsSync)
if (!chrome) throw new Error('no Chrome: set CHROME_PATH')
const port = 9300 + Math.floor(Math.random() * 500)
const args = ['--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${mkdtempSync(join(tmpdir(), 'shot-'))}`,
  '--window-size=1440,900', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars']
if (process.platform === 'linux') args.push('--disable-dev-shm-usage', '--no-sandbox')
const proc = spawn(chrome, [...args, 'about:blank'], { stdio: 'ignore' })
process.on('exit', () => proc.kill())
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let wsUrl
for (let i = 0; i < 50 && !wsUrl; i++) {
  try { wsUrl = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find((t) => t.type === 'page')?.webSocketDebuggerUrl } catch {}
  if (!wsUrl) await sleep(200)
}
if (!wsUrl) throw new Error(`chrome did not start on port ${port}`)
const ws = new WebSocket(wsUrl)
await new Promise((r) => ws.addEventListener('open', r))
let id = 0
const pending = new Map()
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id) }
})
const send = (method, params = {}) => new Promise((ok, fail) => {
  const n = ++id
  const timer = setTimeout(() => { pending.delete(n); fail(new Error(`${method}: no answer in 30 s`)) }, 30000)
  timer.unref()
  pending.set(n, (m) => { clearTimeout(timer); m.error ? fail(new Error(`${method}: ${m.error.message}`)) : ok(m.result) })
  ws.send(JSON.stringify({ id: n, method, params }))
})
const js = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
  if (r.exceptionDetails) throw new Error(`page: ${r.exceptionDetails.exception?.description ?? r.exceptionDetails.text}`)
  return r.result.value
}

// A box drawn in the page around the element, with a word on it: a red
// "BEFORE", a green "AFTER". The word keeps the meaning without the color.
const box = (color, word) => js(`(() => {
  const el = window.__dr; el.scrollIntoView({ block: 'center' })
  const r = el.getBoundingClientRect()
  let d = document.getElementById('__dr_box'), t = document.getElementById('__dr_tag')
  if (!d) {
    d = document.createElement('div'); d.id = '__dr_box'; document.body.appendChild(d)
    t = document.createElement('div'); t.id = '__dr_tag'; document.body.appendChild(t)
  }
  d.style.cssText = 'position:fixed;z-index:2147483647;pointer-events:none;border:3px solid ${color};' +
    'left:' + (r.left - 6) + 'px;top:' + (r.top - 6) + 'px;width:' + (r.width + 12) + 'px;height:' + (r.height + 12) + 'px'
  t.textContent = ${JSON.stringify(word)}
  t.style.cssText = 'position:fixed;z-index:2147483647;pointer-events:none;background:${color};color:#fff;' +
    'font:700 11px/1 ui-monospace,Menlo,monospace;letter-spacing:.06em;padding:4px 6px;' +
    'left:' + (r.left - 6) + 'px;top:' + (r.top - 28) + 'px'
  // The tag sits above the box: the photo keeps it.
  return { x: r.left, y: r.top - 22, w: r.width, h: r.height + 22, vw: innerWidth, vh: innerHeight }
})()`)
const shot = async (rect, file) => {
  const x = Math.max(0, rect.x - pad), y = Math.max(0, rect.y - pad)
  const clip = { x, y, width: Math.min(rect.vw, rect.x + rect.w + pad) - x, height: Math.min(rect.vh, rect.y + rect.h + pad) - y, scale: 1 }
  const { data } = await send('Page.captureScreenshot', { format: 'png', clip })
  writeFileSync(file, Buffer.from(data, 'base64'))
  console.log(file)
}

try {
  await send('Page.enable')
  await send('Runtime.enable')
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false })
  if (opt.token) {
    // hoop keeps the session in localStorage: set it on the app's origin first.
    await send('Page.navigate', { url: base + '/login' })
    await sleep(2000)
    await js(`localStorage.setItem('jwt-token', ${JSON.stringify(readFileSync(opt.token, 'utf8').trim())}); true`)
  }
  await send('Page.navigate', { url: base + path })
  await sleep(4000)
  if (opt.steps) await js(`(async () => { const sleep = (ms) => new Promise((r) => setTimeout(r, ms)); ${opt.steps} })()`)
  const found = await js(`(() => { window.__dr = (${target}); return !!window.__dr })()`)
  if (!found) throw new Error(`target not found on ${path}: ${target}`)
  await sleep(300)
  await shot(await box('#E5484D', '✕ BEFORE'), join(outDir, `${name}-before.png`))
  await js(`(async () => { const el = window.__dr; ${fix} })()`)
  await sleep(400)
  await shot(await box('#30A46C', '✓ AFTER'), join(outDir, `${name}-after.png`))
} finally {
  ws.close()
  proc.kill()
}
