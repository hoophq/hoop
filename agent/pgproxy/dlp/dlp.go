package pgproxydlp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/dlp/apiv2/dlppb"
	"github.com/runopsio/hoop/agent/dlp"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
)

const (
	defaultRequestTimeoutInSec = 5
	defaultMaskingCharacter    = "*"
	defaultNumberToMask        = 5
)

type inputData struct {
	inputTable  *dlppb.Table
	inputBuffer *bytes.Buffer
}

func newTableInputData(data *dlppb.Table) dlp.InputData { return &inputData{inputTable: data} }
func (i *inputData) ContentItem() *dlppb.ContentItem {
	switch {
	case i.inputTable != nil:
		return &dlppb.ContentItem{DataItem: &dlppb.ContentItem_Table{Table: i.inputTable}}
	case i.inputBuffer != nil:
		defer i.inputBuffer.Reset()
		return &dlppb.ContentItem{
			DataItem: &dlppb.ContentItem_Value{
				Value: i.inputBuffer.String(),
			},
		}
	default:
		return nil
	}
}

func ParseInfoTypes(infoTypesList []string) []*dlppb.InfoType {
	var infoTypes []*dlppb.InfoType
	for _, infoType := range infoTypesList {
		if infoType == "" {
			continue
		}
		infoTypes = append(infoTypes, &dlppb.InfoType{Name: infoType})
	}
	return infoTypes
}

func RedactDataRow(dlpclient dlp.Client, conf dlp.DeidentifyConfig, dataRows *bytes.Buffer) (*dlp.Chunk, error) {
	// don't redact too small packets
	if dataRows.Len() < 15 {
		return dlp.NewChunk(dataRows), nil
	}
	tableInput := &dlppb.Table{
		Headers: []*dlppb.FieldId{},
		Rows:    []*dlppb.Table_Row{},
	}
	dataRowsCopy := bytes.NewBuffer(dataRows.Bytes())
	defer dataRowsCopy.Reset()
	for n := 1; ; n++ {
		_, dataRowPkt, err := pgtypes.DecodeTypedPacket(dataRowsCopy)
		if err != nil {
			if err == io.EOF {
				break
			}
			return dlp.NewChunk(dataRows), err
		}
		if dataRowPkt.Type() != pgtypes.ServerDataRow {
			return dlp.NewChunk(dataRows), fmt.Errorf("expected data row packet, got=%v", string(dataRowPkt.Type()))
		}
		dataRowBuf := bytes.NewBuffer(dataRowPkt.Frame())
		columnNumbers := binary.BigEndian.Uint16(dataRowBuf.Next(2))
		tableRowValues := []*dlppb.Value{}
		// iterate in each row field
		for i := 1; i <= int(columnNumbers); i++ {
			columnLength := binary.BigEndian.Uint32(dataRowBuf.Next(4))
			// -1 means it's a NULL value
			isNullValue := false
			if columnLength == pgtypes.ServerDataRowNull {
				columnLength = 0
				isNullValue = true
			}
			columnData := make([]byte, columnLength)
			_, err := io.ReadFull(dataRowBuf, columnData[:])
			if err != nil {
				return dlp.NewChunk(dataRows), fmt.Errorf("failed reading column (idx=%v,len=%v), err=%v", i, columnLength, err)
			}
			// allows decoding this value to the proper type when the data
			// is returning from the dlp service
			if isNullValue {
				columnData = []byte(pgtypes.DLPColumnNullType)
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
	chunkCh := make(chan *dlp.Chunk)
	go func() {
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*defaultRequestTimeoutInSec)
		defer cancelFn()
		redactedChunk := dlpclient.DeidentifyContent(ctx, conf, 0, newTableInputData(tableInput))
		chunkCh <- redactedChunk
	}()
	var redactedChunk *dlp.Chunk
	for c := range chunkCh {
		redactedChunk = c
		close(chunkCh)
		break
	}
	if err := redactedChunk.Error(); err != nil {
		return dlp.NewChunk(dataRows), err
	}
	return redactedChunk, nil
}
