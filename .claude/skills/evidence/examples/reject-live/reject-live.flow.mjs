// ENG-611: an approver rejects a held statement with a reason; the developer's
// psql, held on the sidecar, prints what it receives. The panel at the bottom
// mirrors the real psql output file, live. record-side.sh sets both variables.
const sid = process.env.REVIEW_SESSION
const out = process.env.PSQL_OUT_URL

export default async function flow(ui) {
  await ui.go(`/approvals/${sid}`, { record: true, wait: 6000 })
  await ui.js(`(() => {
    const b = document.createElement('div'); b.id = '__psql'
    b.style.cssText = 'position:fixed;left:330px;right:24px;bottom:16px;z-index:2147483647;background:#0b0f19;' +
      'color:#e5e7eb;font:500 15px/1.5 ui-monospace,Menlo,monospace;padding:12px 16px;border-radius:10px;' +
      'border:1px solid #374151;pointer-events:none;white-space:pre-wrap;min-height:88px'
    document.body.appendChild(b)
    const tick = async () => {
      try { const t = await (await fetch(${JSON.stringify(out)} + '?t=' + Date.now())).text()
        b.textContent = 'test overlay · the developer\\'s psql, live\\n' + t.trimEnd() } catch {}
    }
    tick(); setInterval(tick, 300); return true
  })()`)
  await ui.sleep(3000)
  const lastReject = `[...document.querySelectorAll('button')].filter(b => b.textContent.trim() === 'Reject').pop()`
  await ui.click(lastReject, 'Reject')
  await ui.sleep(2000)
  await ui.js(`(() => { document.querySelector('textarea').focus(); return true })()`)
  const text = 'use the replica'
  for (let i = 1; i <= text.length; i++) {
    await ui.js(`(() => { const el = document.querySelector('textarea')
      const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set
      set.call(el, ${JSON.stringify(text)}.slice(0, ${i})); el.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
    await ui.sleep(110)
  }
  await ui.sleep(1500)
  await ui.click(lastReject, 'confirm Reject')
  // The sidecar asks the plane every 5 s; the denial reaches psql on its next ask.
  await ui.sleep(10000)
}
