import { Tabs as MantineTabs } from '@mantine/core'
import classes from './Tabs.module.css'

/**
 * Tabs wrapper. Passthrough to Mantine with a visible hover background, the
 * app's border color under the list, and a medium-weight active tab.
 *
 * Usage:
 *   <Tabs value={tab} onChange={setTab}>
 *     <Tabs.List>
 *       <Tabs.Tab value="one">One</Tabs.Tab>
 *     </Tabs.List>
 *     <Tabs.Panel value="one" pt="md">...</Tabs.Panel>
 *   </Tabs>
 */
function Tabs({ classNames = {}, unstyled, ...props }) {
  // Unstyled tabs (the /resources heading tabs) own their look.
  const merged = { root: classes.root, ...(unstyled ? {} : { tab: classes.tab }), ...classNames }
  return <MantineTabs classNames={merged} unstyled={unstyled} {...props} />
}

Tabs.List = MantineTabs.List
Tabs.Tab = MantineTabs.Tab
Tabs.Panel = MantineTabs.Panel

export default Tabs
