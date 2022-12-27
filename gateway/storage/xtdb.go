package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"olympos.io/encoding/edn"
)

const (
	defaultAddress = "http://localhost:3000"
)

type (
	Storage struct {
		client  http.Client
		address string
	}
	// TxEdnStruct must be a struct containing edn fields.
	// See https://github.com/go-edn/edn.
	TxEdnStruct any
	TxResponse  struct {
		TxID   int       `edn:"xtdb.api/tx-id"`
		TxTime time.Time `edn:"xtdb.api/tx-time"`
	}
)

func (s *Storage) Connect() error {
	s.client = http.Client{}
	s.address = os.Getenv("XTDB_ADDRESS")
	if s.address == "" {
		s.address = defaultAddress
	}
	return nil
}

// buildTrxPutEdn build transaction put operation as string
func (s *Storage) buildTrxPutEdn(trxs ...TxEdnStruct) (string, error) {
	var trxVector []string
	for _, tx := range trxs {
		txEdn, err := edn.Marshal(tx)
		if err != nil {
			return "", err
		}
		trxVector = append(trxVector, fmt.Sprintf(`[:xtdb.api/put %v]`, string(txEdn)))
	}
	return fmt.Sprintf(`{:tx-ops [%v]}`, strings.Join(trxVector, "")), nil
}

// SubmitPutTx sends put transactions to the xtdb API
// https://docs.xtdb.com/clients/1.22.0/http/#submit-tx
func (s *Storage) SubmitPutTx(trxs ...TxEdnStruct) (*TxResponse, error) {
	url := fmt.Sprintf("%s/_xtdb/submit-tx", s.address)
	txOpsEdn, err := s.buildTrxPutEdn(trxs...)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(txOpsEdn))
	if err != nil {
		return nil, err
	}

	req.Header.Set("content-type", "application/edn")
	req.Header.Set("accept", "application/edn")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("http response is empty")
	}
	defer resp.Body.Close()

	var txResponse TxResponse
	if resp.StatusCode == http.StatusAccepted {
		if err := edn.NewDecoder(resp.Body).Decode(&txResponse); err != nil {
			fmt.Printf("error decoding transaction response, err=%v\n", err)
		}
		return &txResponse, nil
	} else {
		data, _ := io.ReadAll(resp.Body)
		fmt.Printf("unknown status code=%v, body=%v\n", resp.StatusCode, string(data))
	}
	return nil, fmt.Errorf("received unknown status code=%v", resp.StatusCode)
}

func (s *Storage) PersistEntities(payloads []map[string]any) (int64, error) {
	url := fmt.Sprintf("%s/_xtdb/submit-tx", s.address)

	bytePayload, err := buildPersistPayload(payloads)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bytePayload))
	if err != nil {
		return 0, err
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		var j map[string]any
		if err = json.NewDecoder(resp.Body).Decode(&j); err != nil {
			return 0, err
		}
		return int64(j["txId"].(float64)), nil
	}

	return 0, errors.New("not 202")
}

func (s *Storage) GetEntity(xtId string) ([]byte, error) {
	url := fmt.Sprintf("%s/_xtdb/entity", s.address)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("accept", "application/edn")

	q := req.URL.Query()
	q.Add("eid", xtId)
	req.URL.RawQuery = q.Encode()

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	return nil, nil
}

func (s *Storage) QueryRaw(ednQuery []byte) ([]byte, error) {
	return s.queryRequest(ednQuery, "application/edn")
}

func (s *Storage) QueryRawAsJson(ednQuery []byte) ([]byte, error) {
	return s.queryRequest(ednQuery, "application/json")
}

func (s *Storage) Query(ednQuery []byte) ([]byte, error) {
	b, err := s.queryRequest(ednQuery, "application/edn")
	if err != nil {
		return nil, err
	}

	var p [][]map[edn.Keyword]any
	if err = edn.Unmarshal(b, &p); err != nil {
		return nil, err
	}

	r := make([]map[edn.Keyword]any, 0)
	for _, l := range p {
		r = append(r, l[0])
	}

	response, err := edn.Marshal(r)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (s *Storage) QueryAsJson(ednQuery []byte) ([]byte, error) {
	b, err := s.queryRequest(ednQuery, "application/json")
	if err != nil {
		return nil, err
	}

	var p [][]map[string]any
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, err
	}

	r := make([]map[string]any, 0)
	for _, l := range p {
		r = append(r, l[0])
	}

	response, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func EntityToMap(obj any) map[string]any {
	payload := make(map[string]any)

	v := reflect.ValueOf(obj).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		xtdbName := v.Type().Field(i).Tag.Get("edn")
		if xtdbName != "" && xtdbName != "-" {
			payload[xtdbName] = f.Interface()
		}
	}
	return payload
}

func (s *Storage) queryRequest(ednQuery []byte, contentType string) ([]byte, error) {
	url := fmt.Sprintf("%s/_xtdb/query", s.address)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(ednQuery))
	if err != nil {
		return nil, err
	}

	req.Header.Set("accept", contentType)
	req.Header.Set("content-type", "application/edn")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func buildPersistPayload(payloads []map[string]any) ([]byte, error) {
	txOps := make([]any, 0)
	for _, payload := range payloads {
		txOps = append(txOps, []any{
			"put", payload,
		})
	}
	b, err := json.Marshal(map[string]any{"tx-ops": txOps})
	if err != nil {
		return nil, err
	}
	return b, nil
}
