use anyhow::{Context as _, Result};
use ironrdp_pdu::{mcs, nego, x224};
use std::net::IpAddr;
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::io::{AsyncRead, AsyncWrite};
use tracing::debug;
use tracing::info;
use tracing::instrument;
use typed_builder::TypedBuilder;
use zeroize::{Zeroize, ZeroizeOnDrop, Zeroizing};

use ironrdp_tokio::FramedWrite as _;

use crate::conf;
use crate::proxy::Proxy;

#[derive(TypedBuilder)]
pub struct RdpProxy<C, S> {
    config: Arc<conf::Conf>,
    creds: Zeroizing<String>,
    username: String,
    password: Zeroizing<String>,
    server_target: String,
    client_address: SocketAddr,
    client_stream: C,
    server_stream: S,
    #[allow(dead_code)]
    client_stream_leftover_bytes: bytes::BytesMut,
    /// Agent-side PII guard for the post-CredSSP forwarding stage. None = no
    /// guard (transparent forwarding).
    #[builder(default)]
    guard: Option<crate::guard::GuardConfig>,
    #[builder(default = String::new())]
    session_id: String,
    /// Reports guard violations to the gateway. None = no reporting.
    #[builder(default)]
    report: Option<crate::proxy::ViolationReporter>,
}

impl<A, B> RdpProxy<A, B>
where
    A: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
    B: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
{
    pub async fn run(self) -> Result<()> {
        handle(self).await
    }
}

async fn retrieve_gateway_public_key(
    hostname: String,
    acceptor: tokio_rustls::TlsAcceptor,
) -> anyhow::Result<Vec<u8>> {
    debug!("Retrieving Devolutions Gateway TLS public key");
    let (client_side, server_side) = tokio::io::duplex(4096);

    let connect_fut = crate::tls::connect(hostname, client_side);
    let accept_fut = acceptor.accept(server_side);

    let (connect_res, _) = tokio::join!(connect_fut, accept_fut);

    debug!("Retrieved Devolutions Gateway TLS public key");
    let tls_stream = connect_res.context("connect")?;

    let public_key = extract_tls_server_public_key(&tls_stream)
        .context("extract Devolutions Gateway TLS public key")?;

    Ok(public_key)
}

async fn handle<C, S>(proxy: RdpProxy<C, S>) -> Result<()>
where
    C: AsyncRead + AsyncWrite + Unpin + Send + Sync + 'static,
    S: AsyncRead + AsyncWrite + Unpin + Send + Sync + 'static,
{
    info!("RDP-TLS forwarding (credential injection) started");
    let RdpProxy {
        config,
        server_target,
        client_address,
        client_stream,
        server_stream,
        creds,
        username,
        password,
        client_stream_leftover_bytes,
        guard,
        session_id,
        report,
    } = proxy;

    let tls_config = config
        .tls
        .as_ref()
        .context("TLS configuration is required for credssp injection")?;

    let gateway_public_key_handle =
        retrieve_gateway_public_key(config.hostname.clone(), tls_config.acceptor.clone());
    debug!("Retrieved Devolutions Gateway TLS public key");

    let mut client_framed =
        ironrdp_tokio::TokioFramed::new_with_leftover(client_stream, client_stream_leftover_bytes);
    let mut server_framed = ironrdp_tokio::TokioFramed::new(server_stream);

    debug!("Created TokioFramed for client and server");
    let credential_mapping = AppCredentialMapping {
        proxy: AppCredential::UsernamePassword {
            username: creds.as_str().to_owned(),
            password: creds,
        },
        target: AppCredential::UsernamePassword { username, password },
    };

    debug!("Starting dual handshake until TLS upgrade");
    let hs = dual_handshake_until_tls_upgrade(
        &mut client_framed,
        &mut server_framed,
        &credential_mapping,
    )
    .await?;

    debug!("Dual handshake until TLS upgrade completed");

    let client_stream = client_framed.into_inner_no_leftover();
    let server_stream = server_framed.into_inner_no_leftover();
    debug!("Client and server streams created");
    // -- Perform the TLS upgrading for both the client and the server -- //
    let server_target = if server_target.contains(':') {
        server_target.split_once(':').unwrap().0.to_string()
    } else {
        server_target.clone()
    };

    let client_tls_upgrade_fut = tls_config.acceptor.accept(client_stream);
    let server_tls_upgrade_fut = crate::tls::connect(server_target.clone(), server_stream);

    debug!("TLS upgrade with client and server started");
    let (client_stream, server_stream) =
        tokio::join!(client_tls_upgrade_fut, server_tls_upgrade_fut);

    let client_stream = client_stream.context("TLS upgrade with client failed")?;
    let server_stream = server_stream.context("TLS upgrade with server failed")?;
    debug!("TLS upgrade with client and server completed");
    let server_public_key = extract_tls_server_public_key(&server_stream)
        .context("extract target server TLS public key")?;
    let gateway_public_key = gateway_public_key_handle.await?;

    let mut client_framed = ironrdp_tokio::TokioFramed::new(client_stream);
    let mut server_framed = ironrdp_tokio::TokioFramed::new(server_stream);

    let client_credssp_fut = perform_credssp_with_client(
        &mut client_framed,
        client_address.ip(),
        gateway_public_key,
        hs.client_security_protocol,
        &credential_mapping.proxy,
    );

    let server_credssp_fut = perform_credssp_with_server(
        &mut server_framed,
        server_target.clone(),
        server_public_key,
        hs.server_security_protocol,
        &credential_mapping.target,
    );

    let (client_credssp_res, server_credssp_res) =
        tokio::join!(client_credssp_fut, server_credssp_fut);
    client_credssp_res.context("CredSSP with client")?;
    server_credssp_res.context("CredSSP with server")?;
    // CredSSP is complete; credentials are no longer needed for the live RDP
    // stream. Zeroize them now rather than retaining them for the session.
    drop(credential_mapping);

    // -- Intercept the Connect Confirm PDU, to override the server_security_protocol field -- //

    intercept_connect_confirm(
        &mut client_framed,
        &mut server_framed,
        hs.server_security_protocol,
    )
    .await?;

    // -- Intercept the client's Client Info PDU to force server-side rendering
    //    performance flags (disable wallpaper, full-window drag, menu
    //    animations, theming). For an unguarded session this remains a
    //    best-effort optimization. A guarded session requires a clean
    //    activation boundary: after Client Info, both capability PDUs are
    //    pinned before any graphics bytes can enter the forwarding stage.
    if let Some(guard_config) = guard.as_ref() {
        intercept_client_info(&mut client_framed, &mut server_framed)
            .await
            .context("guarded RDP activation requires a decodable Client Info PDU")?;
        intercept_guarded_capabilities(&mut client_framed, &mut server_framed, guard_config)
            .await
            .context("pin guarded RDP capability negotiation")?;
    } else if let Err(e) = intercept_client_info(&mut client_framed, &mut server_framed).await {
        info!("RDP perf-flag injection skipped ({e:#}); forwarding client flags unchanged");
    }

    let (client_stream, client_leftover) = client_framed.into_inner();
    let (server_stream, server_leftover) = server_framed.into_inner();

    // `read_pdu` is cancel-safe by retaining read-ahead bytes in TokioFramed.
    // Preserve those bytes as prefixes for Proxy rather than writing them here:
    // server read-ahead must pass through the PII gate on guarded sessions.

    Proxy::builder()
        .transport_a(client_stream)
        .transport_b(server_stream)
        .initial_a_to_b(client_leftover)
        .initial_b_to_a(server_leftover)
        .session_id(session_id)
        .guard(guard)
        .report(report)
        .build()
        .forward()
        .await
        .context("RDP Tls traffic injection failed")?;

    Ok(())
}

