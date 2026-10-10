/*
 * hoop_codec.h: the hoop codec plug-in ABI for C (see ../../abi/ABI.md).
 *
 * A plug-in is one freestanding wasm32 module:
 *
 *   clang --target=wasm32-unknown-unknown -nostdlib -fno-builtin -O2 \
 *     -Wl,--no-entry -o plugin.wasm plugin.c
 *
 * HOOP_EXPORT marks a function exported, so you need no --export flag and
 * nothing else leaks into the export list. -fno-builtin keeps the
 * optimizer from turning a copy loop into a memcpy call there is no libc
 * to satisfy. Linking needs wasm-ld (LLVM's lld).
 *
 * The header declares the four imports of module `hoop`, the attribute an
 * export carries, the packed-u64 helpers every export returns through,
 * and, under HOOP_CODEC_ARENA, an `alloc`/`free` pair that needs no libc.
 * The skeleton at the end of this file is a complete plug-in to copy.
 *
 * Everything is C99 and nothing here needs a libc: a module that links
 * nothing imports nothing the host would refuse.
 */
#ifndef HOOP_CODEC_H
#define HOOP_CODEC_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The ABI version this header speaks; the manifest's "abi". */
#define HOOP_ABI_VERSION 1

/* `dir` of decode, deny and filter. */
enum hoop_direction { HOOP_DIR_CLIENT = 0, HOOP_DIR_SERVER = 1 };

/* `level` of hoop_log. */
enum hoop_log_level { HOOP_LOG_DEBUG = 0, HOOP_LOG_INFO = 1, HOOP_LOG_WARN = 2, HOOP_LOG_ERROR = 3 };

/* Marks a function as the export `name`. The wasm name is the ABI's; the
 * C name is yours, so `free` and `alloc` need not shadow libc's even when
 * you link one. */
#define HOOP_EXPORT(name) __attribute__((export_name(name), visibility("default")))

/* Marks a declaration as the import `name` of module `hoop`. */
#define HOOP_IMPORT(name) __attribute__((import_module("hoop"), import_name(name)))

/*
 * Imports. Each returns a packed region (see hoop_pack) the host wrote
 * through your `alloc`; read it, then release it with your `free`. A
 * module calls only what it needs: the linker drops an unreferenced
 * import, and a module that never calls analyze_sql owes the host no
 * sql_dialect.
 */

/* Classify SQL with the host lexer in the manifest's sql_dialect; returns
 * SQLAnalysis JSON. */
HOOP_IMPORT("analyze_sql") uint64_t hoop_analyze_sql(const char *sql, uint32_t len);

/* Split a multi-statement text with the host lexer; returns a JSON array
 * of strings. */
HOOP_IMPORT("split_sql") uint64_t hoop_split_sql(const char *sql, uint32_t len);

/* Mask one result-set cell; returns the value to forward, which may differ
 * in length. Legal only on the stack of rewrite or flush: the host traps
 * a call from anywhere else. */
HOOP_IMPORT("mask") uint64_t hoop_mask(const char *column, uint32_t column_len, const uint8_t *value, uint32_t value_len);

/* One log line, tagged by the host with the lane and connection. */
HOOP_IMPORT("log") void hoop_log(uint32_t level, const char *message, uint32_t len);

/*
 * Packed regions. Every export that returns bytes returns one u64:
 * (ptr << 32) | len, and 0 means no output. The host reads len bytes at
 * ptr and then calls free(ptr, len), so the region must come from your
 * allocator and you must not free it.
 */

/* HOOP_NONE is the packed "no output". */
#define HOOP_NONE ((uint64_t)0)

static inline uint64_t hoop_pack(const void *ptr, uint32_t len) {
    if (len == 0) {
        return HOOP_NONE;
    }
    return ((uint64_t)(uint32_t)(uintptr_t)ptr << 32) | (uint64_t)len;
}

static inline void *hoop_ptr(uint64_t packed) { return (void *)(uintptr_t)(uint32_t)(packed >> 32); }

static inline uint32_t hoop_len(uint64_t packed) { return (uint32_t)packed; }

/* hoop_strlen avoids a libc for the one string function a plug-in
 * reaches for when handing a C string to hoop_pack or hoop_log. */
static inline uint32_t hoop_strlen(const char *s) {
    uint32_t n = 0;
    while (s[n] != '\0') {
        n++;
    }
    return n;
}

/*
 * An arena allocator for the ABI buffers, when the plug-in links no libc.
 *
 * Define HOOP_CODEC_ARENA in a single translation unit to get the
 * `alloc` and `free` exports. It is a bump allocator that rewinds when
 * nothing is outstanding: the host frees an export's input right after
 * the export returns and an export's output right after reading it, so
 * between calls the arena is empty and the pointer goes back to the
 * start. That bounds memory to the largest single call, which is the
 * same bound the host's max_reassembly already imposes on input.
 *
 * Consequence: nothing that must outlive one export call may live in the
 * arena. Keep per-connection state in static storage.
 *
 * HOOP_CODEC_ARENA_BYTES sets the size; the default takes four pages. A
 * request the arena cannot serve traps, which the host reports as a
 * failed call and fails the connection closed.
 */
#ifdef HOOP_CODEC_ARENA

#ifndef HOOP_CODEC_ARENA_BYTES
#define HOOP_CODEC_ARENA_BYTES (4u * 65536u)
#endif

