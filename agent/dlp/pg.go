package dlp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	"cloud.google.com/go/dlp/apiv2/dlppb"
	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/agent/pg"
	pgtypes "github.com/runopsio/hoop/common/pg"
)

func NewRedactMiddleware(c Client, infoTypes ...string) (*redactPostgresMiddleware, error) {
	if len(infoTypes) > maxInfoTypes {
		return nil, fmt.Errorf("max (%v) info types reached", maxInfoTypes)
	}
	return &redactPostgresMiddleware{
		dlpClient:       c,
		infoTypes:       parseInfoTypes(infoTypes),
		dataRowPackets:  bytes.NewBuffer([]byte{}),
		typedPackets:    bytes.NewBuffer([]byte{}),
		maxPacketLength: 100000, // 0.10MB
	}, nil
}

func (m *redactPostgresMiddleware) Handler(next pg.NextFn, pkt *pg.Packet, w pg.ResponseWriter) {
	if m.dlpClient == nil || len(m.infoTypes) == 0 {
		next()
		return
	}
	pktLength := m.dataRowPackets.Len()
	// the first data row packet initializes the logic of
	// buffering pakets and redacting it.
	if pkt.Type() == pgtypes.ServerDataRow && pktLength == 0 {
		_, _ = m.dataRowPackets.Write(pkt.Encode())
		// this calculation gives a safe amount
		// of rows that could be redacted per request
		maxRows := maxFindings / binary.BigEndian.Uint16(pkt.Frame()[:2])
		m.maxRows = int(math.RoundToEven(float64(maxRows)))
		return
	}

	// it's not a data row packet, move to the next handler
	if pktLength < 1 {
		next()
		return
	}

	if pkt.Type() == pgtypes.ServerDataRow {
		_, _ = m.dataRowPackets.Write(pkt.Encode())
		m.rowCount++
	} else {
		_, _ = m.typedPackets.Write(pkt.Encode())
	}
	if pktLength > m.maxPacketLength || m.rowCount >= m.maxRows {
		log.Printf("redact and write, buffersize=%v, rows=%v/%v", pktLength, m.rowCount, m.maxRows)
		m.redactAndWrite(w)
		return
	}
	// assuming that a DataRow starts the buffering
	// and a server ready for query packet ends it.
	if pkt.Type() == pgtypes.ServerReadyForQuery {
		log.Printf("redact and write, rows=%v/%v", m.rowCount, m.maxRows)
		m.redactAndWrite(w)
	}
}

func (m *redactPostgresMiddleware) redactAndWrite(w pg.ResponseWriter) {
	defer func() { m.dataRowPackets.Reset(); m.typedPackets.Reset(); m.rowCount = 0 }()
	redactedDataRows, err := redactDataRow(m.dlpClient, &deidentifyConfig{
		maskingCharacter: "#",
		numberToMask:     defaultNumberToMask,
		infoTypes:        m.infoTypes,
		projectID:        m.dlpClient.ProjectID(),
	}, m.dataRowPackets)
	if err != nil {
		errMsg := fmt.Errorf("failed redacting data row packets, err=%v", err)
		log.Println(errMsg)
		sentry.CaptureException(errMsg)
	}
	if _, err := redactedDataRows.Write(m.typedPackets.Bytes()); err != nil {
		errMsg := fmt.Errorf("failed generating packet buffer, err=%v", err)
		log.Println(errMsg)
		sentry.CaptureException(errMsg)
	}
	if _, err = w.Write(redactedDataRows.Bytes()); err != nil {
		errMsg := fmt.Errorf("failed writing packet to response writer, err=%v", err)
		log.Println(errMsg)
		sentry.CaptureException(errMsg)
	}
}

func encodeToDataRow(table *dlppb.Table) *bytes.Buffer {
	var dataRowPackets []byte
	for _, row := range table.Rows {
		var dataRowValues []string
		for _, val := range row.Values {
			dataRowValues = append(dataRowValues, val.GetStringValue())
		}
		pktBytes := pg.NewDataRowPacket(uint16(len(row.Values)), dataRowValues...).Encode()
		dataRowPackets = append(dataRowPackets, pktBytes...)
	}
	return bytes.NewBuffer(dataRowPackets)
}

func redactDataRow(dlpclient Client, conf *deidentifyConfig, dataRows *bytes.Buffer) (*bytes.Buffer, error) {
	// don't redact too small packets
	if dataRows.Len() < 15 {
		return dataRows, nil
	}
	tableInput := &dlppb.Table{
		Headers: []*dlppb.FieldId{},
		Rows:    []*dlppb.Table_Row{},
	}
	dataRowsCopy := bytes.NewBuffer(dataRows.Bytes())
	defer dataRowsCopy.Reset()
	for n := 1; ; n++ {
		_, dataRowPkt, err := pg.DecodeTypedPacket(dataRowsCopy)
		if err != nil {
			if err == io.EOF {
				break
			}
			return dataRows, err
		}
		if dataRowPkt.Type() != pgtypes.ServerDataRow {
			return dataRows, fmt.Errorf("expected data row packet, got=%v", string(dataRowPkt.Type()))
		}
		dataRowBuf := bytes.NewBuffer(dataRowPkt.Frame())
		columnNumbers := binary.BigEndian.Uint16(dataRowBuf.Next(2))
		tableRowValues := []*dlppb.Value{}
		// iterate in each row field
		for i := 1; i <= int(columnNumbers); i++ {
			columnLength := binary.BigEndian.Uint32(dataRowBuf.Next(4))
			// -1 means it's a NULL value
			if columnLength == pgtypes.ServerDataRowNull {
				columnLength = 0
			}
			columnData := make([]byte, columnLength)
			_, err := io.ReadFull(dataRowBuf, columnData[:])
			if err != nil {
				return dataRows, fmt.Errorf("failed reading column (idx=%v,len=%v), err=%v", i, columnLength, err)
			}
			// must append it only once
			if len(tableInput.Headers) < int(columnNumbers) {
				tableInput.Headers = append(tableInput.Headers, &dlppb.FieldId{Name: fmt.Sprintf("%v", i)})
			}
			tableRowValues = append(tableRowValues,
				&dlppb.Value{Type: &dlppb.Value_StringValue{StringValue: string(columnData)}})
		}
		tableInput.Rows = append(tableInput.Rows, &dlppb.Table_Row{Values: tableRowValues})
	}
	chunkCh := make(chan *Chunk)
	go func() {
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*defaultRequestTimeoutInSec)
		defer cancelFn()
		redactedChunk := dlpclient.DeidentifyContent(ctx, conf, 0, newTableInputData(tableInput))
		chunkCh <- redactedChunk
	}()
	var redactedChunk *Chunk
	for c := range chunkCh {
		redactedChunk = c
		close(chunkCh)
		break
	}
	if redactedChunk.transformationSummary.err != nil {
		return dataRows, redactedChunk.transformationSummary.err
	}
	return redactedChunk.data, nil
}
