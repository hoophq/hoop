package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"

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
)

func (s *Storage) Connect() error {
	s.client = http.Client{}
	s.address = os.Getenv("XTDB_ADDRESS")
	if s.address == "" {
		s.address = defaultAddress
	}
	return nil
}

func (s *Storage) PersistEntities(payloads []map[string]interface{}) (int64, error) {
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
		var j map[string]interface{}
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

func (s *Storage) Query(ednQuery []byte) ([]byte, error) {
	b, err := s.queryRequest(ednQuery, "application/edn")
	if err != nil {
		return nil, err
	}

	var p [][]map[edn.Keyword]interface{}
	if err = edn.Unmarshal(b, &p); err != nil {
		return nil, err
	}

	r := make([]map[edn.Keyword]interface{}, 0)
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

	var p [][]map[string]interface{}
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, err
	}

	r := make([]map[string]interface{}, 0)
	for _, l := range p {
		r = append(r, l[0])
	}

	response, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func EntityToMap(obj interface{}) map[string]interface{} {
	payload := make(map[string]interface{})

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

func buildPersistPayload(payloads []map[string]interface{}) ([]byte, error) {
	txOps := make([]interface{}, 0)
	for _, payload := range payloads {
		txOps = append(txOps, []interface{}{
			"put", payload,
		})
	}
	b, err := json.Marshal(map[string]interface{}{"tx-ops": txOps})
	if err != nil {
		return nil, err
	}
	return b, nil
}
