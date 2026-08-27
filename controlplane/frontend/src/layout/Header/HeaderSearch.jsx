import { Input } from '@mantine/core'
import { useOs } from '@mantine/hooks'
import { Search } from 'lucide-react'
import { openCommandPalette } from '@/features/CommandPalette/spotlight'
import classes from './Header.module.css'

// A button, not a text field. A real <input> would take focus, swallow the
// first keystrokes, and then lose them the moment the palette steals focus.
export function HeaderSearch() {
  const os = useOs()
  const isMac = os === 'macos' || os === 'ios'

  return (
    <Input
      component="button"
      type="button"
      pointer
      size="sm"
      aria-label="Search"
      aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}
      onClick={openCommandPalette}
      leftSection={<Search size={16} aria-hidden="true" />}
      classNames={{ input: classes.searchInput }}
    >
      <Input.Placeholder>{`Search (${isMac ? 'cmd' : 'ctrl'} + k)`}</Input.Placeholder>
    </Input>
  )
}