static uint8_t hoop_arena[HOOP_CODEC_ARENA_BYTES] __attribute__((aligned(16)));
static uint32_t hoop_arena_used = 0;
static uint32_t hoop_arena_live = 0;

HOOP_EXPORT("alloc") uint32_t hoop_alloc(uint32_t len) {
    uint32_t rounded = (len + 15u) & ~15u;
    if (rounded < len || HOOP_CODEC_ARENA_BYTES - hoop_arena_used < rounded) {
        hoop_log(HOOP_LOG_ERROR, "hoop_codec: arena exhausted", 27);
        __builtin_trap();
    }
    uint8_t *p = hoop_arena + hoop_arena_used;
    hoop_arena_used += rounded;
    hoop_arena_live++;
    return (uint32_t)(uintptr_t)p;
}

HOOP_EXPORT("free") void hoop_free(uint32_t ptr, uint32_t len) {
    (void)ptr;
    (void)len;
    if (hoop_arena_live > 0 && --hoop_arena_live == 0) {
        hoop_arena_used = 0;
    }
}

/* hoop_take reads a packed region a host import returned into the arena
 * and releases it: the import allocated through hoop_alloc, so the bytes
 * stay readable until the arena rewinds at the end of the call. */
static inline const uint8_t *hoop_take(uint64_t packed, uint32_t *len) {
    *len = hoop_len(packed);
    if (packed == HOOP_NONE) {
        return (const uint8_t *)"";
    }
    hoop_free((uint32_t)(uintptr_t)hoop_ptr(packed), *len);
    return (const uint8_t *)hoop_ptr(packed);
}

#endif /* HOOP_CODEC_ARENA */

#ifdef __cplusplus
}
#endif

#endif /* HOOP_CODEC_H */

/*
 * Skeleton. A complete plug-in for a protocol whose every frame is one
 * length byte and that many bytes of text; it declares the deny
 * capability and nothing else, so its export list is: memory, alloc,
 * free, describe, decode, open, close, deny. Build it with the clang line
 * at the top of this file.
 *
 *   #define HOOP_CODEC_ARENA
 *   #include "hoop_codec.h"
 *
 *   // This skeleton writes JSON by hand; a real plug-in links a JSON
 *   // encoder and escapes its strings. Everything the host reads is UTF-8 JSON.
 *   static const char MANIFEST[] =
 *       "{\"abi\":1,\"protocol\":\"x-skel\",\"label\":\"Skeleton\","
 *       "\"capabilities\":[\"deny\"]}";
 *
 *   // Per-connection state lives in static storage, never in the arena.
 *   static uint32_t frames_seen;
 *
 *   HOOP_EXPORT("describe") uint64_t skel_describe(void) {
 *       return hoop_pack(MANIFEST, sizeof MANIFEST - 1);
 *   }
 *
 *   HOOP_EXPORT("open") uint32_t skel_open(uint32_t conn, const uint8_t *options, uint32_t len) {
 *       (void)conn; (void)options; (void)len;  // Options JSON, string to string
 *       frames_seen = 0;
 *       return 0;                               // non-zero refuses the connection
 *   }
 *
 *   HOOP_EXPORT("close") void skel_close(uint32_t conn) { (void)conn; }
 *
 *   static uint32_t put(uint8_t *out, uint32_t n, const char *s) {
 *       while (*s) out[n++] = (uint8_t)*s++;
 *       return n;
 *   }
 *
 *   HOOP_EXPORT("decode") uint64_t skel_decode(uint32_t conn, uint32_t dir, const uint8_t *data, uint32_t len) {
 *       (void)conn; (void)dir;
 *       // One frame: length byte, then text. On an incomplete frame,
 *       // report consumed = 0 and the host retries with more.
 *       if (len == 0 || len < 1u + data[0]) {
 *           static const char EMPTY[] = "{\"statements\":[],\"consumed\":0}";
 *           return hoop_pack(EMPTY, sizeof EMPTY - 1);
 *       }
 *       frames_seen++;
 *       uint32_t plen = data[0];
 *       uint8_t *out = (uint8_t *)(uintptr_t)hoop_alloc(80 + 2 * plen);
 *       uint32_t n = put(out, 0, "{\"statements\":[{\"text\":\"");
 *       for (uint32_t i = 0; i < plen; i++) {
 *           uint8_t c = data[1 + i];
 *           if (c == '"' || c == '\\') out[n++] = '\\';
 *           if (c < 0x20) c = '?';
 *           out[n++] = c;
 *       }
 *       n = put(out, n, "\",\"operation\":\"other\"}],\"consumed\":");
 *       uint32_t consumed = 1 + plen;
 *       char digits[11];
 *       int d = 0;
 *       do { digits[d++] = (char)('0' + consumed % 10); consumed /= 10; } while (consumed);
 *       while (d) out[n++] = (uint8_t)digits[--d];
 *       out[n++] = '}';
 *       return hoop_pack(out, n);
 *   }
 *
 *   HOOP_EXPORT("deny") uint64_t skel_deny(uint32_t conn, uint32_t dir, const uint8_t *message, uint32_t len) {
 *       (void)conn; (void)dir;
 *       if (len > 255) len = 255;
 *       uint8_t *out = (uint8_t *)(uintptr_t)hoop_alloc(1 + len);
 *       out[0] = (uint8_t)len;
 *       for (uint32_t i = 0; i < len; i++) out[1 + i] = message[i];
 *       return hoop_pack(out, 1 + len);
 *   }
 */
