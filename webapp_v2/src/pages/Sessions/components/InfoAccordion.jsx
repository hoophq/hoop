import Accordion from '@/components/Accordion'
import classes from './InfoAccordion.module.css'

/**
 * The collapsible card chrome shared by the session-details info blocks
 * (AI Session Analyzer, Guardrails).
 *
 * It exists so those two cannot drift apart: v1 gives them the identical root
 * class and renders them adjacent, so any styling decision has to be made once,
 * in one place. See InfoAccordion.module.css for why the border needs a CSS
 * Module rather than a style prop.
 */
function InfoAccordion({ children, ...props }) {
  return (
    <Accordion radius="md" chevronSize={16} classNames={{ item: classes.item }} {...props}>
      {children}
    </Accordion>
  )
}

// Re-exported so call sites never need a second import, same as the base wrapper.
InfoAccordion.Item = Accordion.Item
InfoAccordion.Control = Accordion.Control
InfoAccordion.Panel = Accordion.Panel

export default InfoAccordion