#[instrument(
    name = "dual_handshake_until_tls_upgrade",
    level = "debug",
    skip_all,
    ret
)]
async fn dual_handshake_until_tls_upgrade<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
    mapping: &AppCredentialMapping,
) -> anyhow::Result<HandshakeResult>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    let (_, received_frame) = client_framed
        .read_pdu()
        .await
        .context("read PDU from client")?;
    let received_connection_request: x224::X224<nego::ConnectionRequest> =
        ironrdp_core::decode(&received_frame).context("decode PDU from client")?;

    // Choose the security protocol to use with the client.
    let client_security_protocol = if received_connection_request
        .0
        .protocol
        .contains(nego::SecurityProtocol::HYBRID_EX)
    {
        nego::SecurityProtocol::HYBRID_EX
    } else if received_connection_request
        .0
        .protocol
        .contains(nego::SecurityProtocol::HYBRID)
    {
        nego::SecurityProtocol::HYBRID
    } else {
        anyhow::bail!(
            "client does not support CredSSP (received {})",
            received_connection_request.0.protocol
        )
    };

    let connection_request_to_send = nego::ConnectionRequest {
        nego_data: match &mapping.target {
            AppCredential::UsernamePassword { username, .. } => {
                Some(nego::NegoRequestData::cookie(username.to_owned()))
            }
        },
        flags: received_connection_request.0.flags,
        // https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/902b090b-9cb3-4efc-92bf-ee13373371e3
        // The spec is stating that `PROTOCOL_SSL` "SHOULD" also be set when using `PROTOCOL_HYBRID`.
        // > PROTOCOL_HYBRID (0x00000002)
        // > Credential Security Support Provider protocol (CredSSP) (section 5.4.5.2).
        // > If this flag is set, then the PROTOCOL_SSL (0x00000001) flag SHOULD also be set
        // > because Transport Layer Security (TLS) is a subset of CredSSP.
        // However, crucially, it’s not strictly required (not "MUST").
        // In fact, we purposefully choose to not set `PROTOCOL_SSL` unless `enable_winlogon` is `true`.
        // This tells the server that we are not going to accept downgrading NLA to TLS security.
        protocol: nego::SecurityProtocol::HYBRID | nego::SecurityProtocol::HYBRID_EX,
    };
    send_pdu(server_framed, &x224::X224(connection_request_to_send))
        .await
        .context("send connection request to server")?;

    let (_, received_frame) = server_framed
        .read_pdu()
        .await
        .context("read PDU from server")?;
    let received_connection_confirm: x224::X224<nego::ConnectionConfirm> =
        ironrdp_core::decode(&received_frame).context("decode PDU from server")?;

    let (connection_confirm_to_send, handshake_result) = match &received_connection_confirm.0 {
        nego::ConnectionConfirm::Response {
            flags,
            protocol: server_security_protocol,
        } => {
            let result = if !server_security_protocol
                .intersects(nego::SecurityProtocol::HYBRID | nego::SecurityProtocol::HYBRID_EX)
            {
                Err(anyhow::anyhow!(
                    "server selected security protocol {server_security_protocol}, which is not supported for credential injection"
                ))
            } else {
                Ok(HandshakeResult {
                    client_security_protocol,
                    server_security_protocol: *server_security_protocol,
                })
            };

            (
                x224::X224(nego::ConnectionConfirm::Response {
                    flags: *flags,
                    protocol: client_security_protocol,
                }),
                result,
            )
        }
        nego::ConnectionConfirm::Failure { code } => (
            x224::X224(received_connection_confirm.0.clone()),
            Err(anyhow::anyhow!(
                "RDP session initiation failed with code {code}"
            )),
        ),
    };

    send_pdu(client_framed, &connection_confirm_to_send)
        .await
        .context("send connection confirm to client")?;

    handshake_result
}

