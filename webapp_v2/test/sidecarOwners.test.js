import { beforeEach, describe, expect, it, vi } from 'vitest'

const sidecarsService = { getOwners: vi.fn(), putOwners: vi.fn() }

vi.mock('@/services/sidecars', () => ({ sidecarsService }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))

const { useSidecarStore } = await import('@/stores/useSidecarStore')
const { MAX_OWNERS, normalizeOwners, ownersFieldError } = await import('@/pages/Sidecars/owners')

const answer = (...owners) => ({ data: { owners } })
const ana = { email: 'ana@example.com', matched: true }
const bo = { email: 'bo@example.com', matched: false }

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  useSidecarStore.getState().reset()
})

describe('owners form helpers', () => {
  it('trims, lowercases and drops blanks and repeats', () => {
    expect(normalizeOwners([' Ana@Example.com ', 'ana@example.com', '', 'bo@example.com'])).toEqual([
      'ana@example.com',
      'bo@example.com',
    ])
  })

  it('accepts emails, and an empty list', () => {
    expect(ownersFieldError([])).toBeNull()
    expect(ownersFieldError(['ana@example.com', 'bo@example.com'])).toBeNull()
  })

  it('names each entry that is not an email', () => {
    expect(ownersFieldError(['ana@example.com', 'bo'])).toContain('"bo" is not an email')
    expect(ownersFieldError(['ana', 'bo@example'])).toContain('"ana", "bo@example" are not emails')
  })

  it('refuses more than the limit', () => {
    const many = Array.from({ length: MAX_OWNERS + 1 }, (_, i) => `user${i}@example.com`)
    expect(ownersFieldError(many.slice(0, MAX_OWNERS))).toBeNull()
    expect(ownersFieldError(many)).toContain(`at most ${MAX_OWNERS}`)
  })
})

describe('sidecar store owners', () => {
  it('loads the owners of a sidecar', async () => {
    sidecarsService.getOwners.mockResolvedValueOnce(answer(ana, bo))
    const pending = useSidecarStore.getState().fetchOwners('pg-prod')
    expect(useSidecarStore.getState()).toMatchObject({ owners: null, ownersLoading: true, ownersId: 'pg-prod' })
    await pending
    expect(sidecarsService.getOwners).toHaveBeenCalledWith('pg-prod')
    expect(useSidecarStore.getState()).toMatchObject({ owners: [ana, bo], ownersLoading: false, ownersError: null })
  })

  it('reads an answer with no list as no owners', async () => {
    sidecarsService.getOwners.mockResolvedValueOnce({ data: {} })
    await useSidecarStore.getState().fetchOwners('pg-prod')
    expect(useSidecarStore.getState().owners).toEqual([])
  })

  it('keeps the error of a failed load, and a retry clears it', async () => {
    sidecarsService.getOwners.mockRejectedValueOnce({ response: { data: { message: 'access denied' } } })
    await useSidecarStore.getState().fetchOwners('pg-prod')
    expect(useSidecarStore.getState()).toMatchObject({ owners: null, ownersLoading: false, ownersError: 'access denied' })
    sidecarsService.getOwners.mockResolvedValueOnce(answer())
    await useSidecarStore.getState().fetchOwners('pg-prod')
    expect(useSidecarStore.getState()).toMatchObject({ owners: [], ownersError: null })
  })

  it('drops a response that lands after a newer fetch', async () => {
    const slow = deferred()
    sidecarsService.getOwners.mockReturnValueOnce(slow.promise).mockResolvedValueOnce(answer(bo))
    const first = useSidecarStore.getState().fetchOwners('pg-prod')
    await useSidecarStore.getState().fetchOwners('pg-staging')
    slow.resolve(answer(ana))
    await first
    expect(useSidecarStore.getState()).toMatchObject({ owners: [bo], ownersId: 'pg-staging' })
  })

  it('saves the whole list and shows what the server answered', async () => {
    sidecarsService.getOwners.mockResolvedValueOnce(answer())
    await useSidecarStore.getState().fetchOwners('pg-prod')
    sidecarsService.putOwners.mockResolvedValueOnce(answer(ana, bo))
    const result = await useSidecarStore.getState().saveOwners('pg-prod', ['ana@example.com', 'bo@example.com'])
    expect(sidecarsService.putOwners).toHaveBeenCalledWith('pg-prod', ['ana@example.com', 'bo@example.com'])
    expect(result).toEqual({ ok: true, owners: [ana, bo] })
    expect(useSidecarStore.getState().owners).toEqual([ana, bo])
  })

  it('a save retires a load still in flight', async () => {
    const slow = deferred()
    sidecarsService.getOwners.mockReturnValueOnce(slow.promise)
    const loading = useSidecarStore.getState().fetchOwners('pg-prod')
    sidecarsService.putOwners.mockResolvedValueOnce(answer(ana))
    await useSidecarStore.getState().saveOwners('pg-prod', ['ana@example.com'])
    slow.resolve(answer())
    await loading
    expect(useSidecarStore.getState()).toMatchObject({ owners: [ana], ownersLoading: false })
  })

  it('returns the refusal and keeps the list on a failed save', async () => {
    sidecarsService.getOwners.mockResolvedValueOnce(answer(ana))
    await useSidecarStore.getState().fetchOwners('pg-prod')
    const refusal = { response: { status: 422, data: { message: 'owners: "bo" is not an email' } } }
    sidecarsService.putOwners.mockRejectedValueOnce(refusal)
    const result = await useSidecarStore.getState().saveOwners('pg-prod', ['bo'])
    expect(result).toEqual({ ok: false, error: refusal })
    expect(useSidecarStore.getState().owners).toEqual([ana])
  })

  it('does not write a save that answers after the page left', async () => {
    sidecarsService.getOwners.mockResolvedValueOnce(answer())
    await useSidecarStore.getState().fetchOwners('pg-prod')
    const slow = deferred()
    sidecarsService.putOwners.mockReturnValueOnce(slow.promise)
    const saving = useSidecarStore.getState().saveOwners('pg-prod', ['ana@example.com'])
    useSidecarStore.getState().clearSelected()
    slow.resolve(answer(ana))
    await saving
    expect(useSidecarStore.getState()).toMatchObject({ owners: null, ownersId: null })
  })
})
