import { Tooltip } from '@mantine/core';
import { useLocation, useNavigate } from 'react-router-dom';
import { isActive } from './helpers';
import classes from './Sidebar.module.css';

export function IconBtn({ icon, label, path, action, onClick }) {
  const Icon = icon;
  const location = useLocation();
  const navigate = useNavigate();
  const active = path ? isActive(path, location.pathname) : false;

  return (
    <Tooltip label={label} position="right" withArrow>
      <button
        aria-label={label}
        aria-current={active ? 'page' : undefined}
        className={`${classes.iconBtn} ${active ? classes.iconBtnActive : ''}`}
        onClick={() => {
          if (onClick) { onClick(); return; }
          if (action) { action(); return; }
          if (path) navigate(path);
        }}
      >
        <Icon size={24} aria-hidden="true" />
        <span className={classes.srOnly}>{label}</span>
      </button>
    </Tooltip>
  );
}
