use std::sync::Arc;

use std::pin::Pin;
use std::task::Poll;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::sync::Mutex;
use tracing::{debug, error};

pub struct ChannelWebSocketStream {
    rdp_data_rx: Arc<Mutex<tokio::sync::mpsc::Receiver<Vec<u8>>>>,
    response_tx: tokio::sync::mpsc::Sender<Vec<u8>>,
    read_buffer: Vec<u8>,
}

impl ChannelWebSocketStream {
    pub fn new(
        rdp_data_rx: Arc<Mutex<tokio::sync::mpsc::Receiver<Vec<u8>>>>,
        response_tx: tokio::sync::mpsc::Sender<Vec<u8>>,
    ) -> Self {
        Self {
            rdp_data_rx,
            response_tx,
            read_buffer: Vec::new(),
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

        // If we have buffered data, return it first
        if !this.read_buffer.is_empty() {
            let to_read = std::cmp::min(buf.remaining(), this.read_buffer.len());
            buf.put_slice(&this.read_buffer[..to_read]);
            this.read_buffer.drain(..to_read);
            return Poll::Ready(Ok(()));
        }

        // Try to receive data from the channel
        let mut receiver = match this.rdp_data_rx.try_lock() {
            Ok(guard) => guard,
            Err(_) => {
                // Lock is held, register for wakeup and return Pending
                cx.waker().wake_by_ref();
                return Poll::Pending;
            }
        };

        match receiver.try_recv() {
            Ok(data) => {
                debug!(
                    "> ChannelWebSocketStream: Received {} bytes from channel",
                    data.len()
                );
                let to_read = std::cmp::min(buf.remaining(), data.len());
                buf.put_slice(&data[..to_read]);
                if data.len() > to_read {
                    this.read_buffer.extend_from_slice(&data[to_read..]);
                }
                Poll::Ready(Ok(()))
            }
            Err(tokio::sync::mpsc::error::TryRecvError::Empty) => {
                // No data available, register for wakeup and return Pending
                cx.waker().wake_by_ref();
                Poll::Pending
            }
            Err(tokio::sync::mpsc::error::TryRecvError::Disconnected) => {
                // Channel is closed, return EOF
                Poll::Ready(Ok(()))
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

        // Send data to response channel
        match this.response_tx.try_send(buf.to_vec()) {
            Ok(()) => {
                debug!(
                    "> ChannelWebSocketStream: Sent {} bytes to response channel",
                    buf.len()
                );
                Poll::Ready(Ok(buf.len()))
            }
            Err(tokio::sync::mpsc::error::TrySendError::Full(_)) => {
                // Channel is full, register for wakeup and return Pending
                cx.waker().wake_by_ref();
                Poll::Pending
            }
            Err(tokio::sync::mpsc::error::TrySendError::Closed(_)) => {
                error!("> Response channel is closed");
                Poll::Ready(Err(std::io::Error::new(
                    std::io::ErrorKind::BrokenPipe,
                    "Response channel closed",
                )))
            }
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
        Poll::Ready(Ok(()))
    }
}
