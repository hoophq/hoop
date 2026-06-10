import { Group, Title } from '@mantine/core'
import { ChevronLeft } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import ActionIcon from '@/components/ActionIcon'

function MobileHeader({ title, backTo }) {
  const navigate = useNavigate()

  return (
    <Group gap="xs" wrap="nowrap">
      {backTo && (
        <ActionIcon
          variant="subtle"
          color="gray"
          size="lg"
          aria-label="Back"
          onClick={() => navigate(backTo)}
        >
          <ChevronLeft size={22} />
        </ActionIcon>
      )}
      <Title order={3}>{title}</Title>
    </Group>
  )
}

export default MobileHeader
