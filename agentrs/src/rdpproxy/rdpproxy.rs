// rdpproxy.rs
use anyhow::{Context as _, Result};
use ironrdp_core::{Decode, Encode};
use ironrdp_pdu::rdp::headers::BasicSecurityHeader;
use ironrdp_pdu::{mcs, nego, x224};
use secrecy::{ExposeSecret, SecretString};
use std::net::{IpAddr, Ipv4Addr};
use std::sync::Arc;
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt};
use typed_builder::TypedBuilder;

use ironrdp_tokio::FramedWrite as _;

use crate::tls;
use crate::{proxy::proxy::*, token::Claims};

// ---------- proxy type ----------

#[derive(TypedBuilder)]
pub struct RdpProxy<C, S> {
    client_stream: C,
    server_stream: S,
    #[allow(dead_code)]
    claims: Option<Claims>,
    client_stream_leftover_bytes: bytes::BytesMut,
}

// ---------- minimal TLS conf wrappers ----------

pub struct TlsConf {
    pub acceptor: tokio_rustls::TlsAcceptor,
}

pub struct Conf {
    #[allow(dead_code)]
    pub hostname: String,
    pub tls: Option<TlsConf>,
}

// ---------- public entry ----------

impl<A, B> RdpProxy<A, B>
where
    A: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
    B: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
{
    pub async fn run(self) -> Result<()> {
        handle(self).await
    }
}

// ---------- main flow ----------

