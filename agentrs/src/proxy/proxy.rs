use futures::future::Either;
use std::io;
use std::pin::pin;
use std::sync::Arc;
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt as _};
use tokio::sync::Notify;
use typed_builder::TypedBuilder;

use crate::logio::logio::LoggingIo;
use crate::transport::transport::copy_bidirectional;

#[derive(TypedBuilder)]
pub struct Proxy<A, B> {
    transport_a: A,
    transport_b: B,
    #[builder(default = None)]
    buffer_size: Option<usize>,
}

// this is to copy_bidirectional stream from client to server
// we could here latter sending metrics about the traffic
// copy the bitmaps from the rdp
impl<A, B> Proxy<A, B>
where
    A: AsyncWrite + AsyncRead + Unpin,
    B: AsyncWrite + AsyncRead + Unpin,
{
    pub async fn forward(self) -> anyhow::Result<()> {
        let mut transport_a = self.transport_a; //LoggingIo::new(self.transport_a, "A");
        let mut transport_b = self.transport_b; //LoggingIo::new(self.transport_b, "B");
        let notify_kill = Arc::new(Notify::new());

        let kill_notified = notify_kill.notified();

        let res = if let Some(buffer_size) = self.buffer_size {
            let forward_fut =
                copy_bidirectional(&mut transport_a, &mut transport_b, buffer_size, buffer_size);
            match futures::future::select(pin!(forward_fut), pin!(kill_notified)).await {
                Either::Left((res, _)) => res.map(|_| ()),
                Either::Right(_) => Ok(()),
            }
        } else {
            let forward_fut = tokio::io::copy_bidirectional(&mut transport_a, &mut transport_b);
            match futures::future::select(pin!(forward_fut), pin!(kill_notified)).await {
                Either::Left((res, _)) => res.map(|_| ()),
                Either::Right(_) => Ok(()),
            }
        };

        // Ensure we close the transports cleanly at the end (ignore errors at this point)
        let _ = tokio::join!(transport_a.shutdown(), transport_b.shutdown());

        match res {
            Ok(()) => Ok(()),
            Err(error) => {
                let really_an_error = is_error(&error);

                let error = anyhow::Error::new(error);

                if really_an_error {
                    Err(error.context("forward"))
                } else {
                    Ok(())
                }
            }
        }
    }
}

fn is_error(original_error: &io::Error) -> bool {
    use std::error::Error as _;

    let mut dyn_error: Option<&dyn std::error::Error> = Some(original_error);

    while let Some(source_error) = dyn_error.take() {
        if let Some(io_error) = source_error.downcast_ref::<io::Error>() {
            match io_error.kind() {
                io::ErrorKind::ConnectionReset
                | io::ErrorKind::UnexpectedEof
                | io::ErrorKind::ConnectionAborted => {
                    return false;
                }
                io::ErrorKind::Other => {
                    dyn_error = io_error.source();
                }
                _ => {
                    return true;
                }
            }
        } else if let Some(tungstenite_error) = source_error.downcast_ref::<tungstenite::Error>() {
            match tungstenite_error {
                tungstenite::Error::ConnectionClosed | tungstenite::Error::AlreadyClosed => {
                    return false;
                }
                tungstenite::Error::Protocol(
                    tungstenite::error::ProtocolError::ResetWithoutClosingHandshake,
                ) => {
                    return false;
                }
                tungstenite::Error::Io(io_error) => dyn_error = Some(io_error),
                _ => return true,
            }
        } else {
            dyn_error = source_error.source();
        }
    }

    true
}
