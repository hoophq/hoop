// Record one UI flow on a running hoop stack as an MP4, over Chrome DevTools Protocol.
//
//   node rec.mjs <baseURL> <tokenFile> <flow.mjs> <side> <outDir> [port]
//
// <flow.mjs> exports `default async (ui, { side }) => {...}` and may export
// `counters`: [{ label, match }] — a fixed "test overlay" counts the requests
// whose URL contains `match`, so the video shows numbers the page hides.
// <side> is a free label (main, branch) passed to the flow; the output is
// <outDir>/<side>.mp4. Needs Node >= 22 (global WebSocket), Chrome and ffmpeg.
// Chrome is CHROME_PATH (or CHROME), else Google Chrome on macOS, else the
// Chromium of a cloud sandbox, else the newest Chromium Playwright installed
// (`npx playwright install chromium`; Linux CI and containers).
// A DevTools call of the flow with no answer in 30 s stops the run with an
// error; a frame capture gives up after 3 s and the frame is skipped.
// See examples/access-control.flow.mjs.
import { spawn, execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const [base, tokenFile, flowFile, side, outDir, portArg] = process.argv.slice(2)
if (!outDir) {
  console.error('usage: node rec.mjs <baseURL> <tokenFile> <flow.mjs> <side> <outDir> [port]')
  process.exit(1)
}
const flow = await import(pathToFileURL(resolve(flowFile)).href)
const port = Number(portArg || 9361)
const token = readFileSync(tokenFile, 'utf8').trim()
const framesDir = join(outDir, `${side}-frames`)
mkdirSync(framesDir, { recursive: true })

// Where Playwright installs browsers. "0" means inside playwright-core itself
// (Playwright's registry: <playwright-core>/.local-browsers), found from the cwd.
function playwrightBrowsers() {
  const env = process.env.PLAYWRIGHT_BROWSERS_PATH
  if (env !== '0') return env || join(homedir(), '.cache', 'ms-playwright')
  try {
    const pkg = createRequire(join(process.cwd(), 'noop.js')).resolve('playwright-core/package.json')
    return join(dirname(pkg), '.local-browsers')
  } catch {
    return 'playwright-core/.local-browsers (no playwright-core under the cwd)'
  }
}

function findChrome() {
  const env = process.env.CHROME_PATH || process.env.CHROME
  if (env) return env
  const fixed = ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/opt/pw-browsers/chromium']
    .find(existsSync)
  if (fixed) return fixed
  const root = playwrightBrowsers()
  const builds = existsSync(root) ? readdirSync(root).filter((d) => /^chromium-\d+$/.test(d)) : []
  builds.sort((a, b) => Number(b.slice('chromium-'.length)) - Number(a.slice('chromium-'.length)))
  // chrome-linux, chrome-linux64 or chrome-linux-arm64, by version and arch.
  const found = builds
    .flatMap((d) => readdirSync(join(root, d)).filter((sub) => sub.startsWith('chrome-linux'))
      .map((sub) => join(root, d, sub, 'chrome')))
    .find((path) => existsSync(path))
  if (found) return found
  throw new Error(`no Chrome: set CHROME_PATH, or run "npx playwright install chromium" (looked in ${root})`)
}

const chromeArgs = [
  '--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${mkdtempSync(join(tmpdir(), 'rec-'))}`,
  '--window-size=1440,900', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars',
]
// Linux, as Playwright launches it: a container has a small /dev/shm, and
// Chrome finds no usable sandbox as root, in docker, or under AppArmor's
// user-namespace limits (Ubuntu 23.10+).
if (process.platform === 'linux') chromeArgs.push('--disable-dev-shm-usage', '--no-sandbox')
const proc = spawn(findChrome(), [...chromeArgs, 'about:blank'], { stdio: 'ignore' })
// Any exit, a setup call that times out included, must not leave the browser running.
process.on('exit', () => proc.kill())
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function target() {
  for (let i = 0; i < 50; i++) {
    try {
      const page = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find((t) => t.type === 'page')
      if (page) return page.webSocketDebuggerUrl
    } catch {}
    await sleep(200)
  }
  throw new Error(`chrome did not start on port ${port}`)
}
const ws = new WebSocket(await target())
await new Promise((r) => ws.addEventListener('open', r))
let id = 0
const pending = new Map()
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data)
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id) }
})
// A stalled page never answers a call: fail the flow instead of waiting forever.
const CALL_TIMEOUT_MS = 30000
const send = (method, params = {}) => new Promise((ok, fail) => {
  const n = ++id
  const timer = setTimeout(() => {
    pending.delete(n)
    fail(new Error(`${method}: no answer in ${CALL_TIMEOUT_MS / 1000} s, the page does not respond`))
  }, CALL_TIMEOUT_MS)
  // The open socket keeps the process up while a call waits; the timer must not.
  timer.unref()
  pending.set(n, (m) => {
    clearTimeout(timer)
    if (m.error) fail(new Error(`${method}: ${m.error.message}`))
    else ok(m.result)
  })
  ws.send(JSON.stringify({ id: n, method, params }))
})
const js = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
  if (r.exceptionDetails) throw new Error(`eval: ${r.exceptionDetails.text} ${expression.slice(0, 90)}`)
  return r.result.value
}

