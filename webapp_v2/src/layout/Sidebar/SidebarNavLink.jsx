import { NavLink } from '@mantine/core';
import classes from './Sidebar.module.css';

/**
 * NavLink styled for the dark sidebar shell.
 * All visual decisions live in Sidebar.module.css — never pass styles={} on instances.
 *
 * Props:
 *   danger      — destructive action color (e.g. Log out)
 *   blocked     — dimmed appearance for locked paid features
 *   profileItem — square corners for items nested in the profile disclosure
 */
export function SidebarNavLink({
  danger = false,
  blocked = false,
  profileItem = false,
  classNames: extra,
  ...props
}) {
  const rootClass = [
    classes.navLink,
    danger      && classes.navLinkDanger,
    blocked     && classes.navLinkBlocked,
    profileItem && classes.profileItem,
  ].filter(Boolean).join(' ');

  return (
    <NavLink
      classNames={{
        root:    rootClass,
        label:   classes.navLinkLabel,
        section: classes.navLinkSection,
        chevron: classes.navLinkChevron,
        ...extra,
      }}
      {...props}
    />
  );
}
