use std::io;
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt as _};
use typed_builder::TypedBuilder;

#[derive(TypedBuilder)]
pub struct Proxy<A, B> {
    transport_a: A,
    transport_b: B,
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

        let fwd_fut = tokio::io::copy_bidirectional(&mut transport_a, &mut transport_b).await;
        let res = match fwd_fut {
            Ok((_n1, _n2)) => Ok(()),
            Err(e) => Err(e),
        };

        // Ensure we close the transports cleanly at the end (ignore errors at this point)
        let _ = tokio::join!(transport_a.shutdown(), transport_b.shutdown());

        match res {
            Ok(()) => Ok(()),
            Err(error) if is_error(&error) => Err(anyhow::Error::new(error).context("forward")),
            Err(_) => Ok(()),
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
