package main

// gRPC server reflection, hand-rolled like the rest of this server, so the
// discover demo (../docker-compose.discover.yml) has an upstream a lane can
// fetch its schema from. Off unless LEDGER_REFLECTION names the descriptor
// sets to serve: the other demos keep an upstream with no reflection.
//
// It answers the two requests a schema fetch sends (list_services and
// file_containing_symbol), on both the v1 and the v1alpha path; the
// messages are wire-identical. Anything else gets an error_response.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	reflectionV1      = "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo"
	reflectionV1Alpha = "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
)

// reflectionIndex maps each service's full name to the files of the
// descriptor set that defines it, imports included (--include_imports).
type reflectionIndex struct {
	services []string
	files    map[string][][]byte
}

// loadReflection reads the comma-separated FileDescriptorSet paths in
// LEDGER_REFLECTION. Nil when the variable is empty.
func loadReflection() (*reflectionIndex, error) {
	raw := os.Getenv("LEDGER_REFLECTION")
	if raw == "" {
		return nil, nil
	}
	idx := &reflectionIndex{files: map[string][][]byte{}}
	for _, path := range strings.Split(raw, ",") {
		set, err := os.ReadFile(strings.TrimSpace(path))
		if err != nil {
			return nil, err
		}
		// FileDescriptorSet: repeated FileDescriptorProto file = 1.
		files, err := repeatedBytes(set, 1)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, file := range files {
			fields, err := protoFields(file)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			// FileDescriptorProto: package = 2, service = 6 { name = 1 }.
			pkg := ""
			for _, f := range fields {
				if f.num == 2 {
					pkg = string(f.data)
				}
			}
			for _, f := range fields {
				if f.num != 6 {
					continue
				}
				svc, err := protoFields(f.data)
				if err != nil {
					return nil, err
				}
				for _, s := range svc {
					if s.num == 1 {
						name := string(s.data)
						if pkg != "" {
							name = pkg + "." + name
						}
						idx.services = append(idx.services, name)
						idx.files[name] = files
					}
				}
			}
		}
	}
	sort.Strings(idx.services)
	return idx, nil
}

// serve answers one ServerReflectionInfo stream: one response per request
// message, until the client closes its side.
func (idx *reflectionIndex) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/grpc")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	status, message := "0", ""
	for {
		req, err := readFrame(r.Body)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			status, message = "3", err.Error() // INVALID_ARGUMENT
			break
		}
		if _, err := w.Write(frame(idx.answer(req))); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	w.Header().Set(http.TrailerPrefix+"Grpc-Status", status)
	if message != "" {
		w.Header().Set(http.TrailerPrefix+"Grpc-Message", message)
	}
}

// answer builds a ServerReflectionResponse: original_request = 2,
// file_descriptor_response = 4, list_services_response = 6,
// error_response = 7.
func (idx *reflectionIndex) answer(req []byte) []byte {
	out := appendBytes(nil, 2, req)
	fields, err := protoFields(req)
	if err != nil {
		return appendBytes(out, 7, reflectionError(3, err.Error()))
	}
	for _, f := range fields {
		switch f.num {
		case 7: // list_services
			var list []byte
			for _, name := range idx.services {
				list = appendBytes(list, 1, appendString(nil, 1, name))
			}
			return appendBytes(out, 6, list)
		case 4: // file_containing_symbol
			files, ok := idx.files[string(f.data)]
			if !ok {
				return appendBytes(out, 7, reflectionError(5, "symbol not found: "+string(f.data)))
			}
			var resp []byte
			for _, file := range files {
				resp = appendBytes(resp, 1, file)
			}
			return appendBytes(out, 4, resp)
		}
	}
	return appendBytes(out, 7, reflectionError(12, "this demo server answers list_services and file_containing_symbol only"))
}

// reflectionError is an ErrorResponse: error_code = 1, error_message = 2.
func reflectionError(code int, message string) []byte {
	out := []byte{1 << 3}
	out = binary.AppendUvarint(out, uint64(code))
	return appendString(out, 2, message)
}

// readFrame reads one gRPC message off a stream: a compression byte, a
// big-endian length, the payload. io.EOF only at a frame boundary.
func readFrame(body io.Reader) ([]byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(body, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errors.New("truncated gRPC frame header")
		}
		return nil, err
	}
	if hdr[0] != 0 {
		return nil, errors.New("compressed request frames are not supported")
	}
	msg := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
	if _, err := io.ReadFull(body, msg); err != nil {
		return nil, errors.New("truncated gRPC frame")
	}
	return msg, nil
}

func frame(message []byte) []byte {
	out := make([]byte, 5, 5+len(message))
	binary.BigEndian.PutUint32(out[1:], uint32(len(message)))
	return append(out, message...)
}

// appendBytes encodes one length-delimited field, empty or not: an empty
// list_services_response still has to say which response it is.
func appendBytes(dst []byte, field int, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(field<<3|2))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

type protoField struct {
	num  int
	data []byte // the bytes of a length-delimited field; nil otherwise
}

// protoFields walks one message, keeping length-delimited fields and
// skipping varints and fixed-width ones, which nothing here reads.
func protoFields(msg []byte) ([]protoField, error) {
	var out []protoField
	for i := 0; i < len(msg); {
		tag, n := binary.Uvarint(msg[i:])
		if n <= 0 {
			return nil, fmt.Errorf("bad tag varint at byte %d", i)
		}
		i += n
		switch tag & 7 {
		case 0:
			_, n := binary.Uvarint(msg[i:])
			if n <= 0 {
				return nil, fmt.Errorf("bad varint at byte %d", i)
			}
			i += n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			size, n := binary.Uvarint(msg[i:])
			if n <= 0 || i+n+int(size) > len(msg) {
				return nil, fmt.Errorf("bad length at byte %d", i)
			}
			i += n
			out = append(out, protoField{num: int(tag >> 3), data: msg[i : i+int(size)]})
			i += int(size)
		default:
			return nil, fmt.Errorf("unsupported wire type %d", tag&7)
		}
		if i > len(msg) {
			return nil, errors.New("truncated message")
		}
	}
	return out, nil
}

func repeatedBytes(msg []byte, field int) ([][]byte, error) {
	fields, err := protoFields(msg)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, f := range fields {
		if f.num == field {
			out = append(out, f.data)
		}
	}
	return out, nil
}
