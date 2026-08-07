import { NavLink } from '@mantine/core';
import classes from './Sidebar.module.css';

/**
 * NavLink styled for the sidebar shell.
 * All visual decisions live in Sidebar.module.css — never pass styles={} on instances.
 */
export function SidebarNavLink({ classNames: extra, ...props }) {
  return (
    <NavLink
      classNames={{
        root:     classes.navLink,
        label:    classes.navLinkLabel,
        section:  classes.navLinkSection,
        chevron:  classes.navLinkChevron,
        children: classes.navLinkChildren,
        ...extra,
      }}
      {...props}
    />
  );
}
