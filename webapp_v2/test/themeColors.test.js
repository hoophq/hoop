import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import postcss from 'postcss'
import { describe, expect, it } from 'vitest'

// A page takes its colors from the theme: it picks a component, a variant or a
// role token, never a shade or a literal (CLJS_PATTERNS.md, ENG-994). Colors
// live in src/components/* and src/theme.js only. A one-off effect may name a
// color when a comment says `theme-exception:`: the comment before its CSS
// rule, or a JS comment on or up to two lines above the line. The comment must
// say why: a bare marker, or the words in a string, do not count.

const SRC = join(import.meta.dirname, '..', 'src')
const OWNERS = [/^components\//, /^theme\.js$/]
const MARK = /theme-exception:[ \t]*[^\s*]/
// The same mark, in a JS comment: //, /* or a block comment's * line.
const JS_MARK = new RegExp(`(?:\\/\\/|\\/\\*|^\\s*\\*)[^\\n]*?${MARK.source}`, 'm')

const PALETTES = 'gray|dark|red|pink|grape|violet|indigo|blue|cyan|teal|green|lime|yellow|orange|amber|sky|slate'
// A palette shade as a CSS variable, the palette written out or built in a
// template: --mantine-color-gray-6, `--mantine-color-${name}-6`.
const SHADE_VAR = /--mantine-color-(?:[a-z]+|\$\{[^}]*\})-\d\b/
// Inside any string or template: 'gray.6', "1px solid gray.3", `gray.${n}`.
const SHADE_STRING = new RegExp(`["'\`][^"'\`]*\\b(?:${PALETTES})\\.(?:\\d|\\$\\{)`)
// A shade added to a palette held in a variable: `${name}.6`, name + '.6'.
const BUILT_SHADE = /\$\{[^}]*\}\.\d\b|\+\s*["'`][.-]\d\b/
// theme.colors.gray[6], colors[name][6] (destructured too)
const THEME_INDEX = /\bcolors(?:\.[a-z]+|\[[^\]]+\])\??\.?\[\d\]/
// A literal color in CSS: hex, or a color function.
const HEX = '#[0-9a-fA-F]{3,8}\\b'
const COLOR_FN = '\\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\\('
const LITERAL = new RegExp(`${HEX}|${COLOR_FN}`)
// The same inside a JS string.
const JS_LITERAL = new RegExp(`["'\`][^"'\`]*(?:${HEX}|${COLOR_FN})`)
// CSS color keywords a value may name (no theme token behind them).
const NAMED = new RegExp(
  `(?<![-\\w])(?:${[
    'white', 'black', 'red', 'green', 'blue', 'yellow', 'orange', 'purple', 'pink', 'gray', 'grey', 'silver',
    'maroon', 'navy', 'teal', 'olive', 'lime', 'aqua', 'fuchsia', 'cyan', 'magenta', 'indigo', 'violet',
    'brown', 'gold', 'tan', 'beige', 'ivory', 'coral', 'salmon', 'crimson', 'khaki', 'lavender', 'plum',
    'orchid', 'turquoise', 'tomato', 'chocolate', 'wheat', 'snow', 'linen', 'azure',
  ].join('|')})(?![-\\w])`,
)
const COLOR_PROPS = /color|background|border|outline|shadow|fill|stroke|caret|accent|^--/
// An inline style that names a CSS color keyword: style={{ color: 'red' }}. A
// config object's `color: 'red'` is a Mantine palette name, which is allowed.
const JS_NAMED = new RegExp(`style=\\{\\{[^}]*\\b(?:color|background\\w*|border\\w*|fill|stroke)\\s*:\\s*["'\`]${NAMED.source}`)
// An inline style object or a template that spans lines, where the line scan
// cannot see the key and its color together: style={{\n color: 'red' }} or a
// CSS template with `background: #f00`.
const BLOCKS = /style=\{\{[\s\S]*?\}\}|`[^`]*`/g
const BLOCK_COLOR = new RegExp(
  `\\b(?:color|background[\\w-]*|border[\\w-]*|outline[\\w-]*|fill|stroke)\\s*:\\s*["'\`]?[^;{}"'\`\\n]*?(?:${NAMED.source}|${HEX}|${COLOR_FN})`,
  'g',
)

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? files(path) : [path]
  })
}

const owned = (path) => OWNERS.some((re) => re.test(relative(SRC, path)))

// A comment right before the declaration, or right before its own rule. A
// mark on an at-rule (@media) does not reach the rules inside it.
function marked(decl) {
  return [decl, decl.parent].some((n) => n?.type !== 'atrule' && n?.prev()?.type === 'comment' && MARK.test(n.prev().text))
}

function cssViolations(path) {
  const found = []
  postcss.parse(readFileSync(path, 'utf8'), { from: path }).walkDecls((decl) => {
    const value = decl.value
    const named = COLOR_PROPS.test(decl.prop) && NAMED.test(value)
    if (!(SHADE_VAR.test(value) || LITERAL.test(value) || named) || marked(decl)) return
    found.push(`${relative(SRC, path)}:${decl.source.start.line}: ${decl.prop}: ${value}`)
  })
  return found
}

function jsViolations(path) {
  const text = readFileSync(path, 'utf8')
  const lines = text.split('\n')
  const report = (i) => {
    const near = lines.slice(Math.max(0, i - 2), i + 1).join('\n')
    return JS_MARK.test(near) ? [] : [`${relative(SRC, path)}:${i + 1}: ${lines[i].trim()}`]
  }
  const hits = new Set()
  lines.forEach((line, i) => {
    // A line comment, not the // of a URL inside a string.
    const code = line.replace(/(^|[^:"'`])\/\/.*$/, '$1')
    if (SHADE_VAR.test(code) || SHADE_STRING.test(code) || BUILT_SHADE.test(code) || THEME_INDEX.test(code) || JS_LITERAL.test(code) || JS_NAMED.test(code)) hits.add(i)
  })
  for (const block of text.matchAll(BLOCKS)) {
    if (!block[0].includes('\n')) continue
    for (const color of block[0].matchAll(BLOCK_COLOR)) {
      const at = block.index + color.index + color[0].length - 1
      hits.add(text.slice(0, at).split('\n').length - 1)
    }
  }
  return [...hits].sort((a, b) => a - b).flatMap(report)
}

describe('pages take their colors from the theme', () => {
  const all = files(SRC).filter((path) => !owned(path))

  it('no CSS Module names a shade or a literal color', () => {
    expect(all.filter((path) => path.endsWith('.css')).flatMap(cssViolations)).toEqual([])
  })

  it('no JS or JSX picks a shade or a literal color', () => {
    expect(all.filter((path) => /\.(m?jsx?|tsx?)$/.test(path)).flatMap(jsViolations)).toEqual([])
  })
})
