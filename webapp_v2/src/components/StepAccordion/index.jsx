import { Accordion, Avatar, Box, Flex, Text } from '@mantine/core'
import { Check } from 'lucide-react'
import classes from './StepAccordion.module.css'

function StepTrigger({ icon, title, subtitle, done }) {
  const StepIcon = icon
  return (
    <Flex align="center" gap="md" className={classes.trigger}>
      <Avatar size={40} variant="soft" color="gray" radius="md">
        <StepIcon size={18} />
      </Avatar>
      <Box className={classes.triggerBody}>
        <Text fw={700} fz={24} c="dark">{title}</Text>
        <Text size="md" c="dimmed">{subtitle}</Text>
      </Box>
      {done && (
        <Avatar size="sm" color="green" variant="light" radius="xl" mr="xs">
          <Check size={12} />
        </Avatar>
      )}
    </Flex>
  )
}

/**
 * Styled multi-step accordion matching the CLJS accordion/root component.
 *
 * items: [{ value, icon, title, subtitle, done?, disabled?, content }]
 * value: currently open item value (controlled)
 * onChange: (value) => void
 */
function StepAccordion({ items, value, onChange }) {
  return (
    <Accordion
      value={value}
      onChange={onChange}
      variant="separated"
      classNames={{
        item: classes.item,
        control: classes.control,
        content: classes.content,
      }}
    >
      {items.map((item) => (
        <Accordion.Item key={item.value} value={item.value}>
          <Accordion.Control disabled={item.disabled}>
            <StepTrigger
              icon={item.icon}
              title={item.title}
              subtitle={item.subtitle}
              done={item.done}
            />
          </Accordion.Control>
          <Accordion.Panel>
            {item.content}
          </Accordion.Panel>
        </Accordion.Item>
      ))}
    </Accordion>
  )
}

export default StepAccordion