async fn handle<C, S>(proxy: RdpProxy<C, S>) -> Result<()>
where
    C: AsyncRead + AsyncWrite + Unpin + Send + Sync + 'static,
    S: AsyncRead + AsyncWrite + Unpin + Send + Sync + 'static,
{
    // IMPORTANT: tls::build_self_signed_acceptor() must return (TlsAcceptor, gateway_public_key)
    let (acceptor, gateway_pubkey) = tls::build_self_signed_acceptor()?;

    let conf = Arc::new(Conf {
        hostname: "10.211.55.6".into(),
        tls: Some(TlsConf { acceptor }),
    });

    let RdpProxy {
        client_stream,
        server_stream,
        claims: _,
        client_stream_leftover_bytes,
    } = proxy;

    let gw = tls::gateway_tls()?; // <-- one global instance
    let acceptor = gw.acceptor.clone();
    let gateway_pubkey_spki = gw.spki.clone(); // <-- exact SPKI we presented

    // X.224 framing before any TLS
    let mut client_framed =
        ironrdp_tokio::TokioFramed::new_with_leftover(client_stream, client_stream_leftover_bytes);
    let mut server_framed = ironrdp_tokio::TokioFramed::new(server_stream);

    // do the initial nego pass-through to learn selected protocol
    let hs = dual_handshake_until_connect_confirm_for_passthrough(
        &mut client_framed,
        &mut server_framed,
    )
    .await?;

    println!(
        "Client selected security protocol: {:?}",
        hs.client_security_protocol
    );

    // if server selected any TLS/NLA, we MITM TLS; otherwise try classic rewrite pre-TLS
    if hs.server_security_protocol.intersects(
        nego::SecurityProtocol::SSL
            | nego::SecurityProtocol::HYBRID
            | nego::SecurityProtocol::HYBRID_EX,
    ) {
        eprintln!(
            "Negotiated TLS/NLA ({:?}); falling back to TLS MITM + CredSSP if needed",
            hs.server_security_protocol
        );

        // unwrap to raw TCP
        let client_tcp = client_framed.into_inner_no_leftover();
        let server_tcp = server_framed.into_inner_no_leftover();

        let client_tls = acceptor.accept(client_tcp).await?;
        // TLS-upgrade both legs
        let server_tls = crate::tls::connect("10.211.55.6".to_string(), server_tcp)
            .await
            .context("TLS upgrade with server failed")?;

        let server_pubkey_spki =
            extract_spki_from_tls(&server_tls).context("extract upstream SPKI")?;
        // frame TLS streams
        let mut client_tls_framed = ironrdp_tokio::TokioFramed::new(client_tls);
        let mut server_tls_framed = ironrdp_tokio::TokioFramed::new(server_tls);

        if hs
            .server_security_protocol
            .intersects(nego::SecurityProtocol::HYBRID | nego::SecurityProtocol::HYBRID_EX)
        {
            // HYBRID/HYBRID_EX → run CredSSP on both sides
            let upstream_creds = AppCredential::UsernamePassword {
                username: "chico".into(),
                password: SecretString::new("xxxxx".into()),
            };

            let (cli_res, srv_res) = tokio::join!(
                perform_credssp_with_client(
                    &mut client_tls_framed,
                    IpAddr::V4(Ipv4Addr::LOCALHOST),
                    gateway_pubkey_spki.clone(), // <-- MUST be this SPKI
                    hs.client_security_protocol
                ),
                perform_credssp_with_server(
                    &mut server_tls_framed,
                    "10.211.55.6".to_string(),
                    server_pubkey_spki, // <-- extracted from upstream TLS
                    hs.server_security_protocol,
                    upstream_creds
                ),
            );

            if let Err(e) = cli_res {
                anyhow::bail!("CredSSP client-side failed: {e:#}");
            }
            if let Err(e) = srv_res {
                anyhow::bail!("CredSSP server-side failed: {e:#}");
            }
            // reflect selected protocol for consistency
            intercept_connect_confirm(
                &mut client_tls_framed,
                &mut server_tls_framed,
                hs.server_security_protocol,
            )
            .await
            .context("intercept connect confirm")?;
        } else {
            // SSL only (TLS, no CredSSP): rewrite ClientInfo inside this TLS leg
            pump_until_rewrite_client_info_tls(
                &mut client_tls_framed,
                &mut server_tls_framed,
                Some("MYDOMAIN"),
                "chico",
                "xxx",
            )
            .await
            .context("rewrite ClientInfo inside TLS leg")?;
        }

        // unwrap and forward the rest
        let (mut client_stream, client_leftover) = client_tls_framed.into_inner();
        let (mut server_stream, server_leftover) = server_tls_framed.into_inner();

        if !server_leftover.is_empty() {
            client_stream.write_all(&server_leftover).await?;
        }
        if !client_leftover.is_empty() {
            server_stream.write_all(&client_leftover).await?;
        }

        Proxy::builder()
            .transport_a(client_stream)
            .transport_b(server_stream)
            .build()
            .forward()
            .await
            .context("RDP-TLS proxy forward")?;

        Ok(())
    } else {
        use ironrdp_pdu::rdp::headers::{BasicSecurityHeader, BasicSecurityHeaderFlags};
        use ironrdp_pdu::{mcs, x224};
        use std::borrow::Cow;
        // ---- put this inside your "no TLS" else-branch ----
        println!("Negotiated Standard RDP (no TLS); trying plaintext ClientInfo rewrite...");

        // we'll try to discover the global channel from the server's ConnectResponse
        let mut global_channel_id: Option<u16> = None;
        const MAX_PDUS: usize = 256;
        let mut rewritten = false;

        for _ in 0..MAX_PDUS {
            tokio::select! {
                // -------- server → client --------
                srv = server_framed.read_pdu() => {
                    let (_act, buf) = srv?;
                    let srv_bytes = buf.as_ref();

                    // Try to parse the server frame as "raw X.224 payload"
                    if global_channel_id.is_none() {
                        if let Ok(x_any) = ironrdp_core::decode::<x224::X224<x224::X224Data<'_>>>(srv_bytes) {
                            // mcs::ConnectResponse is BER-encoded (legacy), decode from user data:
                            if let Ok(conn_resp) = ironrdp_core::decode::<mcs::ConnectResponse>(&x_any.0.data) {
                                let g = conn_resp.global_channel_id();
                                eprintln!("Learned global channel id: {}", g);
                                global_channel_id = Some(g);
                            }
                        }
                    }

                    // forward to client
                    client_framed.write_all(srv_bytes).await?;
                }

                // -------- client → server --------
                cli = client_framed.read_pdu() => {
                    let (_act, buf) = cli?;
                    let cli_bytes = buf.as_ref();

                    // We need to find an MCS SendDataRequest on the *global* channel
                    if let Ok(mut x) = ironrdp_core::decode::<x224::X224<mcs::McsMessage<'_>>>(cli_bytes) {
                        if let mcs::McsMessage::SendDataRequest(sdr) = &mut x.0 {
                            // Prefer learned channel; fallback to 1003 if we still don't know it
                            let target_channel = global_channel_id.unwrap_or(1003);
                            if sdr.channel_id == target_channel {
                                let payload = sdr.user_data.as_ref();

                                // If the payload begins with a BasicSecurityHeader, check ENCRYPT
                                let encrypted = match ironrdp_core::decode::<BasicSecurityHeader>(payload) {
                                    Ok(bsh) => bsh.flags.contains(BasicSecurityHeaderFlags::ENCRYPT),
                                    Err(_) => false, // not a BSH first; try decode ClientInfo directly
                                };

                                if !encrypted {
                                    // Try to decode ClientInfoPdu and rewrite
                                    if let Ok(mut ci) = ironrdp_core::decode::<ironrdp_pdu::rdp::ClientInfoPdu>(payload) {
                                        if !ci.security_header.flags.contains(BasicSecurityHeaderFlags::ENCRYPT) {
                                            // ---- actual rewrite here ----
                                            ci.client_info.credentials.domain = None; // or Some("MYDOMAIN")
                                            ci.client_info.credentials.username = "chico".to_string();
                                            ci.client_info.credentials.password = "xxxxx".to_string();

                                            // Re-encode CI and replace SDR user_data
                                            if let Ok(patched) = ironrdp_core::encode_vec(&ci) {
                                                sdr.user_data = Cow::Owned(patched);

                                                // Re-encode whole X.224<McsMessage> and send upstream
                                                let patched_x224 = ironrdp_core::encode_vec(&x)?;
                                                server_framed.write_all(&patched_x224).await?;
                                                eprintln!("ClientInfo rewritten on channel {}.", target_channel);
                                                rewritten = true;
                                                break;
                                            }
                                        }
                                    }
                                } else {
                                    eprintln!("ClientInfo already encrypted by Standard RDP Security; cannot rewrite.");
                                }
                            }
                        }
                    }

                    // Not a rewriteable packet; forward as-is
                    server_framed.write_all(cli_bytes).await?;
                }
            }
        }

        if !rewritten {
            eprintln!(
                "Did not rewrite ClientInfo (likely encrypted or not observed within {MAX_PDUS} PDUs)."
            );
        }

        // unwrap and forward forever
        let (mut client_stream, client_leftover) = client_framed.into_inner();
        let (mut server_stream, server_leftover) = server_framed.into_inner();

        if !server_leftover.is_empty() {
            client_stream.write_all(&server_leftover).await?;
        }
        if !client_leftover.is_empty() {
            server_stream.write_all(&client_leftover).await?;
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
}

