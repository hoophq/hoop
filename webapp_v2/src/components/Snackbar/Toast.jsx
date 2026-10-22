import { useState } from 'react'
import { toast } from 'sonner'
import { AlertCircle, CheckCircle, ChevronDown, ChevronUp, Clock, Hourglass, X } from 'lucide-react'
import Button from '@/components/Button'
import classes from './Toast.module.css'

const ICON_FOR_TYPE = {
  success: { Icon: CheckCircle, className: classes.iconSuccess },
  error: { Icon: AlertCircle, className: classes.iconError },
  info: { Icon: Clock, className: classes.iconInfo },
  approval: { Icon: Hourglass, className: classes.iconApproval },
}

function capitalize(value) {
  if (value == null) return ''
  const str = String(value)
  return str.charAt(0).toUpperCase() + str.slice(1)
}

function renderDetails(details) {
  if (!details || typeof details !== 'object') return null
  const entries = Object.entries(details)
  if (entries.length === 0) return null
  return (
    <div className={classes.detailsCode}>
      {entries.map(([key, value]) => (
        <div key={key} className={classes.detailsLine}>
          <span className={classes.detailsKey}>{`"${capitalize(key)}"`}</span>
          <span className={classes.detailsSep}>: </span>
          <span className={classes.detailsValue}>{`"${capitalize(value)}"`}</span>
          <span className={classes.detailsSep}>,</span>
        </div>
      ))}
    </div>
  )
}

/**
 * Custom toast renderer that mirrors the legacy CLJS toast component:
 * white card with a tinted lucide icon, title, optional description,
 * and an expandable details panel for errors.
 *
 * Always rendered through sonner's `toast.custom`. Use `showSnackbar`
 * from `@/utils/snackbar` rather than instantiating this directly.
 */
export default function Toast({ id, type, title, description, details, actions }) {
  const [expanded, setExpanded] = useState(false)
  const meta = ICON_FOR_TYPE[type]
  const hasDetails = type === 'error' && details != null
  const Icon = meta?.Icon
  const approval = type === 'approval'

  return (
    <div className={approval ? `${classes.root} ${classes.approval}` : classes.root}>
      <div className={classes.header}>
        <div className={classes.body}>
          {Icon && (
            <span className={`${classes.iconWrapper} ${meta.className}`}>
              <Icon size={approval ? 18 : 20} />
            </span>
          )}
          <div className={classes.content}>
            <p className={approval ? `${classes.title} ${classes.titleStrong}` : classes.title}>{title}</p>
            {description &&
              (typeof description === 'string' ? (
                <p className={classes.description}>{description}</p>
              ) : (
                <div className={classes.description}>{description}</div>
              ))}
            {actions?.length > 0 && (
              <div className={classes.actions}>
                {actions.map((action, i) => (
                  <Button
                    key={action.label}
                    size="compact-sm"
                    variant={i === 0 ? 'filled' : 'default'}
                    onClick={() => {
                      toast.dismiss(id)
                      action.onClick()
                    }}
                  >
                    {action.label}
                  </Button>
                ))}
              </div>
            )}
            {hasDetails && (
              <div
                className={classes.detailsToggle}
                onClick={() => setExpanded((v) => !v)}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    setExpanded((v) => !v)
                  }
                }}
              >
                <span className={classes.detailsToggleLabel}>
                  {expanded ? 'Hide details' : 'View details'}
                </span>
                {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
              </div>
            )}
          </div>
        </div>
        <button
          type="button"
          className={classes.dismiss}
          onClick={() => toast.dismiss(id)}
          aria-label="Dismiss"
        >
          <X size={16} />
        </button>
      </div>
      {hasDetails && expanded && (
        <div className={classes.detailsPanel}>{renderDetails(details)}</div>
      )}
    </div>
  )
}
