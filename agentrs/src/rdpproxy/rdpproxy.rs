use anyhow::Context as _;
use ironrdp_pdu::{mcs, nego, x224};
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt};
use typed_builder::TypedBuilder;

use crate::proxy::proxy::*;

#[derive(TypedBuilder)]
pub struct RdpProxy<C, S> {
    client_stream: C,
    server_stream: S,
    client_stream_leftover_bytes: bytes::BytesMut,
}

impl<A, B> RdpProxy<A, B>
where
    A: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    B: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    pub async fn run(self) -> anyhow::Result<()> {
        handle(self).await
    }
}

async fn handle<C, S>(proxy: RdpProxy<C, S>) -> anyhow::Result<()>
where
    C: AsyncRead + AsyncWrite + Unpin + Send + Sync,
    S: AsyncRead + AsyncWrite + Unpin + Send + Sync,
{
    let RdpProxy {
        client_stream,
        server_stream,
        client_stream_leftover_bytes,
    } = proxy;

    //TODO: add tls upgrade
    // --- Dual handshake WITHOUT TLS upgrade / CredSSP ------------------------
    // Create framed I/O
    let mut client_framed =
        ironrdp_tokio::TokioFramed::new_with_leftover(client_stream, client_stream_leftover_bytes);
    let mut server_framed = ironrdp_tokio::TokioFramed::new(server_stream);

    // IMPORTANT: ensure/force PROTOCOL_RDP (no TLS/NLA) during nego.
    // If your helper currently stops at “until TLS upgrade”, refactor it to stop
    // right after Connect-Confirm (and Security Exchange when present).
    let hs = dual_handshake_until_connect_confirm_for_passthrough(
        &mut client_framed,
        &mut server_framed,
    )
    .await?;

    println!(
        "Client selected security protocol: {:?}",
        hs.client_security_protocol
    );
    // inside each framed transport. No TLS, no CredSSP.

    // Get inner streams + leftovers (if any PDUs buffered)
    //
    let (mut client_stream, client_leftover) = client_framed.into_inner();
    let (mut server_stream, server_leftover) = server_framed.into_inner();

    // Flush any leftover bytes in correct directions
    if !server_leftover.is_empty() {
        client_stream
            .write_all(&server_leftover)
            .await
            .context("write server leftover to client")?;
    }
    if !client_leftover.is_empty() {
        server_stream
            .write_all(&client_leftover)
            .await
            .context("write client leftover to server")?;
    }

    Proxy::builder()
        .transport_a(client_stream)
        .transport_b(server_stream)
        .build()
        .forward()
        .await
        .context("forward RDP traffic")?;

    Ok(())
}

#[derive(Debug)]
struct HandshakeResult {
    client_security_protocol: nego::SecurityProtocol,
    server_security_protocol: nego::SecurityProtocol,
}

async fn intercept_connect_confirm<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
    server_security_protocol: nego::SecurityProtocol,
) -> anyhow::Result<()>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    let (_, received_frame) = client_framed
        .read_pdu()
        .await
        .context("read MCS Connect Initial from client")?;
    let received_connect_initial: x224::X224<x224::X224Data<'_>> =
        ironrdp_core::decode(&received_frame).context("decode PDU from client")?;
    let mut received_connect_initial: mcs::ConnectInitial =
        ironrdp_core::decode(&received_connect_initial.0.data)
            .context("decode Connect Initial PDU")?;

    received_connect_initial
        .conference_create_request
        .gcc_blocks
        .core
        .optional_data
        .server_selected_protocol = Some(server_security_protocol);
    let x224_msg_buf = ironrdp_core::encode_vec(&received_connect_initial)?;
    let pdu = x224::X224Data {
        data: std::borrow::Cow::Owned(x224_msg_buf),
    };
    send_pdu(server_framed, &x224::X224(pdu))
        .await
        .context("send connection request to server")?;

    Ok(())
}

pub async fn dual_handshake_until_connect_confirm_for_passthrough<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
) -> anyhow::Result<HandshakeResult>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    // 1) client -> proxy
    let (_, client_frame) = client_framed.read_pdu().await?;
    let client_cr: x224::X224<nego::ConnectionRequest> = ironrdp_core::decode(&client_frame)?;

    // 2) proxy -> server (don’t over-assert protocol; respect client)
    let client_proto = client_cr.0.protocol;
    let tls_capable = nego::SecurityProtocol::HYBRID_EX
        | nego::SecurityProtocol::HYBRID
        | nego::SecurityProtocol::SSL;
    let wanted = client_proto & tls_capable;
    let effective = if wanted.is_empty() {
        client_proto
    } else {
        wanted
    };

    let cr_to_server = nego::ConnectionRequest {
        nego_data: client_cr.0.nego_data.clone(),
        flags: client_cr.0.flags,
        protocol: effective,
    };
    send_pdu(server_framed, &x224::X224(cr_to_server)).await?;

    // 3) server -> proxy
    let (_, server_frame) = server_framed.read_pdu().await?;
    let server_cc: x224::X224<nego::ConnectionConfirm> = ironrdp_core::decode(&server_frame)?;

    // 4) proxy -> client: forward exactly
    send_pdu(client_framed, &server_cc).await?;

    // 5) bookkeeping
    let selected = match &server_cc.0 {
        nego::ConnectionConfirm::Response { protocol, .. } => *protocol,
        _ => nego::SecurityProtocol::empty(),
    };

    Ok(HandshakeResult {
        client_security_protocol: effective,
        server_security_protocol: selected,
    })
}

async fn send_pdu<S, P>(framed: &mut ironrdp_tokio::TokioFramed<S>, pdu: &P) -> anyhow::Result<()>
where
    S: AsyncWrite + Unpin + Send + Sync,
    P: ironrdp_core::Encode,
{
    use ironrdp_tokio::FramedWrite as _;

    let payload = ironrdp_core::encode_vec(pdu).context("failed to encode PDU")?;
    framed
        .write_all(&payload)
        .await
        .context("failed to write PDU")?;
    Ok(())
}
