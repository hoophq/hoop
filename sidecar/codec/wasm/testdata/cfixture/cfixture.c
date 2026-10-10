// cfixture.c is the x-cfix test protocol: a frame is one length byte and
// that many bytes of text. It exists to exercise the host, and it is
// freestanding C so the test fixture depends on nothing but clang's
// wasm32 target.
//
// One source builds every variant; build.sh lists them. Each -D flag
// breaks one rule of abi/ABI.md so a test can prove the host refuses it,
// or misbehaves in one way (trap, spin, memory growth) so a test can
// prove the failure semantics.
#include <stdint.h>

typedef uint32_t u32;
typedef uint64_t u64;

#define EXPORT(n) __attribute__((export_name(n)))
#define IMPORT(m, n) __attribute__((import_module(m), import_name(n)))

IMPORT("hoop", "log") void hoop_log(u32 level, const char *p, u32 n);
#ifdef SQL
IMPORT("hoop", "analyze_sql") u64 hoop_analyze_sql(const char *p, u32 n);
#endif
#ifdef MASKABUSE
IMPORT("hoop", "mask") u64 hoop_mask(const char *c, u32 cn, const char *v, u32 vn);
#endif
#ifdef UNDECLARED
IMPORT("env", "bogus") void env_bogus(void);
#endif
#ifdef WASI
typedef struct { const void *buf; u32 len; } ciovec;
IMPORT("wasi_snapshot_preview1", "fd_write") u32 fd_write(u32 fd, const ciovec *iovs, u32 n, u32 *nwritten);
#endif

// The manifest. Variants override the pieces through -D.
#ifndef ABI_VERSION
#define ABI_VERSION "1"
#endif
#ifndef PROTOCOL
#define PROTOCOL "x-cfix"
#endif
#ifndef CAPS
#define CAPS "[\"deny\"]"
#endif
#ifndef EXTRA
#define EXTRA ""
#endif
static const char manifest[] =
    "{\"abi\":" ABI_VERSION ",\"protocol\":\"" PROTOCOL "\",\"label\":\"C Fixture\","
    "\"version\":\"0.1.0\",\"capabilities\":" CAPS ","
    "\"options\":[{\"name\":\"refuse\",\"label\":\"Refuse connections\",\"type\":\"bool\",\"default\":\"false\"}]"
    EXTRA "}";

// A bump allocator. Every region is dead once the export that produced or
// received it has returned and the host has called free, so free resets
// the arena when it releases the last live region. Memory grows on
// demand; a failed grow traps, which is the ABI's answer to a guest past
// its limit.
extern unsigned char __heap_base;
static u32 top, live;

static u32 grow_to(u32 end) {
  u32 size = __builtin_wasm_memory_size(0) * 65536u;
  if (end <= size) return 1;
  u32 need = (end - size + 65535u) / 65536u;
  return __builtin_wasm_memory_grow(0, need) != (u32)-1;
}

EXPORT("alloc") u32 cfix_alloc(u32 n) {
  if (top == 0) top = (u32)&__heap_base;
  u32 p = (top + 7u) & ~7u;
  if (!grow_to(p + n)) __builtin_trap();
  top = p + n;
  live++;
  return p;
}

EXPORT("free") void cfix_free(u32 p, u32 n) {
  (void)p;
  (void)n;
  if (live != 0 && --live == 0) top = (u32)&__heap_base;
}

static u64 pack(const char *p, u32 n) { return ((u64)(u32)p << 32) | n; }

// Output builder over one region whose capacity the caller sizes.
static char *ob;
static u32 on;

static void begin(u32 cap) {
  ob = (char *)cfix_alloc(cap);
  on = 0;
}
static void put(const char *s, u32 n) {
  for (u32 i = 0; i < n; i++) ob[on++] = s[i];
}
static void puts_(const char *s) {
  u32 n = 0;
  while (s[n]) n++;
  put(s, n);
}
static void put_u32(u32 v) {
  char t[10];
  u32 i = 10;
  do {
    t[--i] = (char)('0' + v % 10);
    v /= 10;
  } while (v);
  put(t + i, 10 - i);
}
static void put_json_str(const unsigned char *s, u32 n) {
  static const char hex[] = "0123456789abcdef";
  put("\"", 1);
  for (u32 i = 0; i < n; i++) {
    unsigned char c = s[i];
    if (c == '"' || c == '\\') {
      char e[2] = {'\\', (char)c};
      put(e, 2);
    } else if (c < 0x20) {
      char e[6] = {'\\', 'u', '0', '0', hex[c >> 4], hex[c & 15]};
      put(e, 6);
    } else {
      put((const char *)s + i, 1);
    }
  }
  put("\"", 1);
}
static u64 finish(void) { return pack(ob, on); }