fn extract_spki_from_tls<S>(tls: &tokio_rustls::client::TlsStream<S>) -> Result<Vec<u8>>
where
    S: 'static, // satisfy rustls' lifetime expectations
{
    use x509_cert::der::Decode as _;

    // First cert in the peer chain is the end-entity
    let ee = tls
        .get_ref()
        .1
        .peer_certificates()
        .and_then(|cs| cs.first())
        .context("upstream TLS presented no certificate")?;

    let x509 = x509_cert::Certificate::from_der(ee.as_ref()).context("parse upstream X.509")?;

    let spki = x509
        .tbs_certificate
        .subject_public_key_info
        .subject_public_key
        .as_bytes()
        .context("unaligned SPKI bit string")?
        .to_vec();

    Ok(spki)
}
// ---------- CredSSP (server side / upstream) ----------

#[derive(Clone)]
pub enum AppCredential {
    UsernamePassword {
        username: String,
        password: SecretString,
    },
}

async fn perform_credssp_with_server<S>(
    framed: &mut ironrdp_tokio::Framed<S>,
    server_name: String,
    server_public_key: Vec<u8>,
    security_protocol: nego::SecurityProtocol,
    credentials: AppCredential,
) -> Result<()>
where
    S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
{
    use ironrdp_tokio::FramedWrite as _;

    let connector_creds = match credentials {
        AppCredential::UsernamePassword { username, password } => {
            ironrdp_connector::Credentials::UsernamePassword {
                username,
                password: password.expose_secret().to_owned(),
            }
        }
    };

    let (mut sequence, mut ts_request) = ironrdp_connector::credssp::CredsspSequence::init(
        connector_creds,
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
        if let Some(len) = written.size() {
            framed
                .write_all(&buf[..len])
                .await
                .map_err(|e| ironrdp_connector::custom_err!("write all", e))?;
        }

        let Some(next_hint) = sequence.next_pdu_hint() else {
            break;
        };
        let pdu = framed
            .read_by_hint(next_hint)
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

// ---------- CredSSP (client side / acceptor) ----------

#[allow(clippy::unit_arg)]
async fn perform_credssp_with_client<S>(
    framed: &mut ironrdp_tokio::Framed<S>,
    client_addr: IpAddr,
    gateway_public_key: Vec<u8>,
    security_protocol: nego::SecurityProtocol,
) -> Result<()>
where
    S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
{
    use ironrdp_connector::sspi::credssp::EarlyUserAuthResult;
    use ironrdp_connector::sspi::{AuthIdentity, Secret, Username};
    use ironrdp_tokio::FramedWrite as _;

    let mut buf = ironrdp_pdu::WriteBuf::new();
    let client_computer_name = ironrdp_connector::ServerName::new(client_addr.to_string());

    async fn credssp_loop<S>(
        framed: &mut ironrdp_tokio::Framed<S>,
        buf: &mut ironrdp_pdu::WriteBuf,
        client_computer_name: ironrdp_connector::ServerName,
        gateway_public_key: Vec<u8>,
    ) -> Result<()>
    where
        S: ironrdp_tokio::FramedRead + ironrdp_tokio::FramedWrite,
    {
        let placeholder_user = Username::new("chico", Some("MYDOMAIN"))?;
        let identity = AuthIdentity {
            username: placeholder_user,
            password: Secret::new(String::new()).into(),
        };

        let mut sequence = ironrdp_acceptor::credssp::CredsspSequence::init(
            &identity,
            client_computer_name,
            gateway_public_key,
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
            if let Some(len) = written.size() {
                framed
                    .write_all(&buf[..len])
                    .await
                    .map_err(|e| ironrdp_connector::custom_err!("write all", e))?;
            }
        }

        Ok(())
    }

    let res = credssp_loop(framed, &mut buf, client_computer_name, gateway_public_key).await;

    if security_protocol.intersects(nego::SecurityProtocol::HYBRID_EX) {
        let result = if res.is_ok() {
            EarlyUserAuthResult::Success
        } else {
            EarlyUserAuthResult::AccessDenied
        };
        buf.clear();
        result.to_buffer(&mut buf)?;
        let response = &buf[..result.buffer_len()];
        framed.write_all(response).await?;
    }

    res
}

// ---------- Connect-Confirm patch (post CredSSP) ----------

async fn intercept_connect_confirm<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
    server_security_protocol: nego::SecurityProtocol,
) -> Result<()>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    use ironrdp_tokio::{FramedRead as _, FramedWrite as _};

    const MAX_SCAN: usize = 128;
    for _ in 0..MAX_SCAN {
        tokio::select! {
            srv = server_framed.read_pdu() => {
                let (_act, buf) = srv?;
                client_framed.write_all(buf.as_ref()).await?;
            }
            cli = client_framed.read_pdu() => {
                let (_act, buf) = cli?;
                let bytes = buf.as_ref();

                let maybe_x224 = ironrdp_core::decode::<x224::X224<x224::X224Data<'_>>>(bytes);

                if let Ok(x_data) = maybe_x224 {
                    if let Ok(mut connect_initial) = ironrdp_core::decode::<mcs::ConnectInitial>(&x_data.0.data) {
                        connect_initial
                            .conference_create_request
                            .gcc_blocks
                            .core
                            .optional_data
                            .server_selected_protocol = Some(server_security_protocol);

                        let x224_payload = ironrdp_core::encode_vec(&connect_initial)?;
                        let pdu = x224::X224Data { data: std::borrow::Cow::Owned(x224_payload) };
                        send_pdu(server_framed, &x224::X224(pdu)).await?;
                        return Ok(());
                    }
                }

                server_framed.write_all(bytes).await?;
            }
        }
    }

    Ok(())
}

