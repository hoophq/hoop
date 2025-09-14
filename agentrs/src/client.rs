use bytes::BytesMut;
use std::time::Duration;
use std::{net::SocketAddr, sync::Arc};
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::TcpStream;
use tokio::time::timeout;
use typed_builder::TypedBuilder;

use crate::conf;
use crate::rdp::proxy::RdpProxy;

#[derive(TypedBuilder)]
pub struct Client<S> {
    config: Arc<conf::Conf>,
    client_addr: SocketAddr,
    client_stream: S,
}

impl<S> Client<S>
where
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
{
    pub async fn serve(self) -> anyhow::Result<()> {
        let Self {
            client_stream,
            client_addr,
            config,
        } = self;
        println!("Serving client from {}", client_addr);
        let ip = "10.211.55.6";

        let strip: &str = &ip.trim();

        let upstream_addr: SocketAddr = format!("{}:{}", strip, 3389)
            .parse()
            .map_err(|e| anyhow::anyhow!("bad upstream addr '{}': {}", strip, e))?;

        println!("Connecting to upstream RDP server at {:?}", upstream_addr);

        // Connect upstream
        let server_stream = timeout(Duration::from_secs(5), TcpStream::connect(upstream_addr))
            .await
            .map_err(|_| anyhow::anyhow!("connect timeout to {}", upstream_addr))??;

        let proxy = RdpProxy::builder()
            .config(config)
            .client_address(client_addr)
            .client_stream(client_stream)
            .server_stream(server_stream)
            .client_stream_leftover_bytes(BytesMut::new()) //for now i am not using this but
            //I will use to split initial pcb or token cookie from the left over bytes in the stream
            .build();

        proxy.run().await?;

        Ok(())
    }
}