// Per-connection state, so an instancing test can tell connections apart:
// seq counts the frames each connection decoded.
static u32 seq[16], opens, closes, initialized;

EXPORT("_initialize") void cfix_initialize(void) { initialized = 1; }

EXPORT("describe") u64 cfix_describe(void) {
  begin(sizeof(manifest));
  put(manifest, sizeof(manifest) - 1);
  return finish();
}

static int contains(const char *s, u32 n, const char *needle) {
  u32 m = 0;
  while (needle[m]) m++;
  if (m > n) return 0;
  for (u32 i = 0; i + m <= n; i++) {
    u32 j = 0;
    while (j < m && s[i + j] == needle[j]) j++;
    if (j == m) return 1;
  }
  return 0;
}

EXPORT("open") u32 cfix_open(u32 conn, const char *opts, u32 n) {
  hoop_log(0, opts, n);
  seq[conn & 15] = 0;
  opens++;
  return contains(opts, n, "\"refuse\":\"true\"") ? 1 : 0;
}

EXPORT("close") void cfix_close(u32 conn) {
  seq[conn & 15] = 0;
  closes++;
}

#ifdef GROW
// text is a decimal page count; the statement reports whether memory.grow
// honoured it, so a test can prove the manifest's memory_limit_pages
// reached the runtime.
static u32 parse_u32(const unsigned char *s, u32 n) {
  u32 v = 0;
  for (u32 i = 0; i < n; i++) v = v * 10 + (s[i] - '0');
  return v;
}
#endif

EXPORT("decode") u64 cfix_decode(u32 conn, u32 dir, const unsigned char *in, u32 n) {
  (void)dir;
#ifdef DECODE_TRAP
  __builtin_trap();
#endif
#ifdef DECODE_SPIN
  for (volatile u32 spin = 1; spin;) {
  }
#endif
#ifdef UNDECLARED
  if (n == 0xFFFFFFFFu) env_bogus();
#endif
#ifdef MASKABUSE
  hoop_mask("c", 1, "v", 1);
#endif
#ifdef WASI
  static const char hi[] = "cfix says hi\n";
  ciovec iov = {hi, sizeof(hi) - 1};
  u32 wrote;
  fd_write(1, &iov, 1, &wrote);
#endif
  // Worst case: every byte escaped to six, plus the fixed JSON per frame,
  // and a frame is at least one byte.
  begin(512u + 160u * n);
  puts_("{\"statements\":[");
  u32 off = 0, count = 0;
  while (off < n) {
    u32 len = in[off];
    if (off + 1 + len > n) break; // incomplete trailing frame: the host retains it
    if (len == 0) {
      begin(64);
      puts_("{\"error\":\"empty frame\"}");
      return finish();
    }
    const unsigned char *text = in + off + 1;
    if (count++) put(",", 1);
#ifdef GROW
    u32 pages = parse_u32(text, len);
    if (__builtin_wasm_memory_grow(0, pages) == (u32)-1) {
      begin(64);
      puts_("{\"error\":\"memory.grow(");
      put_u32(pages);
      puts_(") failed\"}");
      return finish();
    }
    puts_("{\"operation\":\"other\",\"text\":\"grew ");
    put_u32(pages);
    puts_("\"");
#elif defined(SQL)
    u64 a = hoop_analyze_sql((const char *)text, len);
    const unsigned char *ap = (const unsigned char *)(u32)(a >> 32);
    u32 an = (u32)a;
    puts_("{\"operation\":\"other\",\"text\":");
    put_json_str(ap, an);
    cfix_free((u32)ap, an);
#else
    puts_("{\"operation\":\"other\",\"text\":");
    put_json_str(text, len);
#endif
    puts_(",\"metadata\":{\"x-cfix.verb\":\"TEXT\",\"x-cfix.conn\":\"");
    put_u32(conn);
    puts_("\",\"x-cfix.seq\":\"");
    put_u32(++seq[conn & 15]);
    puts_("\",\"x-cfix.opens\":\"");
    put_u32(opens);
    puts_("\",\"x-cfix.closes\":\"");
    put_u32(closes);
    puts_("\",\"x-cfix.initialized\":\"");
    put_u32(initialized);
    puts_("\"}}");
    off += 1 + len;
  }
  puts_("],\"consumed\":");
  put_u32(off);
  puts_("}");
  return finish();
}

#ifndef NO_DENY
EXPORT("deny") u64 cfix_deny(u32 conn, u32 dir, const char *msg, u32 n) {
  (void)conn;
  (void)dir;
  begin(8 + n);
  puts_("CFIX:");
  put(msg, n);
  return finish();
}
#endif
