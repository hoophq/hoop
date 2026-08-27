// Fails the build when the code references an asset that is not in public/.
//
// There is no asset proxy here — webapp_v2 served /images and /icons from the
// ClojureScript resources tree, and this app serves them from public/. A missing file
// is invisible at build time and shows up as a broken image in the browser, so the
// check has to be explicit.
//
// Two of the three reference styles are built at runtime and cannot be found by
// grepping for string literals, which is exactly how they got missed the first time:
//   - connection icons, from the `icon-name` field of every entry in
//     public/data/connections-metadata.json
//   - feature promotion illustrations, from the `image` prop at each call site
//
// Adding a reference of a new shape means teaching this file about it.

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const SRC = 'src'
const PUBLIC = 'public'

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

const files = walk(SRC)
const read = (f) => readFileSync(f, 'utf8')
const expected = new Map() // path -> where it came from

const add = (path, source) => {
  if (!expected.has(path)) expected.set(path, source)
}

// 1. Static literals.
for (const f of files.filter((f) => /\.(js|jsx|css)$/.test(f))) {
  for (const m of read(f).matchAll(/['"](\/(?:images|icons)\/[^'"]+)['"]/g)) {
    add(m[1], relative(SRC, f))
  }
}

// 2. Connection icons — utils/connectionIcons.js builds
//    `/icons/connections/${iconName}-default.svg` from the metadata catalog.
const metadataPath = '/data/connections-metadata.json'
add(metadataPath, 'services/connectionsMetadata.js')
const catalog = JSON.parse(readFileSync(join(PUBLIC, metadataPath), 'utf8')).connections
for (const entry of catalog) {
  if (entry['icon-name']) {
    add(`/icons/connections/${entry['icon-name']}-default.svg`, 'connections-metadata.json')
  }
}

// 3. Feature promotion illustrations — components/FeaturePromotion builds
//    `/images/illustrations/${image}` from the prop each call site passes.
for (const f of files.filter((f) => f.endsWith('.jsx'))) {
  for (const m of read(f).matchAll(/image="([^"]+\.(?:png|svg|jpg|jpeg|webp))"/g)) {
    add(`/images/illustrations/${m[1]}`, relative(SRC, f))
  }
}

const missing = [...expected].filter(([p]) => {
  try { return !statSync(join(PUBLIC, p)).isFile() } catch { return true }
})

if (missing.length) {
  console.error(`\n${missing.length} asset(s) referenced but not in ${PUBLIC}/:\n`)
  for (const [p, source] of missing) console.error(`  ${p}\n      referenced by ${source}`)
  console.error('\nCopy the file into public/ in this change.\n')
  process.exit(1)
}

console.log(`assets ok — ${expected.size} referenced, all present`)
