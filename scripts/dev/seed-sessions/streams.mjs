// Event streams in the shape the gateway records them: a JSON array of
// [seconds, type, base64] (gateway/models/session.go). Each builder returns
// that array; the bytes match what the agent, the SSH proxy and the RDP
// recorder write.

const bytesOf = (data) => (typeof data === 'string' ? Buffer.from(data, 'utf8') : Buffer.from(data))

export const event = (seconds, type, data) => [seconds, type, bytesOf(data).toString('base64')]

// An exec session: input is in its own blob, the stream holds output (o) and
// errors (e).
export function execStream(entries) {
  return entries.map(([seconds, type, data]) => event(seconds, type, data))
}

// A terminal (pty) recording: output and input interleaved, with ANSI colors,
// and one euro sign split across two events (UTF-8 decoding).
export function ptyStream() {
  const prompt = '\x1b[1;32mowner@seed\x1b[0m:\x1b[1;34m~\x1b[0m$ '
  const out = [
    [0.2, 'o', prompt],
  ]
  let t = 1
  for (const ch of 'ls\r') {
    out.push([t, 'i', ch], [t + 0.05, 'o', ch === '\r' ? '\r\n' : ch])
    t += 0.3
  }
  out.push(
    [t, 'o', '\x1b[1;34mbin\x1b[0m  \x1b[1;34metc\x1b[0m  README.md  price.txt\r\n' + prompt],
    [t + 1, 'i', 'cat price.txt\r'],
    [t + 1.1, 'o', 'cat price.txt\r\nprice: 12 '],
    [t + 1.2, 'o', [0xe2]],
    [t + 1.3, 'o', [0x82, 0xac]],
    [t + 1.4, 'o', '\r\n' + prompt],
    [t + 2.5, 'i', 'exit\r'],
    [t + 2.6, 'o', 'exit\r\nlogout\r\n'],
  )
  return out.map(([s, type, data]) => event(s, type, data))
}

// libhoop proxy/ssh framing: a type byte, the channel id (uint16, little
// endian), then the payload. Builders match the CLJS ssh_decoder tests.
const OPEN = 1
const REQUEST = 2
const DATA = 3
const CLOSE = 4
const REPLY = 5