#[derive(Zeroize, ZeroizeOnDrop)]
pub struct AppCredentialMapping {
    pub proxy: AppCredential,
    pub target: AppCredential,
}

#[derive(Zeroize)]
pub enum AppCredential {
    UsernamePassword {
        username: String,
        password: Zeroizing<String>,
    },
}

fn sspi_password(password: &str) -> ironrdp_connector::sspi::Secret<String> {
    // The allocation moves directly into SSPI's ZeroizeOnDrop wrapper. Never
    // construct an ordinary connector Credentials value: that type drops its
    // password String without scrubbing it after cloning into SSPI.
    ironrdp_connector::sspi::Secret::new(password.to_owned())
}

const _: fn() = || {
    fn assert_zeroize_on_drop<T: ZeroizeOnDrop>() {}
    assert_zeroize_on_drop::<ironrdp_connector::sspi::Secret<String>>();
    assert_zeroize_on_drop::<ironrdp_connector::sspi::Secret<Vec<u8>>>();
};

#[derive(Clone, Copy, Debug)]
struct CredsspTsRequestHint;

impl ironrdp_pdu::PduHint for CredsspTsRequestHint {
    fn find_size(&self, bytes: &[u8]) -> ironrdp_core::DecodeResult<Option<(bool, usize)>> {
        match ironrdp_connector::sspi::credssp::TsRequest::read_length(bytes) {
            Ok(length) => Ok(Some((true, length))),
            Err(error) if error.kind() == std::io::ErrorKind::UnexpectedEof => Ok(None),
            Err(error) => Err(ironrdp_core::other_err!(
                "CredsspTsRequestHint",
                source: error
            )),
        }
    }
}

#[derive(Clone, Copy, Debug)]
struct CredsspEarlyUserAuthResultHint;

impl ironrdp_pdu::PduHint for CredsspEarlyUserAuthResultHint {
    fn find_size(&self, _bytes: &[u8]) -> ironrdp_core::DecodeResult<Option<(bool, usize)>> {
        Ok(Some((
            true,
            ironrdp_connector::sspi::credssp::EARLY_USER_AUTH_RESULT_PDU_SIZE,
        )))
    }
}

const CREDSSP_TS_REQUEST_HINT: CredsspTsRequestHint = CredsspTsRequestHint;
const CREDSSP_EARLY_USER_AUTH_RESULT_HINT: CredsspEarlyUserAuthResultHint =
    CredsspEarlyUserAuthResultHint;

async fn perform_credssp_with_server<S>(
    framed: &mut ironrdp_tokio::Framed<S>,
    server_name: String,
    server_public_key: Vec<u8>,
    security_protocol: nego::SecurityProtocol,
    credentials: &AppCredential,
) -> anyhow::Result<()>
where
    S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
{
    use ironrdp_connector::sspi;
    use ironrdp_tokio::FramedWrite as _;

    let AppCredential::UsernamePassword { username, password } = credentials;
    let username = sspi::Username::new(username, None).context("invalid username")?;
    let credentials = sspi::AuthIdentity {
        username,
        password: sspi_password(password.as_str()),
    }
    .into();
    let service_principal_name = format!("TERMSRV/{server_name}");
    let protocol_config: Box<dyn sspi::negotiate::ProtocolConfig> =
        Box::<sspi::ntlm::NtlmConfig>::default();
    let client_mode = sspi::credssp::ClientMode::Negotiate(sspi::NegotiateConfig {
        protocol_config,
        package_list: None,
        client_computer_name: server_name,
    });
    let mut client = sspi::credssp::CredSspClient::new(
        server_public_key,
        credentials,
        sspi::credssp::CredSspMode::WithCredentials,
        client_mode,
        service_principal_name,
    )
    .context("initialize CredSSP client")?;
    let mut request = sspi::credssp::TsRequest::default();
    let mut buf = ironrdp_pdu::WriteBuf::new();

    loop {
        let mut generator = client.process(request);
        let state = generator
            .resolve_to_result()
            .context("sspi generator resolve")?;
        drop(generator);

        let (response, final_message) = match state {
            sspi::credssp::ClientState::ReplyNeeded(response) => (response, false),
            sspi::credssp::ClientState::FinalMessage(response) => (response, true),
        };
        buf.clear();
        let response_len = usize::from(response.buffer_len());
        response
            .encode_ts_request(buf.unfilled_to(response_len))
            .context("encode CredSSP request")?;
        buf.advance(response_len);
        framed
            .write_all(&buf[..response_len])
            .await
            .context("write CredSSP request")?;

        if final_message {
            if !security_protocol.contains(nego::SecurityProtocol::HYBRID_EX) {
                break;
            }
            let pdu = framed
                .read_by_hint(&CREDSSP_EARLY_USER_AUTH_RESULT_HINT)
                .await
                .context("read CredSSP early user authorization result")?;
            match sspi::credssp::EarlyUserAuthResult::from_buffer(&pdu[..])
                .context("decode CredSSP early user authorization result")?
            {
                sspi::credssp::EarlyUserAuthResult::Success => break,
                sspi::credssp::EarlyUserAuthResult::AccessDenied => {
                    anyhow::bail!("CredSSP early user authorization denied")
                }
            }
        }

        let pdu = framed
            .read_by_hint(&CREDSSP_TS_REQUEST_HINT)
            .await
            .context("read CredSSP request")?;
        request =
            sspi::credssp::TsRequest::from_buffer(&pdu[..]).context("decode CredSSP request")?;
    }

    Ok(())
}

