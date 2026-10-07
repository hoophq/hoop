import { NavLink } from '@mantine/core';
import classes from './Sidebar.module.css';

const CHILDREN_CLASS = {
  guide: classes.navLinkChildrenGuide,
  label: classes.navLinkChildrenLabel,
  nested: classes.navLinkChildrenNested,
};

/**
 * NavLink styled for the sidebar shell.
 * All visual decisions live in Sidebar.module.css — never pass styles={} on instances.
 */
export function SidebarNavLink({ classNames: extra, childrenVariant, ...props }) {
  return (
    <NavLink
      classNames={{
        root:     classes.navLink,
        label:    classes.navLinkLabel,
        section:  classes.navLinkSection,
        chevron:  classes.navLinkChevron,
        children: `${classes.navLinkChildren} ${CHILDREN_CLASS[childrenVariant] ?? ''}`,
        ...extra,
      }}
      {...props}
    />
  );
}
