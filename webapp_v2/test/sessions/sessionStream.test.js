import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  readSessionStream,
  StreamDropped,
  StreamRefused,
} from '@/features/SessionDetails/viewers/recording/sessionStream'

// A fetch answer whose body yields the chunks as they would arrive on the wire.
function answer(chunks, status = 200) {
  const encoder = new TextEncoder()
  const body = new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    },
  })
  return { ok: status >= 200 && status < 300, status, body }
}

afterEach(() => vi.unstubAllGlobals())

describe('readSessionStream', () => {
  it('reads events split across chunks and resolves on session_end', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      answer([
        ': connected\n\n',
        'event: event\ndata: {"type":"o","payload":"YQ=="',
        '}\n\n: keepalive\n\nevent: event\ndata: {"type":"e","payload":"Yg=="}\n\nevent: session_end\ndata: {}\n\n',
      ]),
    )
    vi.stubGlobal('fetch', fetchMock)
    const onOpen = vi.fn()
    const messages = []
    await readSessionStream('/api/sessions/s1/stream', {
      token: 't',
      onOpen,
      onMessage: (m) => messages.push(JSON.parse(m.data).type),
    })
    expect(onOpen).toHaveBeenCalledOnce()
    expect(messages).toEqual(['o', 'e'])
    expect(fetchMock.mock.calls[0][1].headers).toEqual({ Authorization: 'Bearer t', Accept: 'text/event-stream' })
  })

  it('reports a stream that closes before session_end as dropped', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(answer([': connected\n\n'])))
    await expect(readSessionStream('/x', { token: 't', onOpen: () => {}, onMessage: () => {} })).rejects.toBeInstanceOf(
      StreamDropped,
    )
  })

  it('reports an HTTP error as refused, before it opens', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(answer([], 403)))
    const onOpen = vi.fn()
    await expect(readSessionStream('/x', { token: 't', onOpen, onMessage: () => {} })).rejects.toBeInstanceOf(
      StreamRefused,
    )
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('hands over the headers, on an error too, so a renewed token is kept (EVL-389)', async () => {
    const headers = new Headers({ 'X-New-Access-Token': 'token-2' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ...answer([], 401), headers }))
    const onResponse = vi.fn()
    await expect(
      readSessionStream('/x', { token: 't', onResponse, onOpen: () => {}, onMessage: () => {} }),
    ).rejects.toBeInstanceOf(StreamRefused)
    expect(onResponse).toHaveBeenCalledWith(headers)
  })
})