#[instrument(level = "debug", ret, skip_all)]
async fn perform_credssp_with_client<S>(
    framed: &mut ironrdp_tokio::Framed<S>,
    client_addr: IpAddr,
    gateway_public_key: Vec<u8>,
    security_protocol: nego::SecurityProtocol,
    credentials: &AppCredential,
) -> anyhow::Result<()>
where
    S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
{
    use ironrdp_connector::sspi::credssp::EarlyUserAuthResult;
    use ironrdp_tokio::FramedWrite as _;

    let mut buf = ironrdp_pdu::WriteBuf::new();

    // Are we supposed to use the actual computer name of the client?
    // But this does not seem to matter so far, so we stringify the IP address of the client instead.
    let client_computer_name = ironrdp_connector::ServerName::new(client_addr.to_string());

    let result = credssp_loop(
        framed,
        &mut buf,
        client_computer_name,
        gateway_public_key,
        credentials,
    )
    .await;

    if security_protocol.intersects(nego::SecurityProtocol::HYBRID_EX) {
        let result = if result.is_ok() {
            EarlyUserAuthResult::Success
        } else {
            EarlyUserAuthResult::AccessDenied
        };

        buf.clear();
        result
            .to_buffer(&mut buf)
            .context("write early user auth result")?;
        let response = &buf[..result.buffer_len()];
        framed.write_all(response).await.context("write_all")?;
    }

    return result;

    #[instrument(level = "debug", ret, skip_all)]
    async fn credssp_loop<S>(
        framed: &mut ironrdp_tokio::Framed<S>,
        buf: &mut ironrdp_pdu::WriteBuf,
        client_computer_name: ironrdp_connector::ServerName,
        public_key: Vec<u8>,
        credentials: &AppCredential,
    ) -> anyhow::Result<()>
    where
        S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
    {
        let AppCredential::UsernamePassword { username, password } = credentials;
        debug!("Performing CredSSP with client");

        let username =
            ironrdp_connector::sspi::Username::parse(username).context("invalid username")?;

        let identity = ironrdp_connector::sspi::AuthIdentity {
            username,
            password: sspi_password(password.as_str()),
        };

        let mut sequence = ironrdp_acceptor::credssp::CredsspSequence::init(
            &identity,
            client_computer_name,
            public_key,
            None,
        )?;

        loop {
            let Some(next_pdu_hint) = sequence.next_pdu_hint()? else {
                break;
            };

            let pdu = framed
                .read_by_hint(next_pdu_hint)
                .await
                .map_err(|e| ironrdp_connector::custom_err!("read frame by hint", e))?;

            let Some(ts_request) = sequence.decode_client_message(&pdu)? else {
                break;
            };

            let result = sequence.process_ts_request(ts_request);
            buf.clear();
            let written = sequence.handle_process_result(result, buf)?;

            if let Some(response_len) = written.size() {
                let response = &buf[..response_len];
                framed
                    .write_all(response)
                    .await
                    .map_err(|e| ironrdp_connector::custom_err!("write all", e))?;
            }
        }

        Ok(())
    }
}

// ---------- Connect-Confirm patch (post CredSSP) ----------
#[instrument(level = "debug", ret, skip_all)]
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

fn extract_tls_server_public_key(tls_stream: &impl GetPeerCert) -> anyhow::Result<Vec<u8>> {
    use x509_cert::der::Decode as _;

    let cert = tls_stream
        .get_peer_certificate()
        .context("certificate is missing")?;

    let cert = x509_cert::Certificate::from_der(cert).context("parse X509 certificate")?;

    let server_public_key = cert
        .tbs_certificate
        .subject_public_key_info
        .subject_public_key
        .as_bytes()
        .context("subject public key BIT STRING is not aligned")?
        .to_owned();

    Ok(server_public_key)
}

