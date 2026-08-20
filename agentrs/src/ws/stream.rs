use std::pin::Pin;
use std::task::Poll;

use bytes::{Buf as _, Bytes};
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::sync::mpsc;
use tokio_util::sync::PollSender;
use tracing::{debug, error};

/// The adapter accepts at most this many bytes per `poll_write`. Combined with
/// `RESPONSE_QUEUE_SLOTS`, this is a strict byte bound for queued RDP output.
pub const RESPONSE_CHUNK_BYTES: usize = 32 * 1024;
pub const RESPONSE_QUEUE_SLOTS: usize = 32;

pub struct ChannelWebSocketStream {
    rdp_data_rx: mpsc::Receiver<Vec<u8>>,
    response_tx: PollSender<Vec<u8>>,
    read_buffer: Bytes,
}

impl ChannelWebSocketStream {
    pub fn new(rdp_data_rx: mpsc::Receiver<Vec<u8>>, response_tx: mpsc::Sender<Vec<u8>>) -> Self {
        Self {
            rdp_data_rx,
            response_tx: PollSender::new(response_tx),
            read_buffer: Bytes::new(),
        }
    }
}

impl AsyncRead for ChannelWebSocketStream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
        buf: &mut tokio::io::ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        let this = self.get_mut();
        if buf.remaining() == 0 {
            return Poll::Ready(Ok(()));
        }

        loop {
            if !this.read_buffer.is_empty() {
                let to_read = buf.remaining().min(this.read_buffer.len());
                buf.put_slice(&this.read_buffer[..to_read]);
                this.read_buffer.advance(to_read);
                return Poll::Ready(Ok(()));
            }

            match Pin::new(&mut this.rdp_data_rx).poll_recv(cx) {
                Poll::Ready(Some(data)) if data.is_empty() => continue,
                Poll::Ready(Some(data)) => {
                    debug!(
                        "> ChannelWebSocketStream: Received {} bytes from channel",
                        data.len()
                    );
                    this.read_buffer = Bytes::from(data);
                }
                Poll::Ready(None) => return Poll::Ready(Ok(())),
                Poll::Pending => return Poll::Pending,
            }
        }
    }
}

