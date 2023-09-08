package storagev2

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func submitPutTx(client HTTPClient, xtdbAPI string, trxs ...types.TxObject) (*types.TxResponse, error) {
	url := fmt.Sprintf("%s/_xtdb/submit-tx", xtdbAPI)

	txOps, err := buildTrxPutEdn(trxs...)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(txOps))
	if err != nil {
		return nil, err
	}

	req.Header.Set("content-type", "application/edn")
	req.Header.Set("accept", "application/edn")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("http response is empty")
	}
	defer resp.Body.Close()

	var txResponse types.TxResponse
	if resp.StatusCode == http.StatusAccepted {
		if err := edn.NewDecoder(resp.Body).Decode(&txResponse); err != nil {
			log.Warnf("error decoding transaction response, err=%v", err)
		}
		// make a best-effort to wait the transaction to sync
		if txResponse.TxID > 0 {
			if err := awaitTx(xtdbAPI, txResponse.TxID); err != nil {
				log.Warnf(err.Error())
			}
		}
		return &txResponse, nil
	} else {
		data, _ := io.ReadAll(resp.Body)
		if len(data) > 2000 {
			data = data[:2000]
		}
		log.Infof("unknown status code=%v, body=%v", resp.StatusCode, string(data))
	}
	return nil, fmt.Errorf("received unknown status code=%v", resp.StatusCode)
}

// submitEvictTx sends evict transactions to the xtdb API
// https://docs.xtdb.com/clients/1.22.0/http/#submit-tx
func submitEvictTx(client HTTPClient, xtdbAPI string, xtIDs ...string) (*types.TxResponse, error) {
	if len(xtIDs) == 0 {
		return nil, fmt.Errorf("need at least one xt/id to evict")
	}
	url := fmt.Sprintf("%s/_xtdb/submit-tx", xtdbAPI)
	txOpsEdn, err := buildTrxEvictEdn(xtIDs...)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(txOpsEdn))
	if err != nil {
		return nil, err
	}

	req.Header.Set("content-type", "application/edn")
	req.Header.Set("accept", "application/edn")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("http response is empty")
	}
	defer resp.Body.Close()

	var txResponse types.TxResponse
	if resp.StatusCode == http.StatusAccepted {
		if err := edn.NewDecoder(resp.Body).Decode(&txResponse); err != nil {
			log.Infof("error decoding transaction response, err=%v", err)
		}
		// make a best-effort to wait the transaction to sync
		if txResponse.TxID > 0 {
			if err := awaitTx(xtdbAPI, txResponse.TxID); err != nil {
				log.Warnf(err.Error())
			}
		}
		return &txResponse, nil
	} else {
		data, _ := io.ReadAll(resp.Body)
		log.Infof("unknown status code=%v, body=%v", resp.StatusCode, string(data))
	}
	return nil, fmt.Errorf("received unknown status code=%v", resp.StatusCode)
}

// buildTrxEvictEdn build transaction evict operation as string
func buildTrxEvictEdn(xtIDs ...string) (string, error) {
	var trxVector []string
	for _, xtID := range xtIDs {
		trxVector = append(trxVector, fmt.Sprintf(`[:xtdb.api/evict %q]`, xtID))
	}
	return fmt.Sprintf(`{:tx-ops [%v]}`, strings.Join(trxVector, "")), nil
}

func buildTrxPutEdn(trxs ...types.TxObject) (string, error) {
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

func awaitTx(xtdbAPI string, txID int64) error {
	url := fmt.Sprintf("%s/_xtdb/await-tx?tx-id=%v&timeout=5000", xtdbAPI, txID)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed awaiting transaction %v, err=%v", txID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed awaiting transaction %v, code=%v, response=%v",
			txID, resp.StatusCode, string(data))
	}
	return nil
}
