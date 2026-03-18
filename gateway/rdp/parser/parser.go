// Package parser provides a Go interface to the RDP bitmap parser WASM module.
// It uses wazero to run the WASM module and parse RDP output data to extract bitmap updates.
//
//go:generate go run generate.go
package parser

import (
	"context"
	_ "embed"

	"github.com/hoophq/hoop/common/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed rdp_parser.wasm
var wasmBinary []byte

// BitmapRect represents a bitmap rectangle extracted from RDP stream
type BitmapRect struct {
	X            uint16 // Left position
	Y            uint16 // Top position
	Right        uint16 // Right edge
	Bottom       uint16 // Bottom edge
	Width        uint16 // Width of the rectangle
	Height       uint16 // Height of the rectangle
	BitsPerPixel uint16 // Bits per pixel
	DataOffset   uint32 // Offset of bitmap data in original input
	DataLen      uint32 // Length of bitmap data
	Compressed   bool   // True if data is RLE compressed
}

// Parser wraps the WASM RDP parser
type Parser struct {
	ctx     context.Context
	runtime wazero.Runtime
	module  api.Module

	// Exported functions
	fnParserVersion  api.Function
	fnParseRdpOutput api.Function
	fnGetBitmapCount api.Function
	fnGetBitmap      api.Function
	fnGetBitmapData  api.Function
	fnGetError       api.Function
	fnGetErrorLen    api.Function
	fnClearParsed    api.Function
	fnGetPduSize     api.Function
	fnAllocate       api.Function
	fnDeallocate     api.Function

	// Memory
	memory api.Memory
}

// ParseResult contains the result of parsing RDP output
type ParseResult struct {
	Bitmaps []BitmapRect
	Error   string
}

func logString(ctx context.Context, m api.Module, level, offset, byteCount uint32) {
	buf, ok := m.Memory().Read(offset, byteCount)
	if !ok {
		log.Errorf("[WASM] Memory.Read(%d, %d) out of range", offset, byteCount)
		return
	}
	switch level {
	case 1:
		log.Errorf("[WASM] %s", string(buf))
	case 2:
		log.Warnf("[WASM] %s", string(buf))
	case 3:
		log.Infof("[WASM] %s", string(buf))
	case 4:
		log.Debugf("[WASM] %s", string(buf))
	}
}

// NewParser creates a new RDP parser instance
func NewParser(ctx context.Context) (*Parser, error) {
	// Create a new runtime
	runtime := wazero.NewRuntime(ctx)
	// Instantiate a Go-defined module named "env" that exports a function to
	// log to the console.
	_, err := runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(logString).Export("log").
		Instantiate(ctx)
	if err != nil {
		runtime.Close(ctx)
		return nil, err
	}

	// Instantiate the WASM module
	module, err := runtime.Instantiate(ctx, wasmBinary)
	if err != nil {
		runtime.Close(ctx)
		return nil, err
	}

	// Get exported functions
	initModule := module.ExportedFunction("_initialize")
	fnParserVersion := module.ExportedFunction("parser_version")
	fnParseRdpOutput := module.ExportedFunction("parse_rdp_output")
	fnGetBitmapCount := module.ExportedFunction("get_bitmap_count")
	fnGetBitmap := module.ExportedFunction("get_bitmap")
	fnGetBitmapData := module.ExportedFunction("get_bitmap_data")
	fnGetError := module.ExportedFunction("get_error")
	fnGetErrorLen := module.ExportedFunction("get_error_len")
	fnClearParsed := module.ExportedFunction("clear_parsed")
	fnGetPduSize := module.ExportedFunction("get_pdu_size")
	allocate := module.ExportedFunction("allocate")
	deallocate := module.ExportedFunction("deallocate")

	if fnParserVersion == nil || fnParseRdpOutput == nil || fnGetBitmapCount == nil ||
		fnGetBitmap == nil || fnGetBitmapData == nil || fnGetError == nil || fnClearParsed == nil ||
		fnGetPduSize == nil || allocate == nil || deallocate == nil {
		module.Close(ctx)
		runtime.Close(ctx)
		return nil, err
	}

	// Get memory
	memory := module.ExportedMemory("memory")
	if memory == nil {
		module.Close(ctx)
		runtime.Close(ctx)
		return nil, err
	}

	// Call initModule
	_, err = initModule.Call(ctx)
	if err != nil {
		module.Close(ctx)
		runtime.Close(ctx)
		return nil, err
	}

	return &Parser{
		ctx:              ctx,
		runtime:          runtime,
		module:           module,
		fnParserVersion:  fnParserVersion,
		fnParseRdpOutput: fnParseRdpOutput,
		fnGetBitmapCount: fnGetBitmapCount,
		fnGetBitmap:      fnGetBitmap,
		fnGetBitmapData:  fnGetBitmapData,
		fnGetError:       fnGetError,
		fnGetErrorLen:    fnGetErrorLen,
		fnClearParsed:    fnClearParsed,
		fnGetPduSize:     fnGetPduSize,
		fnAllocate:       allocate,
		fnDeallocate:     deallocate,
		memory:           memory,
	}, nil
}

// Close releases the parser resources
func (p *Parser) Close() error {
	return p.runtime.Close(p.ctx)
}

// Version returns the parser version
func (p *Parser) Version() (uint32, error) {
	results, err := p.fnParserVersion.Call(p.ctx)
	if err != nil {
		return 0, err
	}
	return uint32(results[0]), nil
}

// GetPduSize attempts to determine the size of a complete PDU in the buffer.
// Returns 0 if not enough data to determine size, or the PDU size if complete.
func (p *Parser) GetPduSize(data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}

	dataSize := uint64(len(data))

	// Allocate memory in WASM for the input data
	results, err := p.fnAllocate.Call(p.ctx, dataSize)
	if err != nil {
		return 0, err
	}
	dataPtr := uint32(results[0])
	defer p.fnDeallocate.Call(p.ctx, uint64(dataPtr), dataSize)

	// Write data to the allocated WASM memory
	if !p.memory.Write(dataPtr, data) {
		return 0, err
	}

	// Call get_pdu_size
	results, err = p.fnGetPduSize.Call(p.ctx, uint64(dataPtr), dataSize)
	if err != nil {
		return 0, err
	}

	return uint32(results[0]), nil
}

