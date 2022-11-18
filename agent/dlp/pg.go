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
	"github.com/runopsio/hoop/agent/pg"
	pgtypes "github.com/runopsio/hoop/common/pg"
)

func NewRedactMiddleware(c Client, infoTypes ...string) (*redactPostgresMiddleware, error) {
	if len(infoTypes) > maxInfoTypes {
		return nil, fmt.Errorf("max (%v) info types reached", maxInfoTypes)
	}
	return &redactPostgresMiddleware{
		dlpClient:      c,
		infoTypes:      parseInfoTypes(infoTypes),
		dataRowPackets: bytes.NewBuffer([]byte{}),
		typedPackets:   bytes.NewBuffer([]byte{}),
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
	// if the pkt length is above 0.10MB or the amount of rows reached its max
	if pktLength > 100000 || m.rowCount >= m.maxRows {
		log.Printf("redact and write, buffersize=%v, rows=%v/%v", pktLength, m.maxRows, m.rowCount)
		m.redactAndWrite(w)
		return
	}
	// assuming that a DataRow starts the buffering
	// and a server ready for query packet ends it.
	if pkt.Type() == pgtypes.ServerReadyForQuery {
		log.Printf("redact and write, rows=%v/%v", m.maxRows, m.rowCount)
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
		log.Printf("failed redacting data row packets, err=%v", err)
	}
	if _, err := redactedDataRows.Write(m.typedPackets.Bytes()); err != nil {
		log.Printf("failed generating packet buffer, err=%v", err)
	}

	if _, err = w.Write(redactedDataRows.Bytes()); err != nil {
		log.Printf("failed writing packet to response writer, err=%v", err)
	}
}

func encodeToDataRow(table *dlppb.Table) (*bytes.Buffer, error) {
	dataRowPackets := bytes.NewBuffer([]byte{})
	for _, row := range table.Rows {
		var fieldCount [2]byte
		binary.BigEndian.PutUint16(fieldCount[:], uint16(len(row.Values)))
		dataRow := []byte{pgtypes.ServerDataRow.Byte(), 0x00, 0x00, 0x00, 0x00}
		dataRow = append(dataRow, fieldCount[:]...)
		for _, val := range row.Values {
			var columnLen [4]byte
			binary.BigEndian.PutUint32(columnLen[:], uint32(len(val.GetStringValue())))
			dataRow = append(dataRow, columnLen[:]...)
			dataRow = append(dataRow, []byte(val.GetStringValue())...)
		}
		binary.BigEndian.PutUint32(dataRow[1:5], uint32(len(dataRow)-1))
		if _, err := dataRowPackets.Write(dataRow); err != nil {
			return nil, fmt.Errorf("failed writing data row byte to buffer, err=%v", err)
		}
	}
	return dataRowPackets, nil
}

func redactDataRow(dlpclient Client, conf *deidentifyConfig, dataRows *bytes.Buffer) (*bytes.Buffer, error) {
	// don't redact too small packets
	if dataRows.Len() < 15 {
		return dataRows, nil
	}
	chunkData := bytes.NewBuffer([]byte{})
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
			columnData := make([]byte, columnLength)
			_, err := io.ReadFull(dataRowBuf, columnData[:])
			if err != nil {
				return dataRows, fmt.Errorf("failed reading column (%v), err=%v", i, err)
			}
			// must append it only once
			if len(tableInput.Headers) < int(columnNumbers) {
				tableInput.Headers = append(tableInput.Headers, &dlppb.FieldId{Name: fmt.Sprintf("%v", i)})
			}
			tableRowValues = append(tableRowValues,
				&dlppb.Value{Type: &dlppb.Value_StringValue{StringValue: string(columnData)}})
			if _, err := chunkData.Write(columnData); err != nil {
				return dataRows, fmt.Errorf("failed writing column (%v) to buffer, err=%v", i, err)
			}
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
