/*
 * The arena's lifetime rule, proven natively: a region an import returned
 * stays readable until the export returns, whatever the export allocates
 * after taking it, and the call's regions are reclaimed by the first
 * allocation after hoop_arena_reset. Built and run by test.sh with any C
 * compiler; the arena needs no wasm runtime, and the host's part is the
 * alloc/free sequence this file replays.
 */
#define HOOP_CODEC_ARENA
#define HOOP_CODEC_ARENA_BYTES 4096
#include "hoop_codec.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The log import, satisfied natively: the arena calls it before a trap. */
void hoop_log(uint32_t level, const char *message, uint32_t len) {
    fprintf(stderr, "hoop_log %u: %.*s\n", (unsigned)level, (int)len, message);
}

static int failures;

#define CHECK(cond, what)                                                       \
    do {                                                                        \
        if (!(cond)) {                                                          \
            fprintf(stderr, "arena_test: FAIL %s:%d: %s\n", __FILE__, __LINE__, what); \
            failures++;                                                         \
        }                                                                       \
    } while (0)

/* The ABI carries wasm32 addresses: hoop_alloc returns one and hoop_ptr
 * rebuilds a pointer from one. Natively those are the low 32 bits of an
 * address, of which only the arena offset survives, so the test maps them
 * back through the arena's own base. On wasm32 the mapping is the
 * identity. */
static uint8_t *at(uint32_t addr) { return hoop_arena + (uint32_t)(addr - (uint32_t)(uintptr_t)hoop_arena); }

static const uint8_t *taken_at(const uint8_t *truncated) { return at((uint32_t)(uintptr_t)truncated); }

/* An import's return: the host allocates through alloc and packs. */
static uint64_t host_import(const char *bytes) {
    uint32_t n = (uint32_t)strlen(bytes);
    uint32_t addr = hoop_alloc(n);
    memcpy(at(addr), bytes, n);
    return ((uint64_t)addr << 32) | n;
}

/* flush(conn): no host input, one import result, then an output buffer.
 * The export reads the result after allocating the output. */
static void taken_result_survives_a_later_allocation(void) {
    uint32_t n;
    const uint8_t *taken = taken_at(hoop_take(host_import("analysis"), &n));
    CHECK(n == 8, "taken length");
    uint32_t out_addr = hoop_alloc(64);
    uint8_t *out = at(out_addr);
    memset(out, 'X', 64);
    CHECK(out != taken, "the output must not reuse the taken region");
    CHECK(memcmp(taken, "analysis", 8) == 0, "the taken bytes must survive the output allocation");
    /* The host reads the output, frees it, and only then runs the next
     * export; the guest's own frees mean nothing to a bump arena. */
    hoop_arena_reset();
    hoop_free(out_addr, 64);
    CHECK(memcmp(taken, "analysis", 8) == 0, "nothing moves until the next allocation");
}

/* decode(conn): the host's input is the first allocation of the call and
 * lands at the arena's start, which proves the previous call was
 * reclaimed; the call's own regions follow it. */
static void next_call_rewinds_to_the_start(void) {
    uint32_t input_addr = hoop_alloc(32);
    uint8_t *input = at(input_addr);
    CHECK(input == hoop_arena, "the first allocation after a reset starts the arena over");
    uint32_t n;
    const uint8_t *taken = taken_at(hoop_take(host_import("split"), &n));
    CHECK(taken > input, "an import result lands behind the input");
    uint32_t scratch_addr = hoop_alloc(16);
    hoop_free(scratch_addr, 16);
    uint32_t out_addr = hoop_alloc(16);
    CHECK(at(out_addr) > at(scratch_addr), "a freed region is not handed out again inside the call");
    CHECK(memcmp(taken, "split", 5) == 0, "the taken bytes survive scratch and output");
    hoop_arena_reset();
    hoop_free(out_addr, 16);
    hoop_free(input_addr, 32);
}

/* HOOP_NONE takes as an empty string, with nothing allocated. */
static void none_takes_as_empty(void) {
    uint32_t used = hoop_arena_used;
    uint32_t n = 7;
    const uint8_t *taken = hoop_take(HOOP_NONE, &n);
    CHECK(n == 0 && taken[0] == '\0', "HOOP_NONE is empty");
    CHECK(hoop_arena_used == used, "taking HOOP_NONE allocates nothing");
    hoop_arena_reset();
}

/* Exports that forget the reset leak their call, not a region: the next
 * reset still reclaims everything. */
static void a_missed_reset_leaks_until_the_next_one(void) {
    uint8_t *first = at(hoop_alloc(16));
    CHECK(first == hoop_arena, "after a reset the arena starts over");
    /* No reset here. */
    uint8_t *second = at(hoop_alloc(16));
    CHECK(second > first, "without a reset the next call grows the arena");
    hoop_arena_reset();
    uint8_t *third = at(hoop_alloc(16));
    CHECK(third == hoop_arena, "the next reset reclaims both calls");
    hoop_arena_reset();
}

int main(void) {
    taken_result_survives_a_later_allocation();
    next_call_rewinds_to_the_start();
    none_takes_as_empty();
    a_missed_reset_leaks_until_the_next_one();
    if (failures) {
        fprintf(stderr, "arena_test: %d failure(s)\n", failures);
        return 1;
    }
    puts("arena_test: ok");
    return 0;
}
