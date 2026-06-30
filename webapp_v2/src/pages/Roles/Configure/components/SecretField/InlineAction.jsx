import { RefreshCw, RotateCcw } from 'lucide-react'
import Button from '@/components/Button'

// The text+icon action docked in a SecretField input's right section.
//   replace (set state)     → reveals the editor for a new value.
//   restore (editing state) → drops the staged change, back to the stored value.
// Co-located so every SecretField state renders the same control.
const ACTIONS = {
  replace: { label: 'Replace', icon: RefreshCw, color: 'indigo' },
  restore: { label: 'Restore', icon: RotateCcw, color: 'gray' },
}

export default function InlineAction({ kind, onClick }) {
  const { label, icon: Icon, color } = ACTIONS[kind]
  return (
    <Button
      variant="subtle"
      color={color}
      size="compact-sm"
      rightSection={<Icon size={14} />}
      onClick={onClick}
    >
      {label}
    </Button>
  )
}
