package auth

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/runopsio/hoop/agent/mysql/types"
)

type handshakePacket struct {
	protocolVersion byte
	capabilityFlags types.ClientFlag
	pluginName      string
	authData        []byte
}

type clientHandshakeResponse struct {
	clientFlags  types.ClientFlag
	maxPacket    uint32
	charset      byte
	authUser     string
	databaseName string
}

// Handshake Initialization Packet
// http://dev.mysql.com/doc/internals/en/connection-phase-packets.html#packet-Protocol::Handshake
func parseHandshakePacket(pkt *types.Packet) (*handshakePacket, error) {
	data := make([]byte, len(pkt.Frame))
	copy(data, pkt.Frame)

	// protocol version [1 byte]
	if data[0] < minProtocolVersion {
		return nil, fmt.Errorf(
			"unsupported protocol version %d. Version %d or higher is required",
			data[0],
			minProtocolVersion,
		)
	}

	// server version [null terminated string]
	// connection id [4 bytes]
	pos := 1 + bytes.IndexByte(data[1:], 0x00) + 1 + 4

	// first part of the password cipher [8 bytes]
	authData := data[pos : pos+8]

	// (filler) always 0x00 [1 byte]
	pos += 8 + 1

	// capability flags (lower 2 bytes) [2 bytes]
	capFlags1 := pkt.Frame[pos : pos+2]

	pos += 2 + 2 + 1
	capFlags2 := pkt.Frame[pos : pos+2]

	capLower := binary.LittleEndian.Uint16(capFlags1)
	capUpper := binary.LittleEndian.Uint16(capFlags2)

	cap := types.ClientFlag(uint32(capLower) | uint32(capUpper)<<16)
	// disable ssl
	cap.Unset(types.ClientSSL)

	// encode it back
	encCap := make([]byte, 4)
	binary.LittleEndian.PutUint32(encCap, uint32(cap))
	copy(capFlags1, encCap[0:2])
	copy(capFlags2, encCap[2:])

	pos += 2
	var plugin string
	if len(data) > pos {
		// length of auth-plugin-data [1 byte]
		// reserved (all [00]) [10 bytes]
		pos += 1 + 10

		// second part of the password cipher [mininum 13 bytes],
		// where len=MAX(13, length of auth-plugin-data - 8)
		//
		// The web documentation is ambiguous about the length. However,
		// according to mysql-5.7/sql/auth/sql_authentication.cc line 538,
		// the 13th byte is "\0 byte, terminating the second part of
		// a scramble". So the second part of the password cipher is
		// a NULL terminated string that's at least 13 bytes with the
		// last byte being NULL.
		//
		// The official Python library uses the fixed length 12
		// which seems to work but technically could have a hidden bug.
		authData = append(authData, data[pos:pos+12]...)
		pos += 13

		// EOF if version (>= 5.5.7 and < 5.5.10) or (>= 5.6.0 and < 5.6.2)
		// \NUL otherwise
		if end := bytes.IndexByte(data[pos:], 0x00); end != -1 {
			plugin = string(data[pos : pos+end])
		} else {
			plugin = string(data[pos:])
		}

		// make a memory safe copy of the cipher slice
		var b [20]byte
		copy(b[:], authData)
		return &handshakePacket{
			protocolVersion: data[0],
			pluginName:      plugin,
			authData:        b[:],
			capabilityFlags: cap}, nil
	}

	// make a memory safe copy of the cipher slice
	var b [8]byte
	copy(b[:], authData)
	return &handshakePacket{
		protocolVersion: data[0],
		pluginName:      plugin,
		authData:        b[:],
		capabilityFlags: cap}, nil
}

func parseClientHandshakeResponsePacket(authUser string, pkt *types.Packet) (*clientHandshakeResponse, error) {
	clientFlags := types.ClientFlag(binary.LittleEndian.Uint32(pkt.Frame[:4]))
	maxPacket := binary.LittleEndian.Uint32(pkt.Frame[4:8])
	pos := 8
	charset := pkt.Frame[pos]
	databaseName := ""

	if clientFlags.Has(types.ClientConnectWithDB) {
		pos += 23 + 1 // filler
		// username
		endPos := bytes.IndexByte(pkt.Frame[pos:], 0x00)
		if endPos == -1 {
			return nil, fmt.Errorf("username position does not match")
		}
		pos += endPos + 1

		// auth data
		pos += int(pkt.Frame[pos]) + 1

		// schema
		endPos = bytes.IndexByte(pkt.Frame[pos:], 0x00)
		if endPos == -1 {
			return nil, fmt.Errorf("schema position does not match")
		}
		databaseName = string(pkt.Frame[pos : pos+endPos])
	}
	return &clientHandshakeResponse{
		clientFlags:  clientFlags,
		maxPacket:    maxPacket,
		charset:      charset,
		authUser:     authUser,
		databaseName: databaseName,
	}, nil
}