const uint32 = (n) => [(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff]
const ascii = (s) => [...Buffer.from(s, 'utf8')]
const frame = (type, channel, ...parts) => [type, channel & 0xff, channel >> 8, ...parts.flat()]

const sshOpen = (s, ch, kind) => event(s, 'i', frame(OPEN, ch, ascii(kind), [0]))
const sshRequest = (s, ch, kind, payload) => event(s, 'i', frame(REQUEST, ch, ascii(kind), [0, 1], payload))
const sshPty = (s, ch, cols, rows) =>
  sshRequest(s, ch, 'pty-req', [...uint32(5), ...ascii('xterm'), ...uint32(cols), ...uint32(rows),
    ...uint32(0), ...uint32(0), ...uint32(0)])
const sshResize = (s, ch, cols, rows) =>
  sshRequest(s, ch, 'window-change', [...uint32(cols), ...uint32(rows), ...uint32(0), ...uint32(0)])
const sshData = (s, type, ch, data) => event(s, type, frame(DATA, ch, [...bytesOf(data)]))
const sshClose = (s, ch) => event(s, 'o', frame(CLOSE, ch, ascii('session')))

// An interactive shell over the hoop SSH proxy: one pty channel, a resize,
// and a port forward on a second channel that the terminal must skip.
export function sshShellStream() {
  const prompt = 'owner@seed-host:~$ '
  const out = [
    sshOpen(0, 1, 'session'),
    sshPty(0.1, 1, 100, 30),
    sshRequest(0.2, 1, 'shell', []),
    event(0.3, 'o', frame(REPLY, 1, [1])),
    sshData(0.4, 'o', 1, 'Welcome to seed-host\r\n' + prompt),
  ]
  let t = 1
  for (const ch of 'uptime\r') {
    out.push(sshData(t, 'i', 1, ch), sshData(t + 0.05, 'o', 1, ch === '\r' ? '\r\n' : ch))
    t += 0.25
  }
  out.push(
    sshData(t, 'o', 1, ' 12:00:01 up 42 days,  3 users,  load average: 0.08, 0.03, 0.01\r\n' + prompt),
    sshOpen(t + 0.5, 2, 'direct-tcpip'),
    sshData(t + 0.6, 'i', 2, 'GET / HTTP/1.1\r\n\r\n'),
    sshResize(t + 1, 1, 140, 40),
    sshData(t + 1.5, 'i', 1, 'exit\r'),
    sshData(t + 1.6, 'o', 1, 'exit\r\nlogout\r\n'),
    sshClose(t + 1.7, 1),
  )
  return out
}

// An SSH session with no pty (git over SSH): the viewer renders raw rows.
export function sshNoPtyStream() {
  return [
    sshOpen(0, 1, 'session'),
    sshRequest(0.1, 1, 'exec', [...uint32(31), ...ascii("git-upload-pack 'seed/repo.git'")]),
    sshData(0.3, 'o', 1, '00a0ab12cd34 HEAD\0multi_ack side-band-64k ofs-delta\n'),
    sshData(0.5, 'i', 1, '0000'),
    sshClose(0.6, 1),
  ]
}

// Postgres wire protocol (format wire-proto): one message per event. The
// gateway turns the Query and Parse messages into SQL (raw-queries).
const pgMessage = (tag, body) => {
  const len = Buffer.alloc(4)
  len.writeUInt32BE(body.length + 4)
  return Buffer.concat([Buffer.from(tag), len, body])
}
const cstr = (s) => Buffer.concat([Buffer.from(s, 'utf8'), Buffer.from([0])])
const pgQuery = (sql) => pgMessage('Q', cstr(sql))
const pgParse = (sql) => pgMessage('P', Buffer.concat([cstr(''), cstr(sql), Buffer.from([0, 0])]))
const pgBind = () => pgMessage('B', Buffer.concat([cstr(''), cstr(''), Buffer.from([0, 0, 0, 0, 0, 0])]))
const pgExecute = () => pgMessage('E', Buffer.concat([cstr(''), Buffer.from([0, 0, 0, 0])]))
const pgSync = () => pgMessage('S', Buffer.alloc(0))
const pgComplete = (tag) => pgMessage('C', cstr(tag))
const pgReady = () => pgMessage('Z', Buffer.from('I'))

export function pgWireStream({ live = false } = {}) {
  const out = [
    event(0.5, 'i', pgQuery('SELECT version();')),
    event(0.6, 'o', pgComplete('SELECT 1')),
    event(0.6, 'o', pgReady()),
    event(2, 'i', pgParse('SELECT id, email FROM customers WHERE id = $1')),
    event(2, 'i', pgBind()),
    event(2, 'i', pgExecute()),
    event(2, 'i', pgSync()),
    event(2.1, 'o', pgComplete('SELECT 1')),
    event(2.1, 'o', pgReady()),
  ]
  if (!live) {
    out.push(
      event(4, 'i', pgQuery("UPDATE customers SET plan = 'pro' WHERE id = 7;")),
      event(4.1, 'o', pgComplete('UPDATE 1')),
      event(4.1, 'o', pgReady()),
    )
  }
  return out
}

// An HTTP proxy session: requests in, responses out.
export function httpStream() {
  return [
    event(0.3, 'i', 'GET /api/health HTTP/1.1\r\nHost: seed-web.internal\r\nUser-Agent: curl/8.7.1\r\n\r\n'),
    event(0.4, 'o', 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 11\r\n\r\n{"ok":true}'),
    event(1.2, 'i', 'POST /api/orders HTTP/1.1\r\nHost: seed-web.internal\r\nContent-Type: application/json\r\n\r\n{"sku":"A-1"}'),
    event(1.3, 'o', 'HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\n\r\nforbidden'),
  ]
}

// RDP recording (gateway/rdp/recorder.go): a handshake entry, then one entry
// per bitmap update. A bitmap is uncompressed 16 bpp RGB565, bottom-up, as
// rdpBitmapToRGBA (webapp/resources/public/rdpclient/rle.js) reads it.
export const RDP_WIDTH = 800
export const RDP_HEIGHT = 480

const rgb565 = ([r, g, b]) => ((r >> 3) << 11) | ((g >> 2) << 5) | (b >> 3)

function bitmap(x, y, width, height, color) {
  const data = Buffer.alloc(width * height * 2)
  const pixel = rgb565(color)
  for (let i = 0; i < width * height; i++) data.writeUInt16LE(pixel, i * 2)
  const json = JSON.stringify({ x, y, width, height, bits_per_pixel: 16, compressed: false, data: data.toString('base64') })
  return Buffer.from(json)
}

// The frames: a desktop in 5x5 tiles, a window, a form with three filled
// fields (where the PII detections sit), and a cursor that moves.
export function rdpStream() {
  const out = [event(0, 'h', 'seed-rdp-handshake')]
  const tileW = RDP_WIDTH / 5
  const tileH = RDP_HEIGHT / 5
  let t = 0.5
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 5; col++) {
      const shade = 60 + (row + col) * 12
      out.push(event(t, 'b', bitmap(col * tileW, row * tileH, tileW, tileH, [20, 40, shade])))
    }
    t += 0.2
  }
  out.push(event(2, 'b', bitmap(160, 96, 480, 288, [236, 236, 240])))
  out.push(event(2.1, 'b', bitmap(160, 96, 480, 24, [62, 99, 221])))
  for (const [i, y] of [140, 190, 240].entries()) {
    out.push(event(3 + i, 'b', bitmap(200, y, 160, 28, [255, 255, 255])))
    out.push(event(3.5 + i, 'b', bitmap(204, y + 8, 120 - i * 20, 12, [30, 30, 30])))
  }
  for (let i = 0; i < 8; i++) {
    out.push(event(6 + i * 0.5, 'b', bitmap(420 + i * 20, 300 - i * 10, 12, 12, [250, 250, 250])))
  }
  return out
}

export const RDP_DETECTIONS = [
  { frame_index: 28, timestamp: 3.5, entity_type: 'PERSON', score: 0.91, x: 204, y: 148, width: 120, height: 12 },
  { frame_index: 30, timestamp: 4.5, entity_type: 'EMAIL_ADDRESS', score: 0.97, x: 204, y: 198, width: 100, height: 12 },
  { frame_index: 32, timestamp: 5.5, entity_type: 'PHONE_NUMBER', score: 0.84, x: 204, y: 248, width: 80, height: 12 },
]

// A stream whose output is not valid UTF-8.
export function binaryStream() {
  return [event(0.1, 'o', [0x50, 0x4b, 0x03, 0x04, 0xff, 0xfe, 0x00, 0x80, 0x0a])]
}