// Parse parses RDP output data and extracts bitmap updates
func (p *Parser) Parse(data []byte) (*ParseResult, error) {
	if len(data) == 0 {
		return &ParseResult{Bitmaps: nil}, nil
	}

	// Clear previous parsed data
	p.fnClearParsed.Call(p.ctx)

	dataSize := uint64(len(data))

	// Allocate memory in WASM for the input data
	results, err := p.fnAllocate.Call(p.ctx, dataSize)
	if err != nil {
		return nil, err
	}
	dataPtr := uint32(results[0])
	// Deallocate when done
	defer p.fnDeallocate.Call(p.ctx, uint64(dataPtr), dataSize)

	// Write data to the allocated WASM memory
	if !p.memory.Write(dataPtr, data) {
		return nil, err
	}

	// Call parse_rdp_output
	results, err = p.fnParseRdpOutput.Call(p.ctx, uint64(dataPtr), dataSize)
	if err != nil {
		return nil, err
	}

	resultCode := int32(results[0])

	// Check for error (negative return)
	if resultCode < 0 {
		errorMsg := p.getError()
		return &ParseResult{Error: errorMsg}, nil
	}

	// Get bitmap count
	bitmapCount, err := p.getBitmapCount()
	if err != nil {
		return nil, err
	}

	if bitmapCount == 0 {
		return &ParseResult{Bitmaps: nil}, nil
	}

	// Extract bitmaps
	bitmaps := make([]BitmapRect, bitmapCount)
	for i := uint32(0); i < bitmapCount; i++ {
		bmp, err := p.getBitmap(i)
		if err != nil {
			return nil, err
		}
		bitmaps[i] = bmp
	}

	return &ParseResult{Bitmaps: bitmaps}, nil
}

// getBitmapCount returns the number of parsed bitmaps
func (p *Parser) getBitmapCount() (uint32, error) {
	results, err := p.fnGetBitmapCount.Call(p.ctx)
	if err != nil {
		return 0, err
	}
	return uint32(results[0]), nil
}

// getBitmap returns a bitmap by index
func (p *Parser) getBitmap(index uint32) (BitmapRect, error) {
	results, err := p.fnGetBitmap.Call(p.ctx, uint64(index))
	if err != nil {
		return BitmapRect{}, err
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return BitmapRect{}, err
	}

	// Read BitmapRect struct from memory (26 bytes)
	const bitmapSize = 26
	buf, ok := p.memory.Read(ptr, bitmapSize)
	if !ok {
		return BitmapRect{}, err
	}

	compressed := uint16(buf[22]) | uint16(buf[23])<<8

	bmp := BitmapRect{
		X:            uint16(buf[0]) | uint16(buf[1])<<8,
		Y:            uint16(buf[2]) | uint16(buf[3])<<8,
		Right:        uint16(buf[4]) | uint16(buf[5])<<8,
		Bottom:       uint16(buf[6]) | uint16(buf[7])<<8,
		Width:        uint16(buf[8]) | uint16(buf[9])<<8,
		Height:       uint16(buf[10]) | uint16(buf[11])<<8,
		BitsPerPixel: uint16(buf[12]) | uint16(buf[13])<<8,
		DataOffset:   uint32(buf[14]) | uint32(buf[15])<<8 | uint32(buf[16])<<16 | uint32(buf[17])<<24,
		DataLen:      uint32(buf[18]) | uint32(buf[19])<<8 | uint32(buf[20])<<16 | uint32(buf[21])<<24,
		Compressed:   compressed != 0,
	}

	return bmp, nil
}

// getError returns the error message from WASM
func (p *Parser) getError() string {
	results, err := p.fnGetErrorLen.Call(p.ctx)
	if err != nil {
		return ""
	}
	length := uint32(results[0])
	if length == 0 {
		return ""
	}

	results, err = p.fnGetError.Call(p.ctx)
	if err != nil {
		return ""
	}
	ptr := uint32(results[0])
	if ptr == 0 {
		return ""
	}

	buf, ok := p.memory.Read(ptr, length)
	if !ok {
		return ""
	}

	// Remove null terminator if present
	if len(buf) > 0 && buf[len(buf)-1] == 0 {
		buf = buf[:len(buf)-1]
	}
	return string(buf)
}

// GetBitmapData returns the bitmap data for a given BitmapRect
// The data is read from the WASM module's internal storage
func (p *Parser) GetBitmapData(bmp BitmapRect) []byte {
	if bmp.DataOffset == 0 && bmp.DataLen == 0 {
		return nil
	}

	results, err := p.fnGetBitmapData.Call(p.ctx, uint64(bmp.DataOffset), uint64(bmp.DataLen))
	if err != nil {
		return nil
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return nil
	}

	buf, ok := p.memory.Read(ptr, bmp.DataLen)
	if !ok {
		return nil
	}

	// Copy the data since it's in WASM memory
	data := make([]byte, bmp.DataLen)
	copy(data, buf)
	return data
}