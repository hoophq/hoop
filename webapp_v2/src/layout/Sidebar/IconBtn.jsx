import { Tooltip } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import classes from './Sidebar.module.css';

export function IconBtn({ icon, label, path, active = false, onClick }) {
  const Icon = icon;
  const navigate = useNavigate();

  return (
    <Tooltip label={label} position="right" withArrow>
      <button
        aria-label={label}
        aria-current={active ? 'page' : undefined}
        className={`${classes.iconBtn} ${active ? classes.iconBtnActive : ''}`}
        onClick={() => {
          if (onClick) { onClick(); return; }
          if (path) navigate(path);
        }}
      >
        <Icon size={18} aria-hidden="true" />
        <span className={classes.srOnly}>{label}</span>
      </button>
    </Tooltip>
  );
}