// ---------- TLS-only ClientInfo rewrite pump ----------

async fn pump_until_rewrite_client_info_tls<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
    new_domain: Option<&str>,
    new_username: &str,
    new_password: &str,
) -> Result<()>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    use ironrdp_tokio::{FramedRead as _, FramedWrite as _};
    const MAX_PDUS: usize = 256;

    for _ in 0..MAX_PDUS {
        tokio::select! {
            srv = server_framed.read_pdu() => {
                let (_act, buf) = srv?;
                client_framed.write_all(buf.as_ref()).await?;
            }
            cli = client_framed.read_pdu() => {
                let (_act, buf) = cli?;
                let bytes = buf.as_ref();

                if let Some(new_x224) = rewrite_client_info_unencrypted(
                    bytes, new_domain, new_username, new_password
                ) {
                    server_framed.write_all(&new_x224).await?;
                    return Ok(());
                } else {
                    server_framed.write_all(bytes).await?;
                }
            }
        }
    }
    Ok(())
}

// ---------- helpers ----------

fn extract_tls_server_public_key<S>(tls: &tokio_rustls::client::TlsStream<S>) -> Result<Vec<u8>>
where
    S: 'static,
{
    use x509_cert::der::Decode as _;

    let cert = tls
        .get_ref()
        .1
        .peer_certificates()
        .and_then(|cs| cs.first())
        .context("server sent no certificate")?;

    let cert =
        x509_cert::Certificate::from_der(cert.as_ref()).context("parse upstream X509 cert")?;

    Ok(cert
        .tbs_certificate
        .subject_public_key_info
        .subject_public_key
        .as_bytes()
        .context("unaligned SPKI bit string")?
        .to_vec())
}