#[derive(Debug)]
struct HandshakeResult {
    client_security_protocol: nego::SecurityProtocol,
    server_security_protocol: nego::SecurityProtocol,
}

/// Performance flags forced onto every client's Client Info PDU. These ask the
/// RDP server to skip rendering that produces large or frequent bitmap updates
/// with no information value for OCR: the desktop wallpaper, full-window drag
/// ghosting, menu fade/slide animations, and visual theming. Fewer/smaller
/// bitmap updates means less bandwidth and, when the PII guard is on, less
/// screen area to OCR per frame.
fn forced_performance_flags() -> ironrdp_pdu::rdp::client_info::PerformanceFlags {
    use ironrdp_pdu::rdp::client_info::PerformanceFlags;
    PerformanceFlags::DISABLE_WALLPAPER
        | PerformanceFlags::DISABLE_FULLWINDOWDRAG
        | PerformanceFlags::DISABLE_MENUANIMATIONS
        | PerformanceFlags::DISABLE_THEMING
}

/// Relays post-CredSSP PDUs from client to server, pumping server→client
/// traffic for pacing, until the client's Client Info PDU is seen. That PDU is
/// patched to OR-in [`forced_performance_flags`] and forwarded; every other PDU
/// is forwarded byte-for-byte unmodified. Returns as soon as the Client Info
/// PDU has been forwarded, leaving any buffered-ahead bytes in the framed
/// readers for the caller to preserve as forwarding prefixes.
///
/// Safety / cancellation: each PDU is fully read and then forwarded before the
/// next read, so on any error the streams are left on a clean PDU boundary and
/// the caller can fall back to transparent forwarding without desync. The
/// `select!` over two `read_pdu()` futures is sound because
/// `ironrdp_tokio`/`ironrdp_async` buffer bytes in the framed object, not in the
/// future: a `read_pdu` future dropped by `select!` retains any bytes already
/// read in the framed buffer, so cancelling the losing branch cannot lose data
/// or split a frame. `ironrdp_async::Framed::read_pdu` (v0.5.0) is explicitly
/// documented cancel-safe ("Data may have been read, but it will be stored in
/// the internal buffer"), as is the underlying `read_exact`. Only the single
/// Client Info PDU is ever re-encoded, and only when it round-trips losslessly
/// (see `patch_client_info_frame`); every other byte passes through verbatim.
async fn intercept_client_info<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
) -> Result<()>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    // Bound the search: the Client Info PDU arrives a handful of PDUs after the
    // MCS connect (erect domain, attach user, channel joins). If we somehow do
    // not see it within a sane number of client PDUs, give up and let
    // transparent forwarding take over rather than buffering indefinitely.
    const MAX_CLIENT_PDUS_BEFORE_INFO: usize = 32;

    let mut seen = 0usize;
    loop {
        tokio::select! {
            // Pace the handshake: relay any server→client PDU verbatim. The
            // client only emits its next request (and ultimately the Client
            // Info PDU) after these confirms.
            biased;
            server_read = server_framed.read_pdu() => {
                let (_, frame) = server_read.context("read server PDU during client-info intercept")?;
                client_framed.write_all(&frame).await.context("relay server PDU to client")?;
            }
            client_read = client_framed.read_pdu() => {
                let (_, frame) = client_read.context("read client PDU during client-info intercept")?;
                if let Some(patched) = patch_client_info_frame(&frame) {
                    server_framed.write_all(&patched).await.context("forward patched Client Info PDU")?;
                    info!("RDP: forced server-side performance flags (wallpaper/drag/anim/theming disabled)");
                    return Ok(());
                }
                // Not the Client Info PDU — forward verbatim.
                server_framed.write_all(&frame).await.context("relay client PDU to server")?;
                seen += 1;
                if seen > MAX_CLIENT_PDUS_BEFORE_INFO {
                    anyhow::bail!("Client Info PDU not seen within {MAX_CLIENT_PDUS_BEFORE_INFO} PDUs");
                }
            }
        }
    }
}

/// Relays the guarded activation sequence until both capability-bearing PDUs
/// have been losslessly rewritten: server Demand Active and client Confirm
/// Active. Every post-Client-Info frame must remain valid X224/MCS while this
/// runs; malformed input, unsupported/lossy capability encoding, or an
/// unexpected Fast-Path update fails the guarded session closed.
async fn intercept_guarded_capabilities<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
    guard: &crate::guard::GuardConfig,
) -> Result<()>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    const MAX_PDUS_PER_DIRECTION: usize = 128;

    let mut server_pinned = false;
    let mut client_pinned = false;
    let mut server_seen = 0usize;
    let mut client_seen = 0usize;

    loop {
        tokio::select! {
            biased;
            server_read = server_framed.read_pdu() => {
                let (_, frame) =
                    server_read.context("read server PDU during capability intercept")?;
                let patched = guard
                    .pin_server_capability_frame(&frame)
                    .context("pin Server Demand Active")?;
                if patched.is_some() {
                    server_pinned = true;
                }
                client_framed
                    .write_all(patched.as_deref().unwrap_or(frame.as_ref()))
                    .await
                    .context("relay server activation PDU to client")?;
                server_seen += 1;
                if server_seen > MAX_PDUS_PER_DIRECTION {
                    anyhow::bail!(
                        "Server Demand Active not seen within {MAX_PDUS_PER_DIRECTION} server PDUs"
                    );
                }
            }
            client_read = client_framed.read_pdu() => {
                let (_, frame) =
                    client_read.context("read client PDU during capability intercept")?;
                let patched = guard
                    .pin_client_capability_frame(&frame)
                    .context("pin Client Confirm Active")?;
                if patched.is_some() {
                    client_pinned = true;
                }
                server_framed
                    .write_all(patched.as_deref().unwrap_or(frame.as_ref()))
                    .await
                    .context("relay client activation PDU to server")?;
                client_seen += 1;
                if client_seen > MAX_PDUS_PER_DIRECTION {
                    anyhow::bail!(
                        "Client Confirm Active not seen within {MAX_PDUS_PER_DIRECTION} client PDUs"
                    );
                }
            }
        }

        if server_pinned && client_pinned {
            info!("RDP: guarded capability negotiation pinned to Fast-Path bitmaps");
            return Ok(());
        }
    }
}

