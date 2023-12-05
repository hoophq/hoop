package pgproxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/agent/dlp"
	pgproxydlp "github.com/runopsio/hoop/agent/pgproxy/dlp"
	"github.com/runopsio/hoop/common/log"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	"github.com/runopsio/hoop/common/proto"
)

const (
	defaultRequestTimeoutInSec = 5
	defaultMaskingCharacter    = "*"
	defaultNumberToMask        = 5
	// this values needs to be low to avoid the max limit of findings per chunk
	// https://cloud.google.com/dlp/docs/deidentify-sensitive-data#findings-limit
	defaultMaxChunkSize = 62500
	maxFindings         = 2900
	maxInfoTypes        = 30
)

var errDLPNoop = errors.New("dlp: no operation")

type dlpHandler struct {
	dlpClient       dlp.Client
	clientW         io.Writer
	dataRowPackets  *bytes.Buffer
	typedPackets    *bytes.Buffer
	infoTypes       []string
	maxRows         int
	maxPacketLength int
	rowCount        int
}

func newDlpHandler(dlpClient dlp.Client, clientW io.Writer, infoTypes []string) *dlpHandler {
	return &dlpHandler{
		dlpClient:       dlpClient,
		clientW:         clientW,
		dataRowPackets:  bytes.NewBuffer([]byte{}),
		typedPackets:    bytes.NewBuffer([]byte{}),
		infoTypes:       infoTypes,
		maxPacketLength: 100000, // 0.10MB
	}
}

func (h *dlpHandler) handle(pkt *pgtypes.Packet) error {
	if h.dlpClient == nil || len(h.infoTypes) == 0 {
		return errDLPNoop
	}
	pktLength := h.dataRowPackets.Len()
	// the first data row packet initializes the logic of
	// buffering pakets and redacting it.
	if pkt.Type() == pgtypes.ServerDataRow && pktLength == 0 {
		_, _ = h.dataRowPackets.Write(pkt.Encode())
		// this calculation gives a safe amount
		// of rows that could be redacted per request
		maxRows := maxFindings / binary.BigEndian.Uint16(pkt.Frame()[:2])
		h.maxRows = int(math.RoundToEven(float64(maxRows)))
		return nil
	}

	// it's not a data row packet
	if pktLength < 1 {
		return errDLPNoop
	}

	if pkt.Type() == pgtypes.ServerDataRow {
		_, _ = h.dataRowPackets.Write(pkt.Encode())
		h.rowCount++
	} else {
		_, _ = h.typedPackets.Write(pkt.Encode())
	}
	if pktLength > h.maxPacketLength || h.rowCount >= h.maxRows {
		log.Infof("redact and write, buffersize=%v, rows=%v/%v", pktLength, h.rowCount, h.maxRows)
		h.redactAndWrite()
		return nil
	}
	// assuming that a DataRow starts the buffering
	// and a server ready for query packet ends it.
	if pkt.Type() == pgtypes.ServerReadyForQuery {
		log.Infof("redact and write, rows=%v/%v", h.rowCount, h.maxRows)
		h.redactAndWrite()
	}
	return nil
}

func (h *dlpHandler) redactAndWrite() {
	defer func() { h.dataRowPackets.Reset(); h.typedPackets.Reset(); h.rowCount = 0 }()
	redactedChunk, err := pgproxydlp.RedactDataRow(
		h.dlpClient,
		dlp.NewDeidentifyConfig("#", defaultNumberToMask, h.dlpClient.ProjectID(), h.infoTypes),
		h.dataRowPackets)

	if err != nil {
		errMsg := fmt.Errorf("failed redacting data row packets, err=%v", err)
		log.Info(errMsg)
		sentry.CaptureException(errMsg)
	}
	chunkData := redactedChunk.Data()
	if _, err := chunkData.Write(h.typedPackets.Bytes()); err != nil {
		errMsg := fmt.Errorf("failed generating packet buffer, err=%v", err)
		log.Info(errMsg)
		sentry.CaptureException(errMsg)
	}

	switch v := h.clientW.(type) {
	case proto.WriterWithSummary:
		_, err = v.WriteWithSummary(chunkData.Bytes(), redactedChunk.TransformationSummary())
	default:
		_, err = v.Write(chunkData.Bytes())
	}
	if err != nil {
		errMsg := fmt.Errorf("failed writing packet to response writer, err=%v", err)
		log.Info(errMsg)
		sentry.CaptureException(errMsg)
	}
}
