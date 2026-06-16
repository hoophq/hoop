//! RDP capability / update-stream probe.
//!
//! Connects to a real RDP server using the *same* IronRDP `ClientConnector`
//! path the browser web client uses, completes the full connection sequence
//! (X.224 negotiation -> TLS -> CredSSP -> capability exchange), then reads the
//! server's update stream for a fixed window and classifies every Fast-Path
//! update PDU by its `UpdateCode`.
//!
//! The purpose is to answer one empirical question for a given server:
//!
//!   Does this server deliver screen content as plain Fast-Path **Bitmap**
//!   updates (which the PII gate's rewriter handles), or as **SurfaceCommands**
//!   / RemoteFX (which it does not)?
//!
//! We deliberately set `bitmap: None` so the connector falls back to
//! `client_codecs_capabilities(&[])` -- i.e. RemoteFX advertised "on" -- which
//! is the worst case and matches a stock `ironrdp-web` build that does not
//! override `config.bitmap`. If a server still sends only Bitmap updates under
//! that config, it is strong evidence (not a proof) that the bitmap-only
//! rewriter is sufficient for that server/workload.
//!
//! IMPORTANT: this only samples the *observed* Fast-Path update stream for a
//! fixed window. It classifies top-level `UpdateCode`s; it does not parse
//! SurfaceCommands payloads, does not enumerate every possible rendering path,
//! and a server could behave differently for other workloads or later in a
//! session. Treat the output as a confidence signal, not a guarantee.
//!
//! Usage:
//!   cargo run --example rdp_caps_probe -- <host:port> <user> <password> [seconds]
//!
//! NOTE: the password is passed as a CLI argument for convenience as a dev
//! tool; it will be visible in process listings and shell history. Use a
//! throwaway test account only.

use std::collections::BTreeMap;
use std::net::SocketAddr;
use std::time::{Duration, Instant};

use anyhow::Context as _;
use ironrdp_async::{connect_begin, connect_finalize, mark_as_upgraded};
use ironrdp_connector::{ClientConnector, Config, Credentials, DesktopSize, ServerName};
use ironrdp_core::{Decode as _, ReadCursor};
use ironrdp_pdu::fast_path::{FastPathHeader, FastPathUpdatePdu, UpdateCode};
use ironrdp_pdu::gcc::KeyboardType;
use ironrdp_pdu::rdp::capability_sets::MajorPlatformType;
use ironrdp_pdu::FAST_PATH_HINT;
use agentrs::tls;
use tokio::net::TcpStream;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt().with_max_level(tracing::Level::WARN).init();

    let mut args = std::env::args().skip(1);
    let addr: String = args.next().context("usage: <host:port> <user> <pass> [secs]")?;
    let username = args.next().context("missing <user>")?;
    let password = args.next().context("missing <password>")?;
    let secs: u64 = args.next().map(|s| s.parse().unwrap_or(8)).unwrap_or(8);

    let sock: SocketAddr = tokio::net::lookup_host(&addr)
        .await
        .with_context(|| format!("resolve {addr}"))?
        .next()
        .context("no address resolved")?;
    // Derive the host for SNI / CredSSP server-name. Handle IPv6 literals
    // (`[::1]:3389`), `host:port`, and bare hosts. Fall back to the resolved
    // IP when the input has no separable host component.
    let host = if let Some(rest) = addr.strip_prefix('[') {
        // IPv6 literal: take everything up to the closing bracket.
        rest.split(']').next().unwrap_or(rest).to_string()
    } else if addr.matches(':').count() == 1 {
        // Exactly one colon => host:port.
        addr.rsplit_once(':').map(|(h, _)| h).unwrap_or(&addr).to_string()
    } else if addr.contains(':') {
        // Multiple colons, no brackets => bare IPv6 address.
        addr.clone()
    } else {
        addr.clone()
    };

    println!("== RDP capability probe ==");
    println!("target      : {addr} ({sock})");
    println!("user        : {username}");
    println!("codec config: bitmap=None  (=> client_codecs_capabilities(&[]), RemoteFX advertised ON)");
    println!("listen window: {secs}s\n");

    // Mirror the ironrdp-web client config. `bitmap: None` is the key choice:
    // it makes create_client_confirm_active fall back to the default codec set
    // (RemoteFX on), which is the worst case for our bitmap-only rewriter.
    let config = Config {
        desktop_size: DesktopSize { width: 1280, height: 800 },
        desktop_scale_factor: 0,
        bitmap: None,
        enable_tls: true,
        enable_credssp: true,
        credentials: Credentials::UsernamePassword {
            username: username.clone(),
            password,
        },
        domain: None,
        client_build: 0,
        client_name: "hoop-caps-probe".to_owned(),
        keyboard_type: KeyboardType::IbmEnhanced,
        keyboard_subtype: 0,
        keyboard_functional_keys_count: 12,
        keyboard_layout: 0,
        ime_file_name: String::new(),
        dig_product_id: String::new(),
        client_dir: String::new(),
        platform: MajorPlatformType::UNSPECIFIED,
        hardware_id: None,
        request_data: None,
        autologon: false,
        no_audio_playback: true,
        license_cache: None,
        no_server_pointer: false,
        pointer_software_rendering: false,
        performance_flags: Default::default(),
    };

    let tcp = TcpStream::connect(sock).await.context("tcp connect")?;
    let client_addr = tcp.local_addr().context("local addr")?;
    let mut framed = ironrdp_tokio::TokioFramed::new(tcp);

    let mut connector = ClientConnector::new(config, client_addr);

    // Phase 1: X.224 negotiation up to the point a TLS upgrade is required.
    let should_upgrade = connect_begin(&mut framed, &mut connector)
        .await
        .context("connect_begin (X.224 negotiation)")?;
    println!("[ok] X.224 negotiation complete; security upgrade requested");

    // Phase 2: TLS upgrade on the raw stream, then capture the server pubkey
    // for CredSSP channel binding.
    let initial = framed.into_inner_no_leftover();
    let tls_stream = tls::connect(host.clone(), initial)
        .await
        .context("tls upgrade")?;
    let server_public_key = extract_pubkey(&tls_stream)?;
    let mut framed = ironrdp_tokio::TokioFramed::new(tls_stream);
    let upgraded = mark_as_upgraded(should_upgrade, &mut connector);
    println!("[ok] TLS established; server public key captured ({} bytes)", server_public_key.len());

    // Phase 3: CredSSP + capability exchange to a fully connected session.
    let result = connect_finalize(
        upgraded,
        &mut framed,
        connector,
        ServerName::new(host),
        server_public_key,
        None,
        None,
    )
    .await
    .context("connect_finalize (CredSSP + activation)")?;

    println!("[ok] CONNECTED. negotiated desktop = {}x{}, io_channel={}",
        result.desktop_size.width, result.desktop_size.height, result.io_channel_id);
    println!("\n-- listening to server update stream for {secs}s --\n");

    // Phase 4: classify the update stream.
    let mut counts: BTreeMap<&'static str, u64> = BTreeMap::new();
    let deadline = Instant::now() + Duration::from_secs(secs);

    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            break;
        }
        let read = tokio::time::timeout(remaining, framed.read_by_hint(&FAST_PATH_HINT)).await;
        let pdu = match read {
            Ok(Ok(p)) => p,
            Ok(Err(e)) => {
                println!("[warn] read error (stream ended?): {e}");
                break;
            }
            Err(_) => break, // window elapsed
        };
        classify(&pdu, &mut counts);
    }

    print_report(&counts);
    Ok(())
}

