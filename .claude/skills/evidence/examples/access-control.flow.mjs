// ENG-596: the Access Control page with 2,000 roles in group "dba".
// Same start and end state on both sides; the middle differs only where the UI
// does (main scrolls a long list, the branch pages it).
//
//   node .claude/skills/evidence/rec.mjs http://127.0.0.1:8109 token-main .claude/skills/evidence/examples/access-control.flow.mjs main out
//   node .claude/skills/evidence/rec.mjs http://127.0.0.1:8159 token-branch .claude/skills/evidence/examples/access-control.flow.mjs branch out

// The number the page hides: requests that resolve every role up front.
export const counters = [
  { label: 'GET /connections?connection_ids= requests', match: 'connection_ids=' },
]

export default async function flow(ui, { side }) {
  const row = (name) => `[...document.querySelectorAll('p')].find(e => e.textContent.trim() === ${JSON.stringify(name)})?.closest('div')?.parentElement`
  const toggle = (name) => `(${row(name)})?.querySelector('button')`
  const pageButton = (rootIndex, label) =>
    `[...(document.querySelectorAll('.mantine-Pagination-root')[${rootIndex}]?.querySelectorAll('button') ?? [])].find(b => b.textContent.trim() === ${JSON.stringify(label)})`

  await ui.go('/features/access-control', { record: true, wait: 4500 })

  // A group with 2,000 roles.
  await ui.js(`(${row('dba')}).scrollIntoView({ block: 'start' }); window.scrollBy(0, -120)`)
  await ui.sleep(900)
  await ui.click(toggle('dba'), 'expand dba')
  await ui.sleep(3000)
  if (side === 'branch') {
    await ui.click(pageButton(0, '2'), 'roles page 2')
    await ui.sleep(2000)
    await ui.js(`[...document.querySelectorAll('h4')].find(h => h.textContent.trim() === 'Users').scrollIntoView({ block: 'center' })`)
    await ui.sleep(1500)
    await ui.click(pageButton(1, '2'), 'users page 2')
    await ui.sleep(2500)
  } else {
    await ui.scrollBy(5000, 30)
    await ui.sleep(1500)
  }

  // Undo what was opened, so the last frame shows the answer.
  await ui.click(toggle('dba'), 'collapse dba')
  await ui.sleep(1000)
  await ui.js('window.scrollTo({ top: 0 })')
  await ui.sleep(900)

  await ui.click(ui.button('Resource Roles'), 'open the Resource Roles filter')
  await ui.sleep(900)
  await ui.type("input[placeholder='Search resource roles']", 'pg-0001')
  await ui.sleep(2500)
  await ui.click(ui.byText('pg-0001', "document.querySelector('.mantine-Popover-dropdown')"), 'select pg-0001')
  await ui.sleep(3500)
}
