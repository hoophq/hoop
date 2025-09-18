use bytes::BytesMut;
use ironrdp_core::{Decode, ReadCursor};
use ironrdp_pdu::nego::{ConnectionRequest, NegoRequestData};
use ironrdp_pdu::tpkt::TpktHeader;
use std::io;
use std::time::Duration;
use std::{net::SocketAddr, sync::Arc};
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::TcpStream;
use tokio::time::timeout;
use typed_builder::TypedBuilder;

use ironrdp_pdu::tpdu::{TpduCode, TpduHeader};
use ironrdp_pdu::x224;
use tokio::io::AsyncReadExt;

use crate::conf;
use crate::rdp::proxy::RdpProxy;

#[derive(TypedBuilder)]
pub struct Client<S> {
    config: Arc<conf::Conf>,
    client_addr: SocketAddr,
    client_stream: S,
}
/// Read exactly one TPKT PDU from the stream and return the raw bytes.
/// The buffer length will equal the TPKT length field.
pub async fn read_first_tpkt<S: AsyncRead + Unpin>(client: &mut S) -> io::Result<Vec<u8>> {
    // Read 4-byte TPKT header
    let mut hdr = [0u8; TpktHeader::SIZE];
    client.read_exact(&mut hdr).await?;

    // Not TPKT (e.g., TLS/RDG)? Return what we have so upper layers can branch
    if hdr[0] != TpktHeader::VERSION {
        return Ok(hdr.to_vec());
    }

    // Parse header with your own decoder (endianness checked)
    let mut cur = ReadCursor::new(&hdr);
    let tpkt = TpktHeader::read(&mut cur).map_err(|e| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("TPKT parse error: {e:?}"),
        )
    })?;

    let total_len = tpkt.packet_length();
    if total_len < TpktHeader::SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid TPKT length",
        ));
    }

    // Read the remaining payload
    let body_len = total_len - TpktHeader::SIZE;
    let mut body = vec![0u8; body_len];
    client.read_exact(&mut body).await?;

    let mut pdu = Vec::with_capacity(total_len);
    pdu.extend_from_slice(&hdr);
    pdu.extend_from_slice(&body);
    Ok(pdu)
}

/// Extract "mstshash=…" (or msthash=…) from an X.224 ConnectionRequest inside a single TPKT.
pub async fn parse_mstsc_cookie_from_x224(first_pdu: &[u8]) -> Option<String> {
    if first_pdu.len() < TpktHeader::SIZE {
        return None;
    }
    // If not TPKT, bail (likely TLS/RDG)
    if first_pdu[0] != TpktHeader::VERSION {
        return None;
    }

    // Verify we have the whole TPKT
    let mut tpkt_cur = ReadCursor::new(first_pdu);
    let tpkt = TpktHeader::read(&mut tpkt_cur).ok()?;
    if first_pdu.len() != tpkt.packet_length() {
        // You must feed exactly one complete TPKT buffer here.
        return None;
    }

    // Optional: check TPDU header/code before full decode (helps with debugging)
    let payload = &first_pdu[TpktHeader::SIZE..];
    let mut tpdu_cur = ReadCursor::new(payload);
    let tpdu = TpduHeader::read(&mut tpdu_cur, &tpkt).ok()?;

    println!("TPDU code: {:?}", tpdu.code);
    println!("TPDU LI: {}", tpdu.li);
    if tpdu.code != TpduCode::CONNECTION_REQUEST {
        println!("Not a Connection Request");
        return None; // Not a Connection Request
    }

    // Decode X.224<ConnectionRequest> using your generic impls
    let mut cur = ReadCursor::new(first_pdu);
    let x224::X224(ConnectionRequest { nego_data, .. }) =
        <x224::X224<ConnectionRequest> as Decode>::decode(&mut cur).ok()?;
    //println!("Negotiation data: {:?}", nego_data);
    // In this API, the cookie is embedded in nego_data variants.
    // The exact enum variants depend on your IronRDP revision. Common ones:
    //   - NegoRequestData::Cookie(Cookie)             //
    //   - NegoRequestData::RoutingToken(Vec<u8>)      //
    //   //println!("Nego data: {:?}", nego_data);
    let cookies = match nego_data? {
        NegoRequestData::Cookie(c) => c, // Cookie struct
        _ => return None,
        // Add any other variants your enum has; default to None
    };
    //println!("Extracted cookie: {:?}", cookies);

    // Typical wire line: "Cookie: mstshash=user\r\n"

    Some(cookies.0)
}

impl<S> Client<S>
where
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
{
    pub async fn serve(self) -> anyhow::Result<()> {
        let Self {
            mut client_stream,
            client_addr,
            config,
        } = self;
        //println!("Serving client from {}", client_addr);
        // 1) Peek the first client PDU (TPKT/X.224)
        let first_pdu = read_first_tpkt(&mut client_stream).await?;
        // println!("First PDU length: {}", first_pdu.len());
        let claims = parse_mstsc_cookie_from_x224(&first_pdu).await;
        println!("Claims from token: {:?}", claims);

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
            .creds(claims.unwrap())
            .client_address(client_addr)
            .client_stream(client_stream)
            .server_stream(server_stream)
            .client_stream_leftover_bytes(bytes::BytesMut::from(first_pdu.as_slice()))
            //I will use to split initial pcb or token cookie from the left over bytes in the stream
            .build();

        proxy.run().await?;

        Ok(())
    }
}
