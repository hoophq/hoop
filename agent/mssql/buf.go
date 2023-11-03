package mssql

import (
	"bytes"
	"io"
	"sync"
)

type blockingReader struct {
	buf  bytes.Buffer
	cond *sync.Cond
}

func newBlockingReader() *blockingReader {
	m := sync.Mutex{}
	return &blockingReader{
		cond: sync.NewCond(&m),
		buf:  bytes.Buffer{},
	}
}

func (r *blockingReader) Write(b []byte) (ln int, err error) {
	ln, err = r.buf.Write(b)
	r.cond.Broadcast()
	return
}

func (br *blockingReader) Read(b []byte) (ln int, err error) {
	ln, err = br.buf.Read(b)
	if err == io.EOF {
		br.cond.L.Lock()
		br.cond.Wait()
		br.cond.L.Unlock()
		ln, err = br.buf.Read(b)
	}
	return
}
