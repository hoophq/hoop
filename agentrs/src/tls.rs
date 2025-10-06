use std::io;
use std::sync::{Arc, LazyLock};

use anyhow::Context as _;
use tokio_rustls::client::TlsStream;
use tokio_rustls::rustls::{self, pki_types};
use tracing::error;

// rustls doc says:
//
// > Making one of these can be expensive, and should be once per process rather than once per connection.
//
// source: https://docs.rs/rustls/0.21.1/rustls/client/struct.ClientConfig.html
//
// We’ll reuse the same TLS client config for all proxy-based TLS connections.
// (TlsConnector is just a wrapper around the config providing the `connect` method.)
pub static TLS_CONNECTOR: LazyLock<tokio_rustls::TlsConnector> = LazyLock::new(|| {
    let mut config = rustls::client::ClientConfig::builder()
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(danger::NoCertificateVerification))
        .with_no_client_auth();

    // Disable TLS resumption because it’s not supported by some services such as CredSSP.
    //
    // > The CredSSP Protocol does not extend the TLS wire protocol. TLS session resumption is not supported.
    //
    // source: https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-cssp/385a7489-d46b-464c-b224-f7340e308a5c
    config.resumption = rustls::client::Resumption::disabled();

    tokio_rustls::TlsConnector::from(Arc::new(config))
});

pub async fn connect<IO>(dns_name: String, stream: IO) -> io::Result<TlsStream<IO>>
where
    IO: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin,
{
    use tokio::io::AsyncWriteExt as _;

    let dns_name = pki_types::ServerName::try_from(dns_name).map_err(io::Error::other)?;

    let mut tls_stream = TLS_CONNECTOR.connect(dns_name, stream).await?;

    // > To keep it simple and correct, [TlsStream] will behave like `BufWriter`.
    // > For `TlsStream<TcpStream>`, this means that data written by `poll_write`
    // > is not guaranteed to be written to `TcpStream`.
    // > You must call `poll_flush` to ensure that it is written to `TcpStream`.
    //
    // source: https://docs.rs/tokio-rustls/latest/tokio_rustls/#why-do-i-need-to-call-poll_flush
    tls_stream.flush().await?;

    Ok(tls_stream)
}

pub enum CertificateSource {
    External {
        certificates: Vec<pki_types::CertificateDer<'static>>,
        private_key: pki_types::PrivateKeyDer<'static>,
    },
}

pub fn build_server_config(
    cert_source: CertificateSource,
    strict_checks: bool,
) -> anyhow::Result<rustls::ServerConfig> {
    let builder = rustls::ServerConfig::builder().with_no_client_auth();

    match cert_source {
        CertificateSource::External {
            certificates,
            private_key,
        } => {
            let first_certificate = certificates.first().context("empty certificate list")?;
            let (report, ok_r) = match check_certificate_now(first_certificate) {
                Ok(r) => (Some(r), true),
                Err(e) => {
                    error!("warning: failed to check the certificate: {e:?}");
                    (None, false)
                }
            };

            let report = report.unwrap_or_else(|| CertReport {
                serial_number: "<unknown>".to_string(),
                subject: picky::x509::name::DirectoryName::default(),
                issuer: picky::x509::name::DirectoryName::default(),
                not_before: picky::x509::date::UtcDate::now(),
                not_after: picky::x509::date::UtcDate::now(),
                issues: CertIssues::empty(),
            });

            if strict_checks
                && ok_r
                && report.issues.intersects(
                    CertIssues::MISSING_SERVER_AUTH_EXTENDED_KEY_USAGE
                        | CertIssues::MISSING_SUBJECT_ALT_NAME,
                )
            {
                let serial_number = report.serial_number;
                let subject = report.subject;
                let issuer = report.issuer;
                let not_before = report.not_before;
                let not_after = report.not_after;
                let issues = report.issues;

                anyhow::bail!(
                    "found significant issues with the certificate: serial_number = {serial_number}, subject = {subject}, issuer = {issuer}, not_before = {not_before}, not_after = {not_after}, issues = {issues} (you can set `TlsVerifyStrict` to `false` in the gateway.json configuration file if that's intended)"
                );
            }

            builder
                .with_single_cert(certificates, private_key)
                .context("failed to set server config cert")
        }
    }
}

pub struct CertReport {
    pub serial_number: String,
    pub subject: picky::x509::name::DirectoryName,
    pub issuer: picky::x509::name::DirectoryName,
    pub not_before: picky::x509::date::UtcDate,
    pub not_after: picky::x509::date::UtcDate,
    pub issues: CertIssues,
}

