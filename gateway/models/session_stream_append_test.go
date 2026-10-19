package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

// Once a stream has a chunk, a blob append must land after it.
func TestAppendSessionStreamAfterChunkKeepsOrder(t *testing.T) {
	startTestDB(t)

	sid := "00000000-0000-0000-0000-0000000000b1"
	if err := models.UpsertSession(models.Session{ID: sid, OrgID: testOrgID, Connection: "bash",
		ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := models.CreateEmptySessionStreamBlob(testOrgID, sid, nil); err != nil {
		t.Fatalf("create stream blob: %v", err)
	}
	for i, step := range []struct {
		chunk   bool
		entries string
	}{
		{false, `[[1,"o","YQ=="]]`},
		{true, `[[2,"o","Yg=="]]`},
		{false, `[[3,"o","Yw=="]]`},
	} {
		var err error
		if step.chunk {
			err = models.AppendSessionStreamChunkTx(models.DB, testOrgID, sid, json.RawMessage(step.entries))
		} else {
			err = models.AppendSessionStream(testOrgID, sid, json.RawMessage(step.entries))
		}
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	sess, err := models.GetSessionByID(testOrgID, sid)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := sess.GetBlobStream()
	if err != nil {
		t.Fatal(err)
	}
	var got [][]any
	if err := json.Unmarshal(blob.BlobStream, &got); err != nil {
		t.Fatalf("decode stream %s: %v", blob.BlobStream, err)
	}
	for i, ev := range got {
		if ev[0].(float64) != float64(i+1) {
			t.Fatalf("stream %s is out of order", blob.BlobStream)
		}
	}
	if len(got) != 3 {
		t.Fatalf("stream %s has %d entries, want 3", blob.BlobStream, len(got))
	}
}
