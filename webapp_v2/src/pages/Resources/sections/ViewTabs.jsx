import Tabs from '@/components/Tabs'
import { TABS } from '@/pages/Resources/store'
import classes from './ViewTabs.module.css'

// The two lists as heading-sized tabs, like the CLJS page. The panels are
// rendered by the caller inside the same <Tabs>.
export default function ViewTabs() {
  return (
    <Tabs.List aria-label="View selector" className={classes.list}>
      <Tabs.Tab value={TABS.RESOURCES} className={classes.tab}>
        My Resources
      </Tabs.Tab>
      <Tabs.Tab value={TABS.ROLES} className={classes.tab}>
        My Roles
      </Tabs.Tab>
    </Tabs.List>
  )
}
