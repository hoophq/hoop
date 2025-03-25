package sshtypes

import (
	"fmt"
	"io"
)

type OpenChannel struct {
	ChannelID        uint16
	ChannelType      string
	ChannelExtraData []byte
}

type SSHRequest struct {
	ChannelID   uint16
	RequestType string
	WantReply   bool
	Payload     []byte
}

type Data struct {
	ChannelID uint16
	Payload   []byte
}

type CloseChannel struct {
	ID   uint16
	Type string
}

type Encoder interface {
	Encode() []byte
}

func (o OpenChannel) Encode() []byte {
	return []byte(``)
}

func (o SSHRequest) Encode() []byte {
	return []byte(``)
}

func (o Data) Encode() []byte {
	return []byte(``)
}

func (o CloseChannel) Encode() []byte {
	return []byte(``)
}

type PacketType byte

const (
	OpenChannelType PacketType = iota + 1
	SSHRequestType
	DataType
	CloseChannelType
)

func (p PacketType) Byte() byte { return byte(p) }

func DecodeType(data []byte) PacketType {
	return 0x00
}

func Decode(data []byte, into any) error {
	return fmt.Errorf("libhoop: not implemented")
}

func NewDataWriter(w io.Writer, channelID uint16) io.Writer {
	return &DataWriter{}
}

type DataWriter struct{}

func (w *DataWriter) Write(b []byte) (n int, err error) {
	return 0, fmt.Errorf("libhoop: not implemented")
}
