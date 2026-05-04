import { Link, useLocation, useNavigate } from 'react-router-dom';
import { useEffect } from 'react';
import { useUIStore } from '@/stores/useUIStore';
import { ItemBadge } from './ItemBadge';
import { SidebarNavLink } from './SidebarNavLink';
import { shouldHide, isBlocked, isActive } from './helpers';

// ─── Collapsible nav item (Integrations / Settings) ───────────────────────
// Separate component so useEffect can run on mount to clear pendingOpenSection.

export function CollapsibleNavItem({ item, isAdmin, isFreeLicense, isSelfHosted, defaultOpened, onMount }) {
  useEffect(() => {
    onMount?.();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <SidebarNavLink
      label={item.label}
      aria-label={item.label}
      leftSection={<item.icon size={24} aria-hidden="true" />}
      defaultOpened={defaultOpened}
    >
      {item.children.map((child) => (
        <NavItem key={child.path} item={child} isAdmin={isAdmin} isFreeLicense={isFreeLicense} isSelfHosted={isSelfHosted} />
      ))}
    </SidebarNavLink>
  );
}

// ─── Single expanded nav item ──────────────────────────────────────────────

export function NavItem({ item, isAdmin, isFreeLicense, isSelfHosted }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { setSidebarOpen, pendingOpenSection, clearPendingOpenSection } = useUIStore();

  if (shouldHide(item, isAdmin, isSelfHosted)) return null;

  const blocked = isBlocked(item, isFreeLicense);
  const active = item.path ? isActive(item.path, location.pathname) : false;
  const closeMobile = () => setSidebarOpen(false);

  if (item.children) {
    const shouldOpen = pendingOpenSection === item.label;
    return (
      <CollapsibleNavItem
        item={item}
        isAdmin={isAdmin}
        isFreeLicense={isFreeLicense}
        isSelfHosted={isSelfHosted}
        defaultOpened={shouldOpen}
        onMount={shouldOpen ? clearPendingOpenSection : undefined}
      />
    );
  }

  if (item.action) {
    return (
      <SidebarNavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
        rightSection={<ItemBadge badge={item.badge} blocked={blocked} shortcut={item.shortcut} />}
        onClick={() => { item.action(); closeMobile(); }}
      />
    );
  }

  if (blocked) {
    return (
      <SidebarNavLink
        blocked
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
        rightSection={<ItemBadge badge={item.badge} blocked={true} />}
        onClick={() => { navigate(item.upgradeRoute || '/upgrade-plan'); closeMobile(); }}
      />
    );
  }

  return (
    <SidebarNavLink
      component={Link}
      to={item.path}
      label={item.label}
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
      active={active}
      onClick={closeMobile}
    />
  );
}
