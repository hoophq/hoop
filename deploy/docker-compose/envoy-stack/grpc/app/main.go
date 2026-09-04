// Command ledger is the dummy upstream behind the gRPC lane.
//
// It is a real gRPC server with no gRPC framework: demo.v1.Ledger served
// over stdlib net/http with cleartext HTTP/2. gRPC is HTTP/2 plus a 5-byte
// message framing plus grpc-status trailers, and this service is two unary
// RPCs over string fields, so hand-rolling those three layers costs less
// than a dependency. The zero-dependency build is the point: the image
// compiles offline, and the stack's claim that the SIDECAR owns protocol
// intelligence stays honest when the upstream demonstrably has none.
//
// What the demo leans on:
//   - GetInvoice echoes the request's id and note into an Invoice that
//     always carries a customer email and an SSN, so the sidecar's mask
//     rule has something to rewrite and the response comes back with
//     [REDACTED:EMAIL_ADDRESS] where ada@example.com left this process.
//   - ExportAll is the bulk endpoint a commented guardrail in
//     ../config-grpc.yaml would refuse; as shipped it answers.
//
// grpcurl and grpc-go both interoperate with it; ./demo-grpc.sh proves the
// first.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	addr := ":9000"
	if v := os.Getenv("LEDGER_LISTEN"); v != "" {
		addr = v
	}
	// Cleartext HTTP/2 with prior knowledge, which is what every gRPC
	// client sends on a plaintext dial. HTTP/1 stays on for the one
	// non-gRPC route, the /healthz the compose healthcheck curls.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{
		Addr:      addr,
		Handler:   http.HandlerFunc(handle),
		Protocols: protocols,
	}
	log.Printf("ledger listening on %s (h2c)", addr)
	log.Fatal(server.ListenAndServe())
}

func handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		fmt.Fprintln(w, "ok")
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		http.Error(w, "this port speaks gRPC; see ledger.proto", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		reply(w, nil, 13, "reading request: "+err.Error()) // INTERNAL
		return
	}

	switch r.URL.Path {
	case "/demo.v1.Ledger/GetInvoice":
		payload, status, message := framePayload(body)
		if status != 0 {
			reply(w, nil, status, message)
			return
		}
		fields, err := stringFields(payload)
		if err != nil {
			reply(w, nil, 3, "decoding GetInvoiceRequest: "+err.Error()) // INVALID_ARGUMENT
			return
		}
		id := fields[1]
		if id == "" {
			id = "INV-1001"
		}
		reply(w, invoice(id, fields[2]), 0, "")

	case "/demo.v1.Ledger/ExportAll":
		// The bulk endpoint. Nothing here is different from GetInvoice;
		// the difference the demo shows is a POLICY one, refused or not
		// by the sidecar's no-bulk-export rule, never by this server.
		reply(w, invoice("INV-1001", "1 of 3 invoices; the full export pages here"), 0, "")

	default:
		reply(w, nil, 12, "unknown method "+r.URL.Path) // UNIMPLEMENTED
	}
}

// invoice encodes a demo.v1.Invoice. The email and SSN are constants on
// purpose: the demo is about what the SIDECAR does to them on the way back,
// and a fixed value makes the rewrite visible in a diff of two runs.
func invoice(id, note string) []byte {
	var out []byte
	out = appendString(out, 1, id)
	out = appendString(out, 2, "ada@example.com")
	out = appendString(out, 3, "078-05-1120")
	out = appendString(out, 4, note)
	return out
}

// framePayload unwraps one gRPC data frame: a compression flag byte and a
// big-endian length, then the protobuf payload. Unary RPC, so exactly one
// frame; gRPC status codes name what a conforming server answers otherwise.
func framePayload(wire []byte) (payload []byte, status int, message string) {
	if len(wire) < 5 {
		return nil, 3, fmt.Sprintf("short gRPC frame: %d bytes", len(wire))
	}
	if wire[0] != 0 {
		return nil, 12, "compressed request frames are not supported"
	}
	size := int(binary.BigEndian.Uint32(wire[1:5]))
	if len(wire) < 5+size {
		return nil, 3, fmt.Sprintf("frame declares %d bytes, %d present", size, len(wire)-5)
	}
	return wire[5 : 5+size], 0, ""
}

// stringFields decodes a protobuf message whose every field is a string,
// which is all of ledger.proto. Standard wire format: varint tag, varint
// length, bytes; later occurrences of a field win, like a real decoder.
func stringFields(payload []byte) (map[int]string, error) {
	out := map[int]string{}
	for i := 0; i < len(payload); {
		tag, n := binary.Uvarint(payload[i:])
		if n <= 0 {
			return nil, fmt.Errorf("bad tag varint at byte %d", i)
		}
		i += n
		if tag&7 != 2 {
			return nil, fmt.Errorf("field %d: wire type %d, want length-delimited", tag>>3, tag&7)
		}
		size, n := binary.Uvarint(payload[i:])
		if n <= 0 || i+n+int(size) > len(payload) {
			return nil, fmt.Errorf("bad length at byte %d", i)
		}
		i += n
		out[int(tag>>3)] = string(payload[i : i+int(size)])
		i += int(size)
	}
	return out, nil
}

// appendString encodes one string field. Empty values are omitted, the
// proto3 rule. Field numbers here are all below 16, so the tag is one byte.
func appendString(dst []byte, field int, value string) []byte {
	if value == "" {
		return dst
	}
	dst = append(dst, byte(field<<3|2))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

// reply writes one gRPC response: initial headers, at most one data frame,
// and the grpc-status trailer every client blocks on. http.TrailerPrefix
// marks a header to be sent as an HTTP/2 trailer without pre-declaring it.
func reply(w http.ResponseWriter, message []byte, status int, statusMessage string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set(http.TrailerPrefix+"Grpc-Status", strconv.Itoa(status))
	if statusMessage != "" {
		w.Header().Set(http.TrailerPrefix+"Grpc-Message", statusMessage)
	}
	w.WriteHeader(http.StatusOK)
	if status == 0 {
		frame := make([]byte, 5, 5+len(message))
		binary.BigEndian.PutUint32(frame[1:], uint32(len(message)))
		_, _ = w.Write(append(frame, message...))
	}
}