fn classify(pdu: &[u8], counts: &mut BTreeMap<&'static str, u64>) {
    let mut cursor = ReadCursor::new(pdu);
    let header = match FastPathHeader::decode(&mut cursor) {
        Ok(h) => h,
        Err(_) => {
            *counts.entry("<undecodable header>").or_default() += 1;
            return;
        }
    };
    let _ = header;
    match FastPathUpdatePdu::decode(&mut cursor) {
        Ok(update) => {
            let label = match update.update_code {
                UpdateCode::Orders => "Orders",
                UpdateCode::Bitmap => "Bitmap  <-- rewriter handles",
                UpdateCode::Palette => "Palette",
                UpdateCode::Synchronize => "Synchronize",
                UpdateCode::SurfaceCommands => "SurfaceCommands  <-- NOT handled",
                UpdateCode::HiddenPointer => "HiddenPointer",
                UpdateCode::DefaultPointer => "DefaultPointer",
                UpdateCode::PositionPointer => "PositionPointer",
                UpdateCode::ColorPointer => "ColorPointer",
                UpdateCode::CachedPointer => "CachedPointer",
                UpdateCode::NewPointer => "NewPointer",
                UpdateCode::LargePointer => "LargePointer",
            };
            *counts.entry(label).or_default() += 1;
        }
        Err(_) => {
            *counts.entry("<undecodable update>").or_default() += 1;
        }
    }
}

fn print_report(counts: &BTreeMap<&'static str, u64>) {
    println!("\n== update PDU classification ==");
    if counts.is_empty() {
        println!("(no update PDUs received -- try interacting with the session / longer window)");
        return;
    }
    let total: u64 = counts.values().sum();
    for (k, v) in counts {
        println!("  {:>8}  {:5.1}%  {}", v, 100.0 * (*v as f64) / total as f64, k);
    }
    println!("  -------- total {total}");

    let bitmap = counts.iter().filter(|(k, _)| k.starts_with("Bitmap")).map(|(_, v)| *v).sum::<u64>();
    let surface = counts.iter().filter(|(k, _)| k.starts_with("SurfaceCommands")).map(|(_, v)| *v).sum::<u64>();
    println!("\n== observation (this sample window only) ==");
    if surface > 0 {
        println!("UNSUPPORTED PATH OBSERVED: {surface} SurfaceCommands update(s) in the sample.");
        println!("  SurfaceCommands bypass the bitmap-only rewriter, so PII delivered this way");
        println!("  could render unredacted. This server/workload needs capability pinning");
        println!("  (RD-239 live wiring) before redact mode can be trusted against it.");
    } else if bitmap > 0 {
        println!("BITMAP-ONLY OBSERVED: saw Bitmap updates and no SurfaceCommands in the window.");
        println!("  Encouraging signal that the bitmap rewriter is sufficient for this");
        println!("  server/workload. NOT a proof: this only samples observed top-level update");
        println!("  codes and does not enumerate every possible rendering path. Re-run with");
        println!("  representative interaction before relying on redact for a given target.");
    } else {
        println!("INCONCLUSIVE: no Bitmap or SurfaceCommands seen in the window.");
        println!("  Interact with the session (move a window) and/or raise the window.");
    }
}

fn extract_pubkey<S>(tls_stream: &tokio_rustls::client::TlsStream<S>) -> anyhow::Result<Vec<u8>> {
    use x509_cert::der::Decode as _;
    let (_, conn) = tls_stream.get_ref();
    let certs = conn
        .peer_certificates()
        .context("no peer certificates")?;
    let end_entity = certs.first().context("empty certificate chain")?;
    let cert = x509_cert::Certificate::from_der(end_entity.as_ref())
        .context("parse X509 certificate")?;
    let key = cert
        .tbs_certificate
        .subject_public_key_info
        .subject_public_key
        .as_bytes()
        .context("subject public key BIT STRING not aligned")?
        .to_owned();
    Ok(key)
}
