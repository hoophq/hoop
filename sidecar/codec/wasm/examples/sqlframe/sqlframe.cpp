// x-sqlframe: a codec plug-in for a binary framing that carries SQL.
//
// The module is C++17 freestanding over hoop_codec.h: no libc, no STL, no
// exceptions, no RTTI. It leans on the host for the hard part, the SQL
// classifier, through hoop_split_sql and hoop_analyze_sql, and splices the
// analysis the host returns into the statement it emits. README.md
// describes the frame layout and the build.
//
// Memory: the arena from hoop_codec.h serves every ABI buffer. The arena
// rewinds when the live count reaches zero, which happens only between
// export calls, so a region a host import returned stays readable until
// the export returns. Per-connection state lives in `conn_state`, static,
// because the arena keeps nothing across calls.
#define HOOP_CODEC_ARENA
#define HOOP_CODEC_ARENA_BYTES (8u * 1024u * 1024u)
#include "hoop_codec.h"

// The optimizer lowers aggregate copies to memcpy/memset calls even under
// -fno-builtin; with no libc, the module defines them.
extern "C" void *memcpy(void *dst, const void *src, size_t n) {
    uint8_t *d = static_cast<uint8_t *>(dst);
    const uint8_t *s = static_cast<const uint8_t *>(src);
    for (size_t i = 0; i < n; i++) d[i] = s[i];
    return dst;
}

extern "C" void *memset(void *dst, int c, size_t n) {
    uint8_t *d = static_cast<uint8_t *>(dst);
    for (size_t i = 0; i < n; i++) d[i] = static_cast<uint8_t>(c);
    return dst;
}

namespace {

constexpr uint8_t MAGIC = 0x5A;
constexpr uint32_t HEADER_LEN = 6;
// Must agree with "max_reassembly" in MANIFEST: a frame that declares
// more than this can never complete, so decode fails it at the header.
constexpr uint32_t MAX_REASSEMBLY = 1u << 20;
// Bound on the dialect_label option, kept in static storage.
constexpr uint32_t DIALECT_MAX = 128;

enum FrameType : uint8_t {
    T_QUERY = 0x01,
    T_PREPARE = 0x02,
    T_PING = 0x03,
    T_CLOSE = 0x7F,
    T_ROWS = 0x81,
    T_OK = 0x82,
    T_ERROR = 0x8E,
};

const char MANIFEST[] =
    "{\"abi\":1,"
    "\"protocol\":\"x-sqlframe\","
    "\"label\":\"SQL Frame\","
    "\"version\":\"0.1.0\","
    "\"capabilities\":[\"deny\",\"filter\"],"
    "\"sql_dialect\":\"postgres\","
    "\"max_reassembly\":1048576,"
    "\"instances\":\"per_connection\","
    "\"options\":[{\"name\":\"dialect_label\",\"label\":\"Dialect label\",\"type\":\"string\","
    "\"default\":\"postgres\","
    "\"help\":\"Copied to metadata x-sqlframe.dialect on every QUERY and PREPARE statement\"}]}";

const char EMPTY_RESULT[] = "{\"statements\":[],\"consumed\":0}";

// The filter's position in the current frame of one direction. Padding is
// legal only at a frame boundary, so the cursor remembers how far into a
// frame the stream is under any chunking.
struct FilterCursor {
    uint8_t header[HEADER_LEN];
    uint32_t header_len;
    uint32_t remaining;
};

struct ConnState {
    // Frames completed per direction; metadata x-sqlframe.seq.
    uint32_t seq[2];
    // The dialect_label option as its JSON string token, quotes and
    // escapes included, so decode can emit it without re-encoding.
    uint8_t dialect[DIALECT_MAX];
    uint32_t dialect_len;
    FilterCursor filter[2];
};

ConnState conn_state;

struct Span {
    const uint8_t *p;
    uint32_t n;
};

// Writer builds one output buffer in the arena. It grows by doubling and
// frees the block it leaves behind, so the arena's live count stays
// balanced: the dead block is reclaimed when the arena rewinds.
struct Writer {
    uint8_t *buf = nullptr;
    uint32_t len = 0;
    uint32_t cap = 0;

