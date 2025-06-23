package log

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap/zapcore"
)

// BaseEncoder contains the shared implementation of zapcore.Encoder methods
type BaseEncoder struct {
	storedFields map[string]interface{}
}

// NewBaseEncoder creates a new instance of the base encoder
func NewBaseEncoder() *BaseEncoder {
	return &BaseEncoder{
		storedFields: make(map[string]interface{}),
	}
}

// GetStoredFields returns the stored fields
func (b *BaseEncoder) GetStoredFields() map[string]interface{} {
	return b.storedFields
}

// SetStoredFields sets the stored fields (used in Clone)
func (b *BaseEncoder) SetStoredFields(fields map[string]interface{}) {
	b.storedFields = fields
}

// CopyStoredFields copies the stored fields to a new map
func (b *BaseEncoder) CopyStoredFields() map[string]interface{} {
	copied := make(map[string]interface{})
	for k, v := range b.storedFields {
		copied[k] = v
	}
	return copied
}

// Implementation of all Add* methods from zapcore.Encoder
func (b *BaseEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	return nil
}

func (b *BaseEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	return nil
}

func (b *BaseEncoder) AddBinary(key string, value []byte) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddByteString(key string, value []byte) {
	b.storedFields[key] = string(value)
}

func (b *BaseEncoder) AddBool(key string, value bool) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddComplex128(key string, value complex128) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddComplex64(key string, value complex64) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddDuration(key string, value time.Duration) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddFloat64(key string, value float64) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddFloat32(key string, value float32) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddInt(key string, value int) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddInt64(key string, value int64) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddInt32(key string, value int32) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddInt16(key string, value int16) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddInt8(key string, value int8) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddString(key, value string) {
	b.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: BaseEncoder.AddString called: %s = %s\n", key, value)
	}
}

func (b *BaseEncoder) AddTime(key string, value time.Time) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUint(key string, value uint) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUint64(key string, value uint64) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUint32(key string, value uint32) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUint16(key string, value uint16) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUint8(key string, value uint8) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddUintptr(key string, value uintptr) {
	b.storedFields[key] = value
}

func (b *BaseEncoder) AddReflected(key string, value interface{}) error {
	b.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: BaseEncoder.AddReflected called: %s = %v\n", key, value)
	}
	return nil
}

func (b *BaseEncoder) OpenNamespace(key string) {
	// Empty implementation - we don't do anything with namespaces
}