fn rewrite_client_info_unencrypted(
    x224_bytes: &[u8],
    new_domain: Option<&str>, // e.g., Some("MYDOMAIN") or None for local account
    new_username: &str,       // e.g., "chico"
    new_password: &str,       // e.g., ""
) -> Option<Vec<u8>> {
    use ironrdp_pdu::mcs;
    use ironrdp_pdu::rdp::headers::{BasicSecurityHeader, BasicSecurityHeaderFlags};
    use std::borrow::Cow;

    // Decode the whole X.224 as an MCS message
    let mut x: x224::X224<mcs::McsMessage<'_>> = ironrdp_core::decode(x224_bytes).ok()?;

    // We only care about a SendDataRequest on the *global* channel (1003)
    let sdr = match &mut x.0 {
        mcs::McsMessage::SendDataRequest(sdr) if sdr.channel_id == 1003 => sdr,
        _ => return None,
    };
    // The user_data inside SDR should contain: BasicSecurityHeader + ClientInfoPdu (for Standard RDP)
    let payload = sdr.user_data.as_ref();

    // If BasicSecurityHeader has ENCRYPT, plaintext rewrite is not possible
    if let Ok(bsh) = ironrdp_core::decode::<BasicSecurityHeader>(payload) {
        if bsh.flags.contains(BasicSecurityHeaderFlags::ENCRYPT) {
            // ClientInfo exists but is encrypted → can't rewrite here
            return None;
        }
    }

    // Decode the ClientInfoPdu
    let mut ci: ironrdp_pdu::rdp::ClientInfoPdu = ironrdp_core::decode(payload).ok()?;
    if ci
        .security_header
        .flags
        .contains(BasicSecurityHeaderFlags::ENCRYPT)
    {
        return None;
    }

    // Patch credentials
    ci.client_info.credentials.domain = new_domain.map(str::to_string);
    ci.client_info.credentials.username = new_username.to_string();
    ci.client_info.credentials.password = new_password.to_string();

    // Re-encode the ClientInfoPdu and stick it back into SDR
    let patched = ironrdp_core::encode_vec(&ci).ok()?;
    sdr.user_data = Cow::Owned(patched);

    // Re-encode X.224<McsMessage>
    ironrdp_core::encode_vec(&x).ok()
}

#[derive(Debug)]
struct HandshakeResult {
    client_security_protocol: nego::SecurityProtocol,
    server_security_protocol: nego::SecurityProtocol,
}

async fn dual_handshake_until_connect_confirm_for_passthrough<C, S>(
    client_framed: &mut ironrdp_tokio::TokioFramed<C>,
    server_framed: &mut ironrdp_tokio::TokioFramed<S>,
) -> Result<HandshakeResult>
where
    C: AsyncWrite + AsyncRead + Unpin + Send + Sync,
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    // client → proxy: ConnectionRequest
    let (_, client_frame) = client_framed.read_pdu().await?;
    let client_cr: x224::X224<nego::ConnectionRequest> =
        ironrdp_core::decode(&client_frame).context("decode client ConnectionRequest")?;

    // request the same class of protocol the client asked for
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

    // proxy → server: ConnectionRequest
    let cr_to_server = nego::ConnectionRequest {
        nego_data: client_cr.0.nego_data.clone(),
        flags: client_cr.0.flags,
        protocol: effective,
    };
    send_pdu(server_framed, &x224::X224(cr_to_server)).await?;

    // server → proxy: ConnectionConfirm
    let (_, server_frame) = server_framed.read_pdu().await?;
    let server_cc: x224::X224<nego::ConnectionConfirm> =
        ironrdp_core::decode(&server_frame).context("decode server ConnectionConfirm")?;

    // proxy → client: forward CC
    send_pdu(client_framed, &server_cc).await?;

    // learn selected protocol
    let selected = match &server_cc.0 {
        nego::ConnectionConfirm::Response { protocol, .. } => *protocol,
        _ => nego::SecurityProtocol::empty(),
    };

    Ok(HandshakeResult {
        client_security_protocol: effective,
        server_security_protocol: selected,
    })
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

// (optional) convenient trait if you need it elsewhere
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
