import { Accordion as MantineAccordion } from '@mantine/core'

/**
 * Expandable accordion. Re-exports sub-components so call sites never import from Mantine.
 *
 * Usage:
 *   <Accordion>
 *     <Accordion.Item value="details">
 *       <Accordion.Control>Show details</Accordion.Control>
 *       <Accordion.Panel>Content here</Accordion.Panel>
 *     </Accordion.Item>
 *   </Accordion>
 */
function Accordion({ children, ...props }) {
  return (
    <MantineAccordion variant="contained" radius="sm" {...props}>
      {children}
    </MantineAccordion>
  )
}

Accordion.Item = MantineAccordion.Item
Accordion.Control = MantineAccordion.Control
Accordion.Panel = MantineAccordion.Panel

export default Accordion
