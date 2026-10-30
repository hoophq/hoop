import { useCallback, useEffect, useState } from 'react'
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
// The CLJS details dim the page as every Radix dialog does (black-a6, 40%).
const OVERLAY = { backgroundOpacity: 0.4 }
// The footer padding grows by 24px when nothing is behind it. A gap under this
// still counts as nothing, so the change cannot flip the state back.
const OVERLAP_SLACK = 24

// Whether content scrolls behind the action bar (Figma "Main Action Bar", nice
// to have): the bar then has a border and a translucent fill. The modal mounts
// its body after the first render, so the box arrives through a callback ref;
// the children it observes change as the session loads.
function useOverlap() {
  const [box, setBox] = useState(null)
  const [overlap, setOverlap] = useState(false)
  const measure = useCallback(() => {
    if (box) setOverlap(box.scrollHeight - box.scrollTop - box.clientHeight > OVERLAP_SLACK)
  }, [box])
  useEffect(() => {
    if (!box) return undefined
    const sizes = new ResizeObserver(measure)
    const observe = () => {
      sizes.disconnect()
      sizes.observe(box)
      for (const child of box.children) sizes.observe(child)
    }
    const children = new MutationObserver(observe)
    children.observe(box, { childList: true })
    observe()
    return () => {
      children.disconnect()
      sizes.disconnect()
    }
  }, [box, measure])
  return { ref: setBox, overlap, onScroll: measure }
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
      overlayProps={OVERLAY}
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