bitflags::bitflags! {
    #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
    pub struct CertIssues: u8 {
        const NOT_YET_VALID = 0b00000001;
        const EXPIRED = 0b00000010;
        const MISSING_SERVER_AUTH_EXTENDED_KEY_USAGE = 0b00000100;
        const MISSING_SUBJECT_ALT_NAME = 0b00001000;
    }
}

impl core::fmt::Display for CertIssues {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        bitflags::parser::to_writer(self, f)
    }
}

pub fn check_certificate_now(cert: &[u8]) -> anyhow::Result<CertReport> {
    check_certificate(cert, time::OffsetDateTime::now_utc())
}

pub fn check_certificate(cert: &[u8], at: time::OffsetDateTime) -> anyhow::Result<CertReport> {
    use anyhow::Context as _;
    use core::fmt::Write as _;

    let cert = picky::x509::Cert::from_der(cert).context("failed to parse certificate")?;
    let at = picky::x509::date::UtcDate::from(at);

    let mut issues = CertIssues::empty();

    let serial_number = cert
        .serial_number()
        .0
        .iter()
        .fold(String::new(), |mut acc, byte| {
            let _ = write!(acc, "{byte:X?}");
            acc
        });
    let subject = cert.subject_name();
    let issuer = cert.issuer_name();
    let not_before = cert.valid_not_before();
    let not_after = cert.valid_not_after();

    if at < not_before {
        issues.insert(CertIssues::NOT_YET_VALID);
    } else if not_after < at {
        issues.insert(CertIssues::EXPIRED);
    }

    let mut has_server_auth_key_purpose = false;
    let mut has_san = false;

    for ext in cert.extensions() {
        match ext.extn_value() {
            picky::x509::extension::ExtensionView::ExtendedKeyUsage(eku)
                if eku.contains(picky::oids::kp_server_auth()) =>
            {
                has_server_auth_key_purpose = true;
            }
            picky::x509::extension::ExtensionView::SubjectAltName(_) => has_san = true,
            _ => {}
        }
    }

    if !has_server_auth_key_purpose {
        issues.insert(CertIssues::MISSING_SERVER_AUTH_EXTENDED_KEY_USAGE);
    }

    if !has_san {
        issues.insert(CertIssues::MISSING_SUBJECT_ALT_NAME);
    }

    Ok(CertReport {
        serial_number,
        subject,
        issuer,
        not_before,
        not_after,
        issues,
    })
}

pub mod danger {
    use tokio_rustls::rustls::client::danger::{
        HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier,
    };
    use tokio_rustls::rustls::{DigitallySignedStruct, Error, SignatureScheme, pki_types};

    #[derive(Debug)]
    pub struct NoCertificateVerification;

    impl ServerCertVerifier for NoCertificateVerification {
        fn verify_server_cert(
            &self,
            _: &pki_types::CertificateDer<'_>,
            _: &[pki_types::CertificateDer<'_>],
            _: &pki_types::ServerName<'_>,
            _: &[u8],
            _: pki_types::UnixTime,
        ) -> Result<ServerCertVerified, Error> {
            Ok(ServerCertVerified::assertion())
        }

        fn verify_tls12_signature(
            &self,
            _: &[u8],
            _: &pki_types::CertificateDer<'_>,
            _: &DigitallySignedStruct,
        ) -> Result<HandshakeSignatureValid, Error> {
            Ok(HandshakeSignatureValid::assertion())
        }

        fn verify_tls13_signature(
            &self,
            _: &[u8],
            _: &pki_types::CertificateDer<'_>,
            _: &DigitallySignedStruct,
        ) -> Result<HandshakeSignatureValid, Error> {
            Ok(HandshakeSignatureValid::assertion())
        }

        fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
            vec![
                SignatureScheme::RSA_PKCS1_SHA1,
                SignatureScheme::ECDSA_SHA1_Legacy,
                SignatureScheme::RSA_PKCS1_SHA256,
                SignatureScheme::ECDSA_NISTP256_SHA256,
                SignatureScheme::RSA_PKCS1_SHA384,
                SignatureScheme::ECDSA_NISTP384_SHA384,
                SignatureScheme::RSA_PKCS1_SHA512,
                SignatureScheme::ECDSA_NISTP521_SHA512,
                SignatureScheme::RSA_PSS_SHA256,
                SignatureScheme::RSA_PSS_SHA384,
                SignatureScheme::RSA_PSS_SHA512,
                SignatureScheme::ED25519,
                SignatureScheme::ED448,
            ]
        }
    }
}
