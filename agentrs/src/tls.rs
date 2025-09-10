use anyhow::{Context, Result};
use rcgen::{Certificate, CertificateParams, SanType};
use std::net::IpAddr;
use std::sync::Arc;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio_rustls::rustls::pki_types::{
    CertificateDer, PrivateKeyDer, PrivatePkcs8KeyDer, ServerName,
};
use tokio_rustls::rustls::{ClientConfig, RootCertStore, ServerConfig};
use tokio_rustls::{TlsAcceptor, TlsConnector};

use tokio_rustls::rustls;
use once_cell::sync::Lazy;

pub struct GatewayTls {
    pub acceptor: TlsAcceptor,
    pub spki: Vec<u8>, // SubjectPublicKeyInfo DER
}

// Build exactly once, reuse forever.
static GATEWAY_TLS: Lazy<Result<GatewayTls>> = Lazy::new(|| {
    // 1) Generate a cert/key (or load from files)
    let mut params = rcgen::CertificateParams::new(vec![
        "proxy.local".into(),
        "localhost".into(),
    ]);
    params
        .subject_alt_names
        .push(rcgen::SanType::IpAddress("127.0.0.1".parse().unwrap()));

    let cert = rcgen::Certificate::from_params(params).context("rcgen build")?;
    let cert_der = tokio_rustls::rustls::pki_types::CertificateDer::from(cert.serialize_der()?);
    let key_der  = tokio_rustls::rustls::pki_types::PrivateKeyDer::Pkcs8(
        tokio_rustls::rustls::pki_types::PrivatePkcs8KeyDer::from(cert.serialize_private_key_der())
    );

    // 2) Build rustls server config
    let server_cfg = ServerConfig::builder()
        .with_no_client_auth()
        .with_single_cert(vec![cert_der.clone()], key_der)
        .context("with_single_cert")?;

    // 3) Extract SPKI (channel binding) from rcgen keypair (SPKI DER)
    // rcgen’s public_key_der() returns SubjectPublicKeyInfo DER
    let spki = cert.get_key_pair().public_key_der().to_vec();

    Ok(GatewayTls {
        acceptor: TlsAcceptor::from(Arc::new(server_cfg)),
        spki,
    })
});

pub fn gateway_tls() -> Result<&'static GatewayTls> {
    GATEWAY_TLS.as_ref().map_err(|e| anyhow::anyhow!("{e:#}"))
}

pub fn build_self_signed_acceptor() -> Result<(TlsAcceptor, Vec<u8>)> {
    let mut params = CertificateParams::new(vec!["proxy.local".into(), "localhost".into()]);
    params
        .subject_alt_names
        .push(SanType::IpAddress("127.0.0.1".parse().unwrap()));

    let cert = Certificate::from_params(params).context("rcgen")?;

    let cert_der_vec = cert.serialize_der().context("serialize der")?;
    let cert_der: CertificateDer<'static> = CertificateDer::from(cert_der_vec.clone());
    let key_der = PrivatePkcs8KeyDer::from(cert.serialize_private_key_der());
    let key: PrivateKeyDer<'static> = PrivateKeyDer::Pkcs8(key_der);

    // Extract SPKI/public-key bytes (for CredSSP channel binding)
    let spki = {
        use x509_cert::der::Decode;
        let x = x509_cert::Certificate::from_der(&cert_der_vec).context("parse cert der")?;
        x.tbs_certificate
            .subject_public_key_info
            .subject_public_key
            .as_bytes()
            .context("unaligned SPKI")?
            .to_vec()
    };

    let server_cfg = ServerConfig::builder()
        .with_no_client_auth()
        .with_single_cert(vec![cert_der], key)
        .context("with_single_cert")?;

    Ok((TlsAcceptor::from(Arc::new(server_cfg)), spki))
}

/// TLS client connector (proxy -> server) using the OS trust store.
fn client_config_with_native_roots() -> Result<rustls::ClientConfig> {
    let mut roots = RootCertStore::empty();

    // rustls-native-certs 0.7 returns an iterator of rustls::Certificate (Vec<u8> DER inside)
    for cert in rustls_native_certs::load_native_certs().context("load native root certs")? {
        // cert.0 is a Cow<[u8]>. Convert to pki_types::CertificateDer and add.
        let der = CertificateDer::from(cert.into_owned());
        let _ = roots.add(der); // ignore dup/add errors
    }

    Ok(ClientConfig::builder()
        .with_root_certificates(roots)
        .with_no_client_auth())
}

/// (Testing only) Insecure client config that accepts any server certificate.
#[allow(dead_code)]
fn client_config_insecure() -> rustls::ClientConfig {
    use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
    use rustls::pki_types;
    use rustls::{DigitallySignedStruct, Error, SignatureScheme};

    #[derive(Debug)]
    struct NoVerifier;
    impl ServerCertVerifier for NoVerifier {
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
                SignatureScheme::RSA_PKCS1_SHA256,
                SignatureScheme::ECDSA_NISTP256_SHA256,
                SignatureScheme::RSA_PSS_SHA256,
                SignatureScheme::ED25519,
            ]
        }
    }

    ClientConfig::builder()
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(NoVerifier))
        .with_no_client_auth()
}

/// Convert a string to rustls `ServerName`, supporting both DNS names and IP literals.
fn to_server_name(name: &str) -> Result<ServerName<'static>> {
    // IP literal?
    if let Ok(ip) = name.parse::<IpAddr>() {
        return Ok(ServerName::IpAddress(ip.into()));
    }
    // DNS name
    Ok(ServerName::try_from(name.to_owned())
        .map_err(|_| anyhow::anyhow!("invalid DNS name for SNI: {name}"))?)
}

/// Establish a TLS client connection to `serv
pub async fn connect<IO>(
    server_name: String,
    io: IO,
) -> anyhow::Result<tokio_rustls::client::TlsStream<IO>>
where
    IO: AsyncRead + AsyncWrite + Unpin + Send + 'static,
{
    // For production: verify against system roots
    // let client_config = client_config_with_native_roots()?;

    // For testing if the upstream cert/SAN isn't right yet:
    let client_config = client_config_insecure(); // <— DO NOT SHIP

    let connector = TlsConnector::from(Arc::new(client_config));
    let sni = to_server_name(&server_name)?; // handles IP or DNS
    Ok(connector.connect(sni, io).await.context("tls connect")?)
}