impl AsyncWrite for ChannelWebSocketStream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
        buf: &[u8],
    ) -> Poll<Result<usize, std::io::Error>> {
        let this = self.get_mut();
        if buf.is_empty() {
            return Poll::Ready(Ok(0));
        }

        match this.response_tx.poll_reserve(cx) {
            Poll::Ready(Ok(())) => {
                let written = buf.len().min(RESPONSE_CHUNK_BYTES);
                if this.response_tx.send_item(buf[..written].to_vec()).is_err() {
                    error!("> Response channel closed after reserving capacity");
                    return Poll::Ready(Err(std::io::Error::new(
                        std::io::ErrorKind::BrokenPipe,
                        "response channel closed",
                    )));
                }
                debug!(
                    "> ChannelWebSocketStream: Sent {} bytes to response channel",
                    written
                );
                Poll::Ready(Ok(written))
            }
            Poll::Ready(Err(_)) => {
                error!("> Response channel is closed");
                Poll::Ready(Err(std::io::Error::new(
                    std::io::ErrorKind::BrokenPipe,
                    "response channel closed",
                )))
            }
            Poll::Pending => Poll::Pending,
        }
    }

    fn poll_flush(
        self: Pin<&mut Self>,
        _cx: &mut std::task::Context<'_>,
    ) -> Poll<Result<(), std::io::Error>> {
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(
        self: Pin<&mut Self>,
        _cx: &mut std::task::Context<'_>,
    ) -> Poll<Result<(), std::io::Error>> {
        self.get_mut().response_tx.close();
        Poll::Ready(Ok(()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::task::Context;

    use futures::task::{ArcWake, waker_ref};
    use tokio::io::AsyncReadExt as _;

    #[derive(Default)]
    struct WakeCounter(AtomicUsize);

    impl ArcWake for WakeCounter {
        fn wake_by_ref(arc_self: &Arc<Self>) {
            arc_self.0.fetch_add(1, Ordering::Relaxed);
        }
    }

    #[tokio::test]
    async fn oversized_input_fills_the_read_buffer_before_retaining_the_tail() {
        let (input_tx, input_rx) = mpsc::channel(1);
        let (output_tx, _output_rx) = mpsc::channel(1);
        input_tx.send(b"abcdefgh".to_vec()).await.unwrap();

        let mut stream = ChannelWebSocketStream::new(input_rx, output_tx);
        let mut first = [0; 3];
        stream.read_exact(&mut first).await.unwrap();
        assert_eq!(&first, b"abc");

        let mut rest = [0; 5];
        stream.read_exact(&mut rest).await.unwrap();
        assert_eq!(&rest, b"defgh");
    }

    #[test]
    fn empty_input_registers_the_waker_without_self_waking() {
        let (_input_tx, input_rx) = mpsc::channel(1);
        let (output_tx, _output_rx) = mpsc::channel(1);
        let mut stream = ChannelWebSocketStream::new(input_rx, output_tx);
        let counter = Arc::new(WakeCounter::default());
        let waker = waker_ref(&counter);
        let mut cx = Context::from_waker(&waker);
        let mut bytes = [0; 1];
        let mut read_buf = tokio::io::ReadBuf::new(&mut bytes);

        assert!(
            Pin::new(&mut stream)
                .poll_read(&mut cx, &mut read_buf)
                .is_pending()
        );
        assert_eq!(counter.0.load(Ordering::Relaxed), 0);
    }

    #[tokio::test]
    async fn full_output_waits_for_receiver_capacity_without_self_waking() {
        let (_input_tx, input_rx) = mpsc::channel(1);
        let (output_tx, mut output_rx) = mpsc::channel(1);
        let mut stream = ChannelWebSocketStream::new(input_rx, output_tx);
        let counter = Arc::new(WakeCounter::default());
        let waker = waker_ref(&counter);
        let mut cx = Context::from_waker(&waker);

        assert!(matches!(
            Pin::new(&mut stream).poll_write(&mut cx, b"first"),
            Poll::Ready(Ok(5))
        ));
        assert!(
            Pin::new(&mut stream)
                .poll_write(&mut cx, b"second")
                .is_pending()
        );
        assert_eq!(counter.0.load(Ordering::Relaxed), 0);

        assert_eq!(output_rx.recv().await.unwrap(), b"first");
        tokio::task::yield_now().await;
        assert!(counter.0.load(Ordering::Relaxed) > 0);
        assert!(matches!(
            Pin::new(&mut stream).poll_write(&mut cx, b"second"),
            Poll::Ready(Ok(6))
        ));
    }

    #[test]
    fn output_queue_has_a_strict_byte_bound() {
        let (_input_tx, input_rx) = mpsc::channel(1);
        let (output_tx, mut output_rx) = mpsc::channel(RESPONSE_QUEUE_SLOTS);
        let mut stream = ChannelWebSocketStream::new(input_rx, output_tx);
        let counter = Arc::new(WakeCounter::default());
        let waker = waker_ref(&counter);
        let mut cx = Context::from_waker(&waker);
        let offered = vec![0x5a; RESPONSE_CHUNK_BYTES * 2];

        for _ in 0..RESPONSE_QUEUE_SLOTS {
            assert!(matches!(
                Pin::new(&mut stream).poll_write(&mut cx, &offered),
                Poll::Ready(Ok(RESPONSE_CHUNK_BYTES))
            ));
        }
        assert!(
            Pin::new(&mut stream)
                .poll_write(&mut cx, &offered)
                .is_pending()
        );

        let mut queued = 0;
        while let Ok(chunk) = output_rx.try_recv() {
            assert!(chunk.len() <= RESPONSE_CHUNK_BYTES);
            queued += chunk.len();
        }
        assert_eq!(queued, RESPONSE_QUEUE_SLOTS * RESPONSE_CHUNK_BYTES);
    }
}
