import { useRef, useState } from 'react'
import { Box, Group, Text } from '@mantine/core'
import Tooltip from '@/components/Tooltip'
import classes from './InfoRow.module.css'

// One detail of the session (Figma "Information Module"): an icon and a
// 160px label, then the value. A label cut by the width shows in full in a
// tooltip; a long value wraps, so no part of it is lost.
export default function InfoRow({ icon: Icon, label, children }) {
  const labelRef = useRef(null)
  const [cut, setCut] = useState(false)
  const measure = () => {
    const el = labelRef.current
    setCut(Boolean(el && el.scrollWidth > el.clientWidth))
  }
  return (
    <Group gap="smAlt" wrap="nowrap" mih={32}>
      <Group gap="smAlt" w={160} wrap="nowrap" flex="none">
        {Icon && <Icon size={20} color="var(--mantine-color-dimmed)" className={classes.icon} aria-hidden />}
        <Tooltip label={label} disabled={!cut}>
          <Text ref={labelRef} size="sm" c="dimmed" truncate onMouseEnter={measure} onFocus={measure}>
            {`${label}:`}
          </Text>
        </Tooltip>
      </Group>
      <Box flex={1} miw={0} className={classes.value}>
        {children}
      </Box>
    </Group>
  )
}
