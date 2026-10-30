import { useCallback, useEffect, useRef, useState } from 'react'
import Modal from '@/components/Modal'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import Header from './sections/Header'
import InfoModule from './sections/InfoModule'
import DetailsTabs from './sections/DetailsTabs'
import ActionBar from './sections/ActionBar'
import DetailsSkeleton from './sections/DetailsSkeleton'
import ErrorState from './sections/ErrorState'
import classes from './SessionDetails.module.css'

const MODAL_WIDTH = 1276
// The footer padding grows by 24px when nothing is behind it. A gap under this
// still counts as nothing, so the change cannot flip the state back.
const OVERLAP_SLACK = 24

// Whether content scrolls behind the action bar (Figma "Main Action Bar", nice
// to have): the bar then has a border and a translucent fill.
function useOverlap() {
  const ref = useRef(null)
  const [overlap, setOverlap] = useState(false)
  const measure = useCallback(() => {
    const el = ref.current
    if (el) setOverlap(el.scrollHeight - el.scrollTop - el.clientHeight > OVERLAP_SLACK)
  }, [])
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    for (const child of el.children) observer.observe(child)
    measure()
    return () => observer.disconnect()
  })
  return { ref, overlap, onScroll: measure }
}

export default function SessionDetailsModal() {
  const session = useSessionDetailsStore((s) => s.session)
  const status = useSessionDetailsStore((s) => s.status)
  const error = useSessionDetailsStore((s) => s.error)
  const close = useSessionDetailsStore((s) => s.close)
  const stacked = useSessionDetailsStore((s) => s.stacked)
  const ready = status === 'ready'
  const { ref: scrollRef, overlap, onScroll } = useOverlap()

  return (
    <Modal
      opened
      onClose={close}
      withCloseButton={false}
      closeOnEscape={!stacked}
      closeOnClickOutside={!stacked}
      padding={0}
      size={MODAL_WIDTH}
      aria-label="Session Details"
      classNames={{ inner: classes.inner, content: classes.content, body: classes.body }}
    >
      <div className={classes.header}>
        <Header session={session} ready={ready} onClose={close} />
      </div>
      <div className={classes.scroll} ref={scrollRef} onScroll={onScroll}>
        {status === 'loading' && <DetailsSkeleton />}
        {status === 'error' && <ErrorState error={error} />}
        {ready && (
          <>
            <InfoModule session={session} />
            <DetailsTabs key={session.id} session={session} />
            <div className={classes.footer} data-overlap={overlap || undefined}>
              <ActionBar session={session} />
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}
