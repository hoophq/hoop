use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tracing::error;
use std::pin::Pin;
use std::task::{Context, Poll};

pub struct LoggingIo<T> {
    inner: T,
    prefix: &'static str,
}

impl<T> LoggingIo<T> {
    pub fn new(inner: T, prefix: &'static str) -> Self {
        Self { inner, prefix }
    }
}

impl<T: AsyncRead + Unpin> AsyncRead for LoggingIo<T> {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        let pre_len = buf.filled().len();
        let poll = Pin::new(&mut self.inner).poll_read(cx, buf);
        if let Poll::Ready(Ok(())) = &poll {
            let filled = &buf.filled()[pre_len..];
            if !filled.is_empty() {
                error!("{} READ: {:02X?}", self.prefix, filled);
            }
        }
        poll
    }
}

impl<T: AsyncWrite + Unpin> AsyncWrite for LoggingIo<T> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        data: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        error!("{} WRITE: {:02X?}", self.prefix, data);
        Pin::new(&mut self.inner).poll_write(cx, data)
    }

    fn poll_flush(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }

    fn poll_shutdown(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}