    void reserve(uint32_t extra) {
        if (cap - len >= extra) return;
        uint32_t want = len + extra;
        uint32_t ncap = cap ? cap : 1024;
        while (ncap < want) ncap *= 2;
        uint8_t *nb = reinterpret_cast<uint8_t *>(hoop_alloc(ncap));
        for (uint32_t i = 0; i < len; i++) nb[i] = buf[i];
        if (buf) hoop_free(static_cast<uint32_t>(reinterpret_cast<uintptr_t>(buf)), cap);
        buf = nb;
        cap = ncap;
    }

    void put(uint8_t c) {
        reserve(1);
        buf[len++] = c;
    }

    void bytes(const uint8_t *p, uint32_t n) {
        reserve(n);
        for (uint32_t i = 0; i < n; i++) buf[len + i] = p[i];
        len += n;
    }

    void bytes(Span s) { bytes(s.p, s.n); }

    void lit(const char *s) { bytes(reinterpret_cast<const uint8_t *>(s), hoop_strlen(s)); }

    // A JSON string: quotes, escapes for the quote, the backslash and
    // control characters. Bytes at or above 0x80 pass through as UTF-8.
    void str(const uint8_t *p, uint32_t n) {
        static const char HEX[] = "0123456789abcdef";
        put('"');
        for (uint32_t i = 0; i < n; i++) {
            uint8_t c = p[i];
            switch (c) {
            case '"': lit("\\\""); break;
            case '\\': lit("\\\\"); break;
            case '\n': lit("\\n"); break;
            case '\r': lit("\\r"); break;
            case '\t': lit("\\t"); break;
            default:
                if (c < 0x20) {
                    lit("\\u00");
                    put(static_cast<uint8_t>(HEX[c >> 4]));
                    put(static_cast<uint8_t>(HEX[c & 0xF]));
                } else {
                    put(c);
                }
            }
        }
        put('"');
    }

    void str(const char *s) { str(reinterpret_cast<const uint8_t *>(s), hoop_strlen(s)); }

    void dec(uint32_t v) {
        char digits[10];
        int d = 0;
        do {
            digits[d++] = static_cast<char>('0' + v % 10);
            v /= 10;
        } while (v);
        while (d) put(static_cast<uint8_t>(digits[--d]));
    }

    void hex2(uint8_t v) {
        static const char HEX[] = "0123456789abcdef";
        put(static_cast<uint8_t>(HEX[v >> 4]));
        put(static_cast<uint8_t>(HEX[v & 0xF]));
    }