await send('Page.enable')
await send('Runtime.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

const counters = flow.counters ?? []
if (counters.length) {
  await send('Page.addScriptToEvaluateOnNewDocument', { source: `
    (() => {
      const specs = ${JSON.stringify(counters)}, n = specs.map(() => 0)
      const draw = () => {
        if (!document.body) return
        let b = document.getElementById('__evi')
        if (!b) {
          b = document.createElement('div'); b.id = '__evi'
          b.style.cssText = 'position:fixed;left:16px;bottom:16px;z-index:2147483647;background:#111827;color:#fff;' +
            'font:600 15px/1.35 ui-monospace,Menlo,monospace;padding:10px 14px;border-radius:8px;opacity:.94;' +
            'pointer-events:none;white-space:pre'
          document.body.appendChild(b)
        }
        b.textContent = specs.map((s, i) => 'test overlay · ' + s.label + ': ' + n[i]).join('\\n')
      }
      new PerformanceObserver((l) => {
        for (const e of l.getEntries()) specs.forEach((s, i) => { if (e.name.includes(s.match)) n[i]++ })
        draw()
      }).observe({ type: 'resource', buffered: true })
      setInterval(draw, 200)
    })()` })
}

// Frame loop: one screenshot about every 110 ms, timestamped for real-time
// playback. A capture of about:blank never answers, so the flow starts the loop
// with ui.go(..., { record: true }) once the first page is up, and every capture
// has a timeout.
const frames = []
let recording = false, loop
const withTimeout = (p, ms) => Promise.race([p, sleep(ms).then(() => { throw new Error('capture timeout') })])
const startLoop = () => {
  if (recording) return
  recording = true
  loop = (async () => {
    while (recording) {
      const t = Date.now()
      try {
        const { data } = await withTimeout(send('Page.captureScreenshot', { format: 'jpeg', quality: 82 }), 3000)
        const file = `f${String(frames.length).padStart(5, '0')}.jpg`
        writeFileSync(join(framesDir, file), Buffer.from(data, 'base64'))
        frames.push({ file, t })
      } catch {}
      await sleep(Math.max(0, 110 - (Date.now() - t)))
    }
  })()
}

// What a flow can do. Element arguments are JS expressions evaluated in the page.
const ui = {
  sleep,
  js,
  async go(path, { wait = 4000, record = false } = {}) {
    await send('Page.navigate', { url: base + path })
    if (record) startLoop()
    await sleep(wait)
  },
  // A ring where the click lands, then a real pointer sequence on the element.
  async click(expr, what = expr) {
    const shown = await js(`(() => {
      const el = (${expr}); if (!el) return false
      el.scrollIntoView({ block: 'center' })
      const r = el.getBoundingClientRect(), d = document.createElement('div')
      d.style.cssText = 'position:fixed;z-index:2147483646;pointer-events:none;border:3px solid #e5484d;' +
        'border-radius:999px;width:34px;height:34px;left:' + (r.left + r.width / 2 - 17) + 'px;top:' +
        (r.top + r.height / 2 - 17) + 'px'
      document.body.appendChild(d); setTimeout(() => d.remove(), 900); return true
    })()`)
    if (!shown) throw new Error(`nothing to click: ${what}`)
    await sleep(700)
    await js(`(() => { const el = (${expr}); const t = el.closest('button, a, [role=option], [role=button]') || el
      const init = { bubbles: true, cancelable: true, view: window, button: 0, pointerType: 'mouse', isPrimary: true }
      for (const type of ['pointerdown', 'mousedown', 'pointerup', 'mouseup', 'click'])
        t.dispatchEvent(type.startsWith('pointer') ? new PointerEvent(type, init) : new MouseEvent(type, init))
    })()`)
    console.log(`click ${what}`)
  },
  // Types one character at a time through React's value setter.
  async type(selector, text, delay = 140) {
    for (let i = 1; i <= text.length; i++) {
      await js(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); el.focus()
        const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
        set.call(el, ${JSON.stringify(text)}.slice(0, ${i})); el.dispatchEvent(new Event('input', { bubbles: true })) })()`)
      await sleep(delay)
    }
  },
  async scrollBy(dy, steps = 12) {
    for (let i = 0; i < steps; i++) { await js(`window.scrollBy(0, ${Math.round(dy / steps)})`); await sleep(90) }
  },
  // Common finders.
  byText: (text, scope = 'document') =>
    `[...${scope}.querySelectorAll('*')].filter(e => e.offsetParent && e.textContent.trim() === ${JSON.stringify(text)}).pop()`,
  button: (text) => `[...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(text)})`,
}

try {
  // hoop keeps the session in localStorage: set it on the app's origin first.
  await send('Page.navigate', { url: base + '/login' })
  await sleep(2500)
  await js(`localStorage.setItem('jwt-token', ${JSON.stringify(token)}); true`)
  await flow.default(ui, { side })
} finally {
  recording = false
  if (loop) await loop
  ws.close()
  proc.kill()
}
if (!frames.length) throw new Error('no frames: call ui.go(path, { record: true }) in the flow')

// Real-time MP4 from the timestamped frames; the last frame holds for 1.5 s.
const list = frames.map((f, i) => {
  const next = frames[i + 1]
  return `file '${f.file}'\nduration ${((next ? next.t : f.t + 1500) - f.t) / 1000}`
}).join('\n') + `\nfile '${frames.at(-1).file}'\n`
writeFileSync(join(framesDir, 'list.txt'), list)
execFileSync('ffmpeg', ['-loglevel', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', join(framesDir, 'list.txt'),
  '-vf', 'fps=15,format=yuv420p', '-c:v', 'libx264', '-crf', '22', join(outDir, `${side}.mp4`)])
console.log(`${frames.length} frames -> ${join(outDir, `${side}.mp4`)}`)