/// If `frame` is an `X224(SendDataRequest)` whose user data is a Client Info
/// PDU, returns the re-encoded frame with [`forced_performance_flags`] OR-ed
/// in. Returns `None` for any frame that is not a Client Info PDU (which is
/// then forwarded verbatim), so a decode mismatch can never corrupt the stream.
fn patch_client_info_frame(frame: &[u8]) -> Option<Vec<u8>> {
    use ironrdp_pdu::rdp::ClientInfoPdu;
    use ironrdp_pdu::rdp::client_info::{ExtendedClientOptionalInfo, PerformanceFlags};
    use std::borrow::Cow;

    // X224 → MCS SendDataRequest (client→server data carrier).
    let x224: x224::X224<mcs::SendDataRequest<'_>> = ironrdp_core::decode(frame).ok()?;
    let sdr = x224.0;

    // SendDataRequest user data → Client Info PDU. Anything else (channel data,
    // other MCS messages) fails this decode and is left untouched.
    let mut info: ClientInfoPdu = ironrdp_core::decode(sdr.user_data.as_ref()).ok()?;

    // FIDELITY GUARD. We can only patch a PDU we can reproduce byte-for-byte
    // except for the flag bits. ironrdp's ExtendedClientOptionalInfo decode
    // captures only {timezone, session_id, performance_flags, reconnect_cookie}
    // and discards any trailing RDP-10+ reserved bytes; its encode emits only
    // those four. So a client that sends extra/trailing optional material would
    // have it dropped on re-encode. To never silently mutate the wire beyond the
    // flags, we re-encode the UNMODIFIED decoded PDU and require it to be
    // byte-identical to the original user data. If it is not (the model is lossy
    // for this exact frame), we decline and the caller forwards it verbatim.
    //
    // For the browser IronRDP web client (the only client today) the PDU is
    // exactly timezone+session_id+performance_flags, so this guard always
    // passes; it exists to keep any future/native client correct by refusing to
    // patch rather than corrupting.
    let reencoded_unmodified = ironrdp_core::encode_vec(&info).ok()?;
    if reencoded_unmodified.as_slice() != sdr.user_data.as_ref() {
        return None;
    }

    // The builder is order-constrained (timezone -> session_id ->
    // performance_flags -> reconnect_cookie). The fidelity guard above
    // guarantees these are exactly the fields present, so replaying them is
    // lossless. Missing timezone/session_id can't reach here (guard would have
    // failed), but we still bail defensively rather than invent values.
    let opt = &info.client_info.extra_info.optional_data;
    let timezone = opt.timezone()?.clone();
    let session_id = opt.session_id()?;
    let current = opt
        .performance_flags()
        .unwrap_or_else(PerformanceFlags::empty);
    let updated = current | forced_performance_flags();

    let builder = ExtendedClientOptionalInfo::builder()
        .timezone(timezone)
        .session_id(session_id)
        .performance_flags(updated);
    // reconnect_cookie is the last optional field; replay it if present.
    let rebuilt = match opt.reconnect_cookie() {
        Some(cookie) => builder.reconnect_cookie(*cookie).build(),
        None => builder.build(),
    };
    info.client_info.extra_info.optional_data = rebuilt;

    // Re-encode the Client Info PDU, rewrap it in a SendDataRequest with the
    // SAME initiator/channel ids, then in X224.
    let info_bytes = ironrdp_core::encode_vec(&info).ok()?;
    let rewrapped = mcs::SendDataRequest {
        initiator_id: sdr.initiator_id,
        channel_id: sdr.channel_id,
        user_data: Cow::Owned(info_bytes),
    };
    ironrdp_core::encode_vec(&x224::X224(rewrapped)).ok()
}

async fn send_pdu<S, P>(framed: &mut ironrdp_tokio::TokioFramed<S>, pdu: &P) -> Result<()>
where
    S: AsyncWrite + Unpin + Send + Sync,
    P: ironrdp_core::Encode,
{
    let payload = ironrdp_core::encode_vec(pdu).context("encode PDU")?;
    framed.write_all(&payload).await.context("write PDU")?;
    Ok(())
}