    uint64_t pack() const { return hoop_pack(buf, len); }
};

// A minimal JSON scanner over what the host produces: well-formed objects
// and arrays. It never builds a tree; callers take raw value spans and
// splice them into the output, which keeps the analysis fields byte for
// byte as the host wrote them.
bool is_ws(uint8_t c) { return c == ' ' || c == '\n' || c == '\r' || c == '\t'; }

uint32_t skip_ws(const uint8_t *p, uint32_t n, uint32_t i) {
    while (i < n && is_ws(p[i])) i++;
    return i;
}

// i at the opening quote; returns the index past the closing quote, or n
// when the string never closes.
uint32_t skip_string(const uint8_t *p, uint32_t n, uint32_t i) {
    i++;
    while (i < n) {
        if (p[i] == '\\') {
            i += 2;
            continue;
        }
        if (p[i] == '"') return i + 1;
        i++;
    }
    return n;
}

// Returns the index past the value starting at i, or n when malformed.
uint32_t skip_value(const uint8_t *p, uint32_t n, uint32_t i) {
    i = skip_ws(p, n, i);
    if (i >= n) return n;
    if (p[i] == '"') return skip_string(p, n, i);
    if (p[i] == '{' || p[i] == '[') {
        uint32_t depth = 0;
        while (i < n) {
            uint8_t c = p[i];
            if (c == '"') {
                i = skip_string(p, n, i);
                continue;
            }
            if (c == '{' || c == '[') depth++;
            if (c == '}' || c == ']') {
                depth--;
                if (depth == 0) return i + 1;
            }
            i++;
        }
        return n;
    }
    while (i < n && p[i] != ',' && p[i] != '}' && p[i] != ']' && !is_ws(p[i])) i++;
    return i;
}

bool key_is(const uint8_t *p, uint32_t start, uint32_t end, const char *key) {
    // start..end spans the key token including its quotes.
    uint32_t klen = hoop_strlen(key);
    if (end - start != klen + 2) return false;
    for (uint32_t i = 0; i < klen; i++) {
        if (p[start + 1 + i] != static_cast<uint8_t>(key[i])) return false;
    }
    return true;
}

// Finds `key` at the top level of the object at p and returns its raw
// value span. False when absent or when the object is malformed.
bool find_key(const uint8_t *p, uint32_t n, const char *key, Span *out) {
    uint32_t i = skip_ws(p, n, 0);
    if (i >= n || p[i] != '{') return false;
    i++;
    for (;;) {
        i = skip_ws(p, n, i);
        if (i >= n) return false;
        if (p[i] == '}') return false;
        if (p[i] == ',') {
            i++;
            continue;
        }
        if (p[i] != '"') return false;
        uint32_t kstart = i;
        uint32_t kend = skip_string(p, n, i);
        if (kend >= n) return false;
        i = skip_ws(p, n, kend);
        if (i >= n || p[i] != ':') return false;
        uint32_t vstart = skip_ws(p, n, i + 1);
        uint32_t vend = skip_value(p, n, vstart);
        if (vend > n || vend == vstart) return false;
        if (key_is(p, kstart, kend, key)) {
            out->p = p + vstart;
            out->n = vend - vstart;
            return true;
        }
        i = vend;
    }
}

// Walks the array at p. *i starts at 0 and advances past each element;
// false at the end or on malformed input.
bool next_elem(const uint8_t *p, uint32_t n, uint32_t *i, Span *out) {
    uint32_t k = skip_ws(p, n, *i);
    if (*i == 0) {
        if (k >= n || p[k] != '[') return false;
        k++;
    }
    k = skip_ws(p, n, k);
    if (k >= n) return false;
    if (p[k] == ']') return false;
    if (p[k] == ',') k = skip_ws(p, n, k + 1);
    uint32_t end = skip_value(p, n, k);
    if (end > n || end == k) return false;
    out->p = p + k;
    out->n = end - k;
    *i = end;
    return true;
}

int hex_digit(uint8_t c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

uint32_t put_utf8(uint8_t *out, uint32_t o, uint32_t cp) {
    if (cp < 0x80) {
        out[o++] = static_cast<uint8_t>(cp);
    } else if (cp < 0x800) {
        out[o++] = static_cast<uint8_t>(0xC0 | (cp >> 6));
        out[o++] = static_cast<uint8_t>(0x80 | (cp & 0x3F));
    } else if (cp < 0x10000) {
        out[o++] = static_cast<uint8_t>(0xE0 | (cp >> 12));
        out[o++] = static_cast<uint8_t>(0x80 | ((cp >> 6) & 0x3F));
        out[o++] = static_cast<uint8_t>(0x80 | (cp & 0x3F));
    } else {
        out[o++] = static_cast<uint8_t>(0xF0 | (cp >> 18));
        out[o++] = static_cast<uint8_t>(0x80 | ((cp >> 12) & 0x3F));
        out[o++] = static_cast<uint8_t>(0x80 | ((cp >> 6) & 0x3F));
        out[o++] = static_cast<uint8_t>(0x80 | (cp & 0x3F));
    }
    return o;
}

// Reads four hex digits at s; returns the code unit or -1.
int read_u16(const uint8_t *s, uint32_t n, uint32_t i) {
    if (i + 4 > n) return -1;
    int v = 0;
    for (uint32_t k = 0; k < 4; k++) {
        int d = hex_digit(s[i + k]);
        if (d < 0) return -1;
        v = (v << 4) | d;
    }
    return v;
}

// Decodes the content of a JSON string token (without its quotes) into
// out, which holds at least n bytes: every escape shrinks or keeps its
// length. Returns the decoded length.
uint32_t unescape(const uint8_t *s, uint32_t n, uint8_t *out) {
    uint32_t o = 0;
    for (uint32_t i = 0; i < n; i++) {
        if (s[i] != '\\' || i + 1 >= n) {
            out[o++] = s[i];
            continue;
        }
        i++;
        switch (s[i]) {
        case 'n': out[o++] = '\n'; break;
        case 'r': out[o++] = '\r'; break;
        case 't': out[o++] = '\t'; break;
        case 'b': out[o++] = '\b'; break;
        case 'f': out[o++] = '\f'; break;
        case 'u': {
            int cp = read_u16(s, n, i + 1);
            if (cp < 0) {
                out[o++] = '?';
                break;
            }
            i += 4;
            if (cp >= 0xD800 && cp < 0xDC00 && i + 6 < n && s[i + 1] == '\\' && s[i + 2] == 'u') {
                int lo = read_u16(s, n, i + 3);
                if (lo >= 0xDC00 && lo < 0xE000) {
                    cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    i += 6;
                }
            }
            o = put_utf8(out, o, static_cast<uint32_t>(cp));
            break;
        }
        default: out[o++] = s[i]; break;
        }
    }
    return o;
}

struct Frame {
    uint8_t type;
    const uint8_t *payload;
    uint32_t len;
};

enum class Parse { Complete, Partial, BadMagic, TooLong };

Parse parse_frame(const uint8_t *data, uint32_t n, Frame *f) {
    if (n < 1) return Parse::Partial;
    if (data[0] != MAGIC) return Parse::BadMagic;
    if (n < HEADER_LEN) return Parse::Partial;
    uint32_t plen = (static_cast<uint32_t>(data[2]) << 24) | (static_cast<uint32_t>(data[3]) << 16) |
                    (static_cast<uint32_t>(data[4]) << 8) | static_cast<uint32_t>(data[5]);
    if (plen > MAX_REASSEMBLY) return Parse::TooLong;
    if (n - HEADER_LEN < plen) return Parse::Partial;
    f->type = data[1];
    f->payload = data + HEADER_LEN;
    f->len = plen;
    return Parse::Complete;
}

// Statement emission. Every statement shares the metadata block; the
// seq is the frame counter of its direction.
struct Emitter {
    Writer &w;
    bool first = true;

    void open(const uint8_t *text, uint32_t text_len) {
        if (!first) w.put(',');
        first = false;
        w.lit("{\"text\":");
        w.str(text, text_len);
    }

    void metadata(const char *command, uint32_t seq, bool with_dialect, const Span *reason) {
        w.lit(",\"metadata\":{\"x-sqlframe.command\":\"");
        w.lit(command);
        w.lit("\",\"x-sqlframe.seq\":\"");
        w.dec(seq);
        w.put('"');
        if (with_dialect && conn_state.dialect_len) {
            w.lit(",\"x-sqlframe.dialect\":");
            w.bytes(conn_state.dialect, conn_state.dialect_len);
        }
        if (reason) {
            w.lit(",\"x-sqlframe.reason\":");
            w.bytes(*reason);
        }
        w.lit("}}");
    }

    // A frame with no SQL: PING, CLOSE, OK, ROWS, ERROR.
    void other(const uint8_t *text, uint32_t text_len, const char *command, uint32_t seq, const uint32_t *row_count) {
        open(text, text_len);
        w.lit(",\"operation\":\"other\"");
        if (row_count) {
            w.lit(",\"result\":{\"row_count\":");
            w.dec(*row_count);
            w.put('}');
        }
        metadata(command, seq, false, nullptr);
    }

    void copy_field(const uint8_t *a, uint32_t an, const char *key) {
        Span v;
        if (!find_key(a, an, key, &v)) return;
        w.put(',');
        w.put('"');
        w.lit(key);
        w.lit("\":");
        w.bytes(v);
    }

    // One SQL statement classified by the host.
    void sql(const uint8_t *text, uint32_t text_len, const char *command, uint32_t seq) {
        uint32_t an;
        const uint8_t *a = hoop_take(hoop_analyze_sql(reinterpret_cast<const char *>(text), text_len), &an);
        open(text, text_len);
        Span op;
        w.lit(",\"operation\":");
        if (find_key(a, an, "operation", &op)) {
            w.bytes(op);
        } else {
            w.lit("\"unknown\"");
        }
        copy_field(a, an, "effects");
        copy_field(a, an, "relations");
        copy_field(a, an, "tables");
        Span reason;
        bool has_reason = find_key(a, an, "reason", &reason) && reason.n > 2;
        metadata(command, seq, true, has_reason ? &reason : nullptr);
    }

    // The payload holds one or more statements; the host lexer splits them
    // and each becomes its own statement. An empty split, which the lexer
    // returns for text it cannot cut, yields the whole payload once.
    void sql_frame(const Frame &f, const char *command, uint32_t seq) {
        uint32_t pn;
        const uint8_t *parts = hoop_take(hoop_split_sql(reinterpret_cast<const char *>(f.payload), f.len), &pn);
        uint32_t i = 0;
        Span el;
        bool any = false;
        while (next_elem(parts, pn, &i, &el)) {
            if (el.n < 2 || el.p[0] != '"') break;
            uint8_t *text = reinterpret_cast<uint8_t *>(hoop_alloc(el.n));
            uint32_t text_len = unescape(el.p + 1, el.n - 2, text);
            sql(text, text_len, command, seq);
            hoop_free(static_cast<uint32_t>(reinterpret_cast<uintptr_t>(text)), el.n);
            any = true;
        }
        if (!any) sql(f.payload, f.len, command, seq);
    }
};

// Rewrites the writer into a DecodeResult that carries only an error.
uint64_t fail(Writer &w, const char *message, int type_byte) {
    w.len = 0;
    w.lit("{\"statements\":[],\"consumed\":0,\"error\":\"");
    w.lit(message);
    if (type_byte >= 0) {
        w.lit(" 0x");
        w.hex2(static_cast<uint8_t>(type_byte));
    }
    w.lit("\"}");
    return w.pack();
}

const char *client_frame(Emitter &e, const Frame &f, uint32_t seq) {
    switch (f.type) {
    case T_QUERY: e.sql_frame(f, "QUERY", seq); return nullptr;
    case T_PREPARE: e.sql_frame(f, "PREPARE", seq); return nullptr;
    case T_PING: e.other(reinterpret_cast<const uint8_t *>("PING"), 4, "PING", seq, nullptr); return nullptr;
    case T_CLOSE: e.other(reinterpret_cast<const uint8_t *>("CLOSE"), 5, "CLOSE", seq, nullptr); return nullptr;
    default: return "x-sqlframe: unknown client frame type";
    }
}

const char *server_frame(Emitter &e, const Frame &f, uint32_t seq) {
    switch (f.type) {
    case T_ROWS: {
        if (f.len != 4) return "x-sqlframe: ROWS payload is not a u32 row count; type";
        uint32_t n = (static_cast<uint32_t>(f.payload[0]) << 24) | (static_cast<uint32_t>(f.payload[1]) << 16) |
                     (static_cast<uint32_t>(f.payload[2]) << 8) | static_cast<uint32_t>(f.payload[3]);
        e.other(reinterpret_cast<const uint8_t *>("ROWS"), 4, "ROWS", seq, &n);
        return nullptr;
    }
    case T_OK: e.other(reinterpret_cast<const uint8_t *>("OK"), 2, "OK", seq, nullptr); return nullptr;
    case T_ERROR: {
        // text = "ERROR: " + message, built in the arena so str() escapes
        // it in one pass.
        uint32_t tl = 7 + f.len;
        uint8_t *text = reinterpret_cast<uint8_t *>(hoop_alloc(tl));
        memcpy(text, "ERROR: ", 7);
        memcpy(text + 7, f.payload, f.len);
        e.other(text, tl, "ERROR", seq, nullptr);
        hoop_free(static_cast<uint32_t>(reinterpret_cast<uintptr_t>(text)), tl);
        return nullptr;
    }
    default: return "x-sqlframe: unknown server frame type";
    }
}

}  // namespace

extern "C" {

HOOP_EXPORT("describe") uint64_t sqlframe_describe(void) { return hoop_pack(MANIFEST, sizeof MANIFEST - 1); }

// Reads dialect_label out of the Options JSON. The host merges the
// manifest default under the listener's value, so the key is always there
// and the module keeps no default of its own. A label the static buffer
// cannot hold refuses the connection.
HOOP_EXPORT("open") uint32_t sqlframe_open(uint32_t conn, const uint8_t *options, uint32_t len) {
    (void)conn;
    conn_state = ConnState{};
    Span v;
    if (!find_key(options, len, "dialect_label", &v)) return 0;
    if (v.n < 2 || v.p[0] != '"') return 0;
    if (v.n > DIALECT_MAX) {
        static const char MSG[] = "x-sqlframe: dialect_label longer than 126 bytes";
        hoop_log(HOOP_LOG_ERROR, MSG, sizeof MSG - 1);
        return 1;
    }
    memcpy(conn_state.dialect, v.p, v.n);
    conn_state.dialect_len = v.n;
    return 0;
}

HOOP_EXPORT("close") void sqlframe_close(uint32_t conn) { (void)conn; }

HOOP_EXPORT("decode") uint64_t sqlframe_decode(uint32_t conn, uint32_t dir, const uint8_t *data, uint32_t len) {
    (void)conn;
    if (len == 0) return hoop_pack(EMPTY_RESULT, sizeof EMPTY_RESULT - 1);
    Writer w;
    Emitter e{w};
    w.lit("{\"statements\":[");
    uint32_t pos = 0;
    while (pos < len) {
        Frame f;
        Parse p = parse_frame(data + pos, len - pos, &f);
        if (p == Parse::Partial) break;
        if (p == Parse::BadMagic) return fail(w, "x-sqlframe: expected frame magic 0x5a, got", data[pos]);
        if (p == Parse::TooLong) return fail(w, "x-sqlframe: frame longer than max_reassembly; type", data[pos + 1]);
        uint32_t seq = ++conn_state.seq[dir & 1];
        const char *err = dir == HOOP_DIR_CLIENT ? client_frame(e, f, seq) : server_frame(e, f, seq);
        if (err) return fail(w, err, f.type);
        pos += HEADER_LEN + f.len;
    }
    w.lit("],\"consumed\":");
    w.dec(pos);
    w.put('}');
    return w.pack();
}

// The native error frame: 0x5A 0x8E length message.
HOOP_EXPORT("deny") uint64_t sqlframe_deny(uint32_t conn, uint32_t dir, const uint8_t *message, uint32_t len) {
    (void)conn;
    (void)dir;
    uint8_t *out = reinterpret_cast<uint8_t *>(hoop_alloc(HEADER_LEN + len));
    out[0] = MAGIC;
    out[1] = T_ERROR;
    out[2] = static_cast<uint8_t>(len >> 24);
    out[3] = static_cast<uint8_t>(len >> 16);
    out[4] = static_cast<uint8_t>(len >> 8);
    out[5] = static_cast<uint8_t>(len);
    memcpy(out + HEADER_LEN, message, len);
    return hoop_pack(out, HEADER_LEN + len);
}

// Drops 0x00 keepalive padding between frames in either direction. The
// cursor holds header bytes until it knows the payload length, then lets
// that many bytes through untouched, so a zero inside a payload survives
// whether the frame arrives whole or one byte at a time.
HOOP_EXPORT("filter") uint64_t sqlframe_filter(uint32_t conn, uint32_t dir, const uint8_t *data, uint32_t len) {
    (void)conn;
    if (len == 0) return HOOP_NONE;
    FilterCursor &c = conn_state.filter[dir & 1];
    Writer w;
    w.reserve(len);
    uint32_t i = 0;
    while (i < len) {
        if (c.remaining > 0) {
            uint32_t n = c.remaining < len - i ? c.remaining : len - i;
            w.bytes(data + i, n);
            c.remaining -= n;
            i += n;
            continue;
        }
        if (c.header_len == 0 && data[i] == 0) {
            i++;
            continue;
        }
        c.header[c.header_len++] = data[i++];
        if (c.header_len == HEADER_LEN) {
            c.remaining = (static_cast<uint32_t>(c.header[2]) << 24) | (static_cast<uint32_t>(c.header[3]) << 16) |
                          (static_cast<uint32_t>(c.header[4]) << 8) | static_cast<uint32_t>(c.header[5]);
            w.bytes(c.header, HEADER_LEN);
            c.header_len = 0;
        }
    }
    if (w.len == 0) {
        hoop_free(static_cast<uint32_t>(reinterpret_cast<uintptr_t>(w.buf)), w.cap);
        return HOOP_NONE;
    }
    return w.pack();
}

}  // extern "C"
