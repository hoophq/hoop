use anyhow::{Context as _, Result};
use ironrdp_pdu::{mcs, nego, x224};
use serde::Deserialize;
use std::net::IpAddr;
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt};
use tracing::debug;
use tracing::info;
use tracing::instrument;
use typed_builder::TypedBuilder;

use ironrdp_tokio::FramedWrite as _;

use crate::conf;
use crate::proxy::Proxy;

#[derive(TypedBuilder)]
pub struct RdpProxy<C, S> {
    config: Arc<conf::Conf>,
    creds: String,
    username: String,
    password: String,
    server_target: String,
    client_address: SocketAddr,
    client_stream: C,
    server_stream: S,
    #[allow(dead_code)]
    client_stream_leftover_bytes: bytes::BytesMut,
}

#[derive(Debug, Deserialize)]
pub struct AppCredentialMapping {
    #[serde(rename = "proxy_credential")]
    pub proxy: AppCredential,
    #[serde(rename = "target_credential")]
    pub target: AppCredential,
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
            username: creds.to_string(),
            password: creds.to_string(),
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

    // -- Intercept the Connect Confirm PDU, to override the server_security_protocol field -- //

    intercept_connect_confirm(
        &mut client_framed,
        &mut server_framed,
        hs.server_security_protocol,
    )
    .await?;

    let (mut client_stream, client_leftover) = client_framed.into_inner();
    let (mut server_stream, server_leftover) = server_framed.into_inner();

    // -- here we will forwarding -- //

    info!("RDP-TLS forwarding (credential injection)");

    client_stream
        .write_all(&server_leftover)
        .await
        .context("write server leftover to client")?;

    server_stream
        .write_all(&client_leftover)
        .await
        .context("write client leftover to server")?;

    Proxy::builder()
        .transport_a(client_stream)
        .transport_b(server_stream)
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

#[derive(Clone, Debug, Deserialize)]
pub enum AppCredential {
    UsernamePassword { username: String, password: String },
}

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
    use ironrdp_tokio::FramedWrite as _;

    let credentials = match credentials {
        AppCredential::UsernamePassword { username, password } => {
            ironrdp_connector::Credentials::UsernamePassword {
                username: username.clone(),
                password: password.to_string().to_owned(),
            }
        }
    };

    let (mut sequence, mut ts_request) = ironrdp_connector::credssp::CredsspSequence::init(
        credentials,
        None,
        security_protocol,
        ironrdp_connector::ServerName::new(server_name),
        server_public_key,
        None,
    )?;

    let mut buf = ironrdp_pdu::WriteBuf::new();

    loop {
        let mut generator = sequence.process_ts_request(ts_request);
        let client_state = generator
            .resolve_to_result()
            .context("sspi generator resolve")?;
        drop(generator);

        buf.clear();
        let written = sequence.handle_process_result(client_state, &mut buf)?;

        if let Some(response_len) = written.size() {
            let response = &buf[..response_len];
            framed
                .write_all(response)
                .await
                .map_err(|e| ironrdp_connector::custom_err!("write all", e))?;
        }

        let Some(next_pdu_hint) = sequence.next_pdu_hint() else {
            break;
        };

        let pdu = framed
            .read_by_hint(next_pdu_hint)
            .await
            .context("read frame by hint")?;

        if let Some(next_request) = sequence.decode_server_message(&pdu)? {
            ts_request = next_request;
        } else {
            break;
        }
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
            password: password.to_string().to_owned().into(),
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
