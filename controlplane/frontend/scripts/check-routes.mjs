// Fails the build when the app navigates to a path Router.jsx does not claim.
//
// The control plane has no catch-all: an unclaimed path renders the 404 page. That is
// the point — the gateway had routes this app deliberately dropped, and a silent
// redirect would hide which. The cost is that any leftover `navigate('/client')` turns
// into a dead end that nothing catches at build time.
//
// Only literal paths are checked. A path built from a variable is invisible here, so
// prefer literals with params (`/reviews/${id}` is checked against `/reviews/:id`).

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const SRC = 'src'

const walk = (dir) =>
  readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })

// `*` is the 404 route. It is a destination for the router, not for the app — leaving
// it in the matcher set would make every path "claimed" and the check always pass.
const routes = [...readFileSync(join(SRC, 'Router.jsx'), 'utf8').matchAll(/path="([^"]+)"/g)]
  .map((m) => m[1])
  .filter((p) => p !== '*')

if (routes.length === 0) {
  console.error('check-routes: no routes parsed from Router.jsx — the matcher is broken, not the app')
  process.exit(1)
}

const matchers = routes.map((p) =>
  new RegExp('^' + p.replace(/:[A-Za-z0-9_]+/g, '[^/]+').replace(/\*/g, '.*') + '$')
)
const claimed = (p) => matchers.some((m) => m.test(p))

// navigate('/x'), navigate(`/x/${y}`), to="/x", to={`/x`}, href = '/x'
const NAV = /(?:navigate\(|(?:to|href)\s*=\s*\{?\s*|window\.location\.href\s*=\s*)(['"`])(\/[^'"`]*)\1/g

const dead = []
for (const f of walk(SRC).filter((f) => /\.jsx?$/.test(f))) {
  const text = readFileSync(f, 'utf8')
  for (const m of text.matchAll(NAV)) {
    // Template placeholders stand in for a route param.
    const path = m[2].replace(/\$\{[^}]*\}/g, 'x').split('?')[0].replace(/\/$/, '') || '/'
    if (!claimed(path)) {
      dead.push({ file: relative(SRC, f), line: text.slice(0, m.index).split('\n').length, path: m[2] })
    }
  }
}

if (dead.length) {
  console.error(`\n${dead.length} navigation target(s) not claimed by Router.jsx:\n`)
  for (const d of dead) console.error(`  ${d.file}:${d.line}  →  ${d.path}`)
  console.error('\nEither add the route or send the user somewhere that exists.\n')
  process.exit(1)
}

console.log(`routes ok — ${routes.length} declared, every literal navigation target resolves`)