// utils if needed
trait GetPeerCert {
    fn get_peer_certificate(
        &self,
    ) -> Option<&tokio_rustls::rustls::pki_types::CertificateDer<'static>>;
}

impl<S> GetPeerCert for tokio_rustls::client::TlsStream<S> {
    fn get_peer_certificate(
        &self,
    ) -> Option<&tokio_rustls::rustls::pki_types::CertificateDer<'static>> {
        self.get_ref()
            .1
            .peer_certificates()
            .and_then(|certs| certs.first())
    }
}

impl<S> GetPeerCert for tokio_rustls::server::TlsStream<S> {
    fn get_peer_certificate(
        &self,
    ) -> Option<&tokio_rustls::rustls::pki_types::CertificateDer<'static>> {
        self.get_ref()
            .1
            .peer_certificates()
            .and_then(|certs| certs.first())
    }
}

#[cfg(test)]
mod credssp_tests {
    use super::*;

    #[tokio::test]
    async fn zeroizing_credssp_client_authenticates() {
        let (client_io, server_io) = tokio::io::duplex(64 * 1024);
        let mut client_framed = ironrdp_tokio::TokioFramed::new(client_io);
        let mut server_framed = ironrdp_tokio::TokioFramed::new(server_io);
        let public_key = vec![0x42; 64];
        let credentials = AppCredential::UsernamePassword {
            username: "hooptest".to_owned(),
            password: Zeroizing::new("secret".to_owned()),
        };

        let client = perform_credssp_with_server(
            &mut client_framed,
            "rdp-target".to_owned(),
            public_key.clone(),
            nego::SecurityProtocol::HYBRID,
            &credentials,
        );
        let server = async {
            let identity = ironrdp_connector::sspi::AuthIdentity {
                username: ironrdp_connector::sspi::Username::parse("hooptest")
                    .context("parse test username")?,
                password: sspi_password("secret"),
            };
            let mut sequence = ironrdp_acceptor::credssp::CredsspSequence::init(
                &identity,
                ironrdp_connector::ServerName::new("rdp-target".to_owned()),
                public_key,
                None,
            )?;
            let mut buf = ironrdp_pdu::WriteBuf::new();

            loop {
                let Some(hint) = sequence.next_pdu_hint()? else {
                    break;
                };
                let pdu = server_framed.read_by_hint(hint).await?;
                let Some(request) = sequence.decode_client_message(&pdu)? else {
                    break;
                };
                let state = sequence.process_ts_request(request);
                buf.clear();
                if let Some(response_len) = sequence.handle_process_result(state, &mut buf)?.size()
                {
                    server_framed.write_all(&buf[..response_len]).await?;
                }
            }
            anyhow::Ok(())
        };

        let (client_result, server_result) = tokio::join!(client, server);
        client_result.expect("zeroizing CredSSP client failed");
        server_result.expect("CredSSP server failed");
    }
}

#[cfg(test)]
mod perf_flags_tests {
    use super::*;
    use ironrdp_pdu::rdp::ClientInfoPdu;
    use ironrdp_pdu::rdp::client_info::{
        ClientInfo, ClientInfoFlags, CompressionType, Credentials, ExtendedClientInfo,
        ExtendedClientOptionalInfo, OptionalSystemTime, PerformanceFlags, TimezoneInfo,
    };
    use ironrdp_pdu::rdp::headers::{BasicSecurityHeader, BasicSecurityHeaderFlags};
    use std::borrow::Cow;

    fn timezone() -> TimezoneInfo {
        TimezoneInfo {
            bias: 0,
            standard_name: String::new(),
            standard_date: OptionalSystemTime(None),
            standard_bias: 0,
            daylight_name: String::new(),
            daylight_date: OptionalSystemTime(None),
            daylight_bias: 0,
        }
    }

    /// Builds a wire frame (`X224(SendDataRequest{ ClientInfoPdu })`) carrying a
    /// Client Info PDU with the given starting performance flags.
    fn client_info_frame(start: PerformanceFlags) -> Vec<u8> {
        let optional_data = ExtendedClientOptionalInfo::builder()
            .timezone(timezone())
            .session_id(0)
            .performance_flags(start)
            .build();
        let info = ClientInfoPdu {
            security_header: BasicSecurityHeader {
                flags: BasicSecurityHeaderFlags::INFO_PKT,
            },
            client_info: ClientInfo {
                credentials: Credentials {
                    username: "user".into(),
                    password: "pass".into(),
                    domain: None,
                },
                code_page: 0,
                flags: ClientInfoFlags::UNICODE | ClientInfoFlags::MOUSE,
                compression_type: CompressionType::K8,
                alternate_shell: String::new(),
                work_dir: String::new(),
                extra_info: ExtendedClientInfo {
                    address_family: ironrdp_pdu::rdp::client_info::AddressFamily::INET,
                    address: String::new(),
                    dir: String::new(),
                    optional_data,
                },
            },
        };
        let info_bytes = ironrdp_core::encode_vec(&info).unwrap();
        let sdr = mcs::SendDataRequest {
            initiator_id: 1007,
            channel_id: 1003,
            user_data: Cow::Owned(info_bytes),
        };
        ironrdp_core::encode_vec(&x224::X224(sdr)).unwrap()
    }

