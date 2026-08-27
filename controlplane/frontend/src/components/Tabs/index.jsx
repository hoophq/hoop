import { Tabs as MantineTabs } from '@mantine/core'
import classes from './Tabs.module.css'

/**
 * Tabs wrapper. Pure passthrough to Mantine, plus a one-line override to make
 * the hover background visible (see Tabs.module.css for the rationale).
 *
 * Usage:
 *   <Tabs value={tab} onChange={setTab}>
 *     <Tabs.List>
 *       <Tabs.Tab value="one">One</Tabs.Tab>
 *     </Tabs.List>
 *     <Tabs.Panel value="one" pt="md">...</Tabs.Panel>
 *   </Tabs>
 */
function Tabs({ classNames = {}, ...props }) {
  const merged = { root: classes.root, ...classNames }
  return <MantineTabs classNames={merged} {...props} />
}

Tabs.List = MantineTabs.List
Tabs.Tab = MantineTabs.Tab
Tabs.Panel = MantineTabs.Panel

export default Tabs