// Client Authentication Packet
// http://dev.mysql.com/doc/internals/en/connection-phase-packets.html#packet-Protocol::HandshakeResponse
func handshakeResponsePacket(authResp []byte, plugin string, chr *clientHandshakeResponse) *types.Packet {
	// Adjust client flags based on server support
	clientFlags := chr.clientFlags
	clientFlags.Unset(types.ClientConnectAttrs)

	// encode length of the auth plugin data
	var authRespLEIBuf [9]byte
	authRespLen := len(authResp)
	authRespLEI := appendLengthEncodedInteger(authRespLEIBuf[:0], uint64(authRespLen))
	if len(authRespLEI) > 1 {
		// if the length can not be written in 1 byte, it must be written as a
		// length encoded integer
		clientFlags |= types.ClientPluginAuthLenEncClientData
	}

	pktLen := 4 + 4 + 1 + 23 + len(chr.authUser) + 1 + len(authRespLEI) + len(authResp) + 21 + 1

	// To specify a db name
	if n := len(chr.databaseName); n > 0 {
		pktLen += n + 1
	}

	data := make([]byte, pktLen+4)

	// ClientFlags [32 bit]
	data[4] = byte(clientFlags)
	data[5] = byte(clientFlags >> 8)
	data[6] = byte(clientFlags >> 16)
	data[7] = byte(clientFlags >> 24)

	binary.LittleEndian.PutUint32(data[8:12], chr.maxPacket)
	data[12] = chr.charset

	// Filler [23 bytes] (all 0x00)
	pos := 13
	for ; pos < 13+23; pos++ {
		data[pos] = 0
	}

	pos += copy(data[pos:], []byte(chr.authUser))
	data[pos] = 0x00
	pos++

	// Auth Data [length encoded integer]
	pos += copy(data[pos:], authRespLEI)
	pos += copy(data[pos:], authResp)

	// Databasename [null terminated string]
	if len(chr.databaseName) > 0 {
		pos += copy(data[pos:], []byte(chr.databaseName))
		data[pos] = 0x00
		pos++
	}

	pos += copy(data[pos:], plugin)
	data[pos] = 0x00
	return types.NewPacket(data[4:], 0)
}

func parseAuthData(authData []byte, authPassword, plugin string) ([]byte, error) {
	switch plugin {
	case "caching_sha2_password":
		authResp := scrambleSHA256Password(authData, authPassword)
		return authResp, nil

	case "mysql_native_password":
		// https://dev.mysql.com/doc/internals/en/secure-password-authentication.html
		// Native password authentication only need and will need 20-byte challenge.
		authResp := scramblePassword(authData[:20], authPassword)
		return authResp, nil

	// DEPRECATED: https://dev.mysql.com/doc/refman/8.0/en/sha256-pluggable-authentication.html
	// case "sha256_password":
	// DEPRECATED: https://dev.mysql.com/doc/refman/5.7/en/old-native-pluggable-authentication.html
	// case "mysql_old_password":
	// Could be a potential security problem in some configurations
	// case "mysql_clear_password":
	// 	// http://dev.mysql.com/doc/refman/5.7/en/cleartext-authentication-plugin.html
	// 	// http://dev.mysql.com/doc/refman/5.7/en/pam-authentication-plugin.html
	// 	return append([]byte(authPassword), 0), nil
	default:
		return nil, fmt.Errorf("authentication plugin %v not supported", plugin)
	}
}

// encodes a uint64 value and appends it to the given bytes slice
func appendLengthEncodedInteger(b []byte, n uint64) []byte {
	switch {
	case n <= 250:
		return append(b, byte(n))

	case n <= 0xffff:
		return append(b, 0xfc, byte(n), byte(n>>8))

	case n <= 0xffffff:
		return append(b, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	}
	return append(b, 0xfe, byte(n), byte(n>>8), byte(n>>16), byte(n>>24),
		byte(n>>32), byte(n>>40), byte(n>>48), byte(n>>56))
}