    fn decode_flags(frame: &[u8]) -> PerformanceFlags {
        let x224: x224::X224<mcs::SendDataRequest<'_>> = ironrdp_core::decode(frame).unwrap();
        let info: ClientInfoPdu = ironrdp_core::decode(x224.0.user_data.as_ref()).unwrap();
        info.client_info
            .extra_info
            .optional_data
            .performance_flags()
            .unwrap()
    }

    #[test]
    fn patches_perf_flags_into_client_info() {
        let frame = client_info_frame(PerformanceFlags::ENABLE_FONT_SMOOTHING);
        let patched = patch_client_info_frame(&frame).expect("should patch a Client Info PDU");
        let flags = decode_flags(&patched);
        // Forced flags are present.
        assert!(flags.contains(PerformanceFlags::DISABLE_WALLPAPER));
        assert!(flags.contains(PerformanceFlags::DISABLE_FULLWINDOWDRAG));
        assert!(flags.contains(PerformanceFlags::DISABLE_MENUANIMATIONS));
        assert!(flags.contains(PerformanceFlags::DISABLE_THEMING));
        // Pre-existing client flag is preserved (OR, not replace).
        assert!(flags.contains(PerformanceFlags::ENABLE_FONT_SMOOTHING));
    }

    #[test]
    fn preserves_credentials_and_channel_ids() {
        let frame = client_info_frame(PerformanceFlags::empty());
        let patched = patch_client_info_frame(&frame).expect("should patch");
        // Channel/initiator ids and credentials must survive the re-encode.
        let x224: x224::X224<mcs::SendDataRequest<'_>> = ironrdp_core::decode(&patched).unwrap();
        assert_eq!(x224.0.initiator_id, 1007);
        assert_eq!(x224.0.channel_id, 1003);
        let info: ClientInfoPdu = ironrdp_core::decode(x224.0.user_data.as_ref()).unwrap();
        assert_eq!(info.client_info.credentials.username, "user");
        assert_eq!(info.client_info.credentials.password, "pass");
    }

    #[test]
    fn idempotent_when_flags_already_set() {
        let frame = client_info_frame(forced_performance_flags());
        let patched = patch_client_info_frame(&frame).expect("should still decode/re-encode");
        assert_eq!(decode_flags(&patched), forced_performance_flags());
    }

    #[test]
    fn non_client_info_frame_is_left_untouched() {
        // An arbitrary (non-Client-Info) X224 SendDataRequest must not be
        // mistaken for a Client Info PDU — patch returns None so the caller
        // forwards it verbatim.
        let sdr = mcs::SendDataRequest {
            initiator_id: 1007,
            channel_id: 1003,
            user_data: Cow::Owned(vec![0xDE, 0xAD, 0xBE, 0xEF]),
        };
        let frame = ironrdp_core::encode_vec(&x224::X224(sdr)).unwrap();
        assert!(patch_client_info_frame(&frame).is_none());
    }

    #[test]
    fn garbage_frame_is_left_untouched() {
        assert!(patch_client_info_frame(&[0x00, 0x01, 0x02, 0x03]).is_none());
    }

    #[test]
    fn declines_when_reencode_would_be_lossy() {
        use ironrdp_pdu::rdp::ClientInfoPdu;
        // Build a canonical Client Info PDU, then append the RDP-10+ reserved
        // trailing bytes (reserved1/reserved2) that ironrdp's decoder CONSUMES
        // but its encoder does NOT re-emit. The fidelity guard must detect that
        // re-encoding cannot reproduce these bytes and decline to patch (so the
        // caller forwards the frame verbatim) rather than silently drop them.
        let optional_data = ExtendedClientOptionalInfo::builder()
            .timezone(timezone())
            .session_id(0)
            .performance_flags(PerformanceFlags::empty())
            .build();
        let info = ClientInfoPdu {
            security_header: BasicSecurityHeader {
                flags: BasicSecurityHeaderFlags::INFO_PKT,
            },
            client_info: ClientInfo {
                credentials: Credentials {
                    username: "user".into(),
                    password: "pass".into(),
                    domain: None,
                },
                code_page: 0,
                flags: ClientInfoFlags::UNICODE | ClientInfoFlags::MOUSE,
                compression_type: CompressionType::K8,
                alternate_shell: String::new(),
                work_dir: String::new(),
                extra_info: ExtendedClientInfo {
                    address_family: ironrdp_pdu::rdp::client_info::AddressFamily::INET,
                    address: String::new(),
                    dir: String::new(),
                    optional_data,
                },
            },
        };
        let mut info_bytes = ironrdp_core::encode_vec(&info).unwrap();
        // Append reserved1 + reserved2 (two u16) that decode reads and discards.
        info_bytes.extend_from_slice(&[0x00, 0x00, 0x00, 0x00]);
        let sdr = mcs::SendDataRequest {
            initiator_id: 1007,
            channel_id: 1003,
            user_data: Cow::Owned(info_bytes),
        };
        let frame = ironrdp_core::encode_vec(&x224::X224(sdr)).unwrap();
        assert!(
            patch_client_info_frame(&frame).is_none(),
            "a frame whose trailing bytes can't be reproduced must NOT be patched"
        );
    }
}
