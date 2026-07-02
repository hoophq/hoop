package rdp

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/hoophq/hoop/gateway/rdp/parser"
)

// TestParserInstancesAreIsolatedAcrossConcurrentSessions is the DEP-47
// regression test. The WASM parser keeps per-instance mutable state (parsed
// bitmaps, bump allocator and Fast-Path fragment reassembly); the session
// recorder used to share ONE global instance across every concurrent RDP
// session, so interleaved sessions corrupted each other's state — leading to
// out-of-bounds reads and intermittent gateway crashes.
//
// This test runs many concurrent "sessions", each with its own parser
// instance, interleaving complete and fragmented Fast-Path bitmap PDUs, and
// verifies every session decodes exactly its own bitmaps. Run with -race.
func TestParserInstancesAreIsolatedAcrossConcurrentSessions(t *testing.T) {
	const sessions = 8
	const rounds = 25

	// Distinct per-session rect geometry so cross-instance state bleed would
	// surface as wrong coordinates/dimensions, not just as a race report.
	pduFor := func(session int) []byte {
		return fastPathBitmapPDU(t, testRect{
			x: 8 * session, y: 4 * session, w: 8 + session, h: 8, bgr: white,
		})
	}
	fragmentsFor := func(session int) [][]byte {
		return fragmentedFastPathBitmapPDU(t, 3, testRect{
			x: 8*session + 1, y: 4*session + 2, w: 8 + session, h: 8, bgr: magenta,
		})
	}

	var wg sync.WaitGroup
	errCh := make(chan error, sessions)
	for s := 0; s < sessions; s++ {
		wg.Add(1)
		go func(session int) {
			defer wg.Done()

			p, err := parser.NewParser(context.Background())
			if err != nil {
				errCh <- fmt.Errorf("session %d: NewParser: %w", session, err)
				return
			}
			defer p.Close()

			complete := pduFor(session)
			frags := fragmentsFor(session)

			for r := 0; r < rounds; r++ {
				// Complete PDU: must yield exactly one bitmap with this
				// session's geometry.
				res, err := p.Parse(complete)
				if err != nil {
					errCh <- fmt.Errorf("session %d round %d: parse: %w", session, r, err)
					return
				}
				if len(res.Bitmaps) != 1 {
					errCh <- fmt.Errorf("session %d round %d: got %d bitmaps, want 1", session, r, len(res.Bitmaps))
					return
				}
				bmp := res.Bitmaps[0]
				if int(bmp.X) != 8*session || int(bmp.Y) != 4*session || int(bmp.Width) != 8+session {
					errCh <- fmt.Errorf("session %d round %d: got rect (%d,%d %dx%d), want (%d,%d %dx8) — cross-session state bleed",
						session, r, bmp.X, bmp.Y, bmp.Width, bmp.Height, 8*session, 4*session, 8+session)
					return
				}
				if data := p.GetBitmapData(bmp); len(data) != int(bmp.DataLen) || len(data) == 0 {
					errCh <- fmt.Errorf("session %d round %d: bitmap data len %d, want %d", session, r, len(data), bmp.DataLen)
					return
				}

				// Fragmented PDU: reassembly state is per-instance; the final
				// fragment must yield this session's rect. Feed fragments one
				// by one, as the wire delivers them.
				for i, frag := range frags {
					res, err = p.Parse(frag)
					if err != nil {
						errCh <- fmt.Errorf("session %d round %d: parse frag %d: %w", session, r, i, err)
						return
					}
					if i < len(frags)-1 && len(res.Bitmaps) != 0 {
						errCh <- fmt.Errorf("session %d round %d: frag %d yielded %d bitmaps before Last", session, r, i, len(res.Bitmaps))
						return
					}
				}
				if len(res.Bitmaps) != 1 {
					errCh <- fmt.Errorf("session %d round %d: reassembly yielded %d bitmaps, want 1", session, r, len(res.Bitmaps))
					return
				}
				bmp = res.Bitmaps[0]
				if int(bmp.X) != 8*session+1 || int(bmp.Y) != 4*session+2 {
					errCh <- fmt.Errorf("session %d round %d: reassembled rect (%d,%d), want (%d,%d) — fragment state bleed",
						session, r, bmp.X, bmp.Y, 8*session+1, 4*session+2)
					return
				}
			}
			errCh <- nil
		}(s)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestParserGetPduSizeConcurrent exercises the framing helper concurrently on
// isolated instances — the recorder calls GetPduSize on every server->client
// chunk, so this is the hottest path of the old shared-instance race.
func TestParserGetPduSizeConcurrent(t *testing.T) {
	const sessions = 8
	const rounds = 200

	var wg sync.WaitGroup
	errCh := make(chan error, sessions)
	for s := 0; s < sessions; s++ {
		wg.Add(1)
		go func(session int) {
			defer wg.Done()
			p, err := parser.NewParser(context.Background())
			if err != nil {
				errCh <- fmt.Errorf("session %d: NewParser: %w", session, err)
				return
			}
			defer p.Close()

			pdu := fastPathBitmapPDU(t, testRect{x: session, y: session, w: 8, h: 8, bgr: white})
			for r := 0; r < rounds; r++ {
				size, err := p.GetPduSize(pdu)
				if err != nil {
					errCh <- fmt.Errorf("session %d round %d: GetPduSize: %w", session, r, err)
					return
				}
				if int(size) != len(pdu) {
					errCh <- fmt.Errorf("session %d round %d: GetPduSize=%d, want %d", session, r, size, len(pdu))
					return
				}
			}
			errCh <- nil
		}(s)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}
