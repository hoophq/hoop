use anyhow::{Context, Result};
use camino::Utf8PathBuf;
use rcgen::{
    Certificate, CertificateParams, DistinguishedName, DnType, ExtendedKeyUsagePurpose, IsCa,
    KeyUsagePurpose, SanType,
};
use std::fs;
use std::time::{Duration, SystemTime};
use tokio_rustls::rustls::pki_types;

/// Configuration for generating a self-signed certificate
#[derive(Debug, Clone)]
pub struct CertConfig {
    /// Common Name (CN) for the certificate
    pub common_name: String,
    /// Subject Alternative Names (SAN) - DNS names
    pub dns_names: Vec<String>,
    /// Subject Alternative Names (SAN) - IP addresses
    pub ip_addresses: Vec<std::net::IpAddr>,
    /// Certificate validity duration in days (default: 365 days)
    pub validity_days: u32,
    /// RSA key size in bits (default: 2048)
    pub key_size: u32,
}

// Default configuration for a gateway certificate
impl Default for CertConfig {
    fn default() -> Self {
        Self {
            common_name: "gateway.hoop".to_string(),
            dns_names: vec!["gateway.hoop".to_string()],
            ip_addresses: vec![],
            validity_days: 365,
            key_size: 2048,
        }
    }
}

/// Generated certificate and private key pair
#[derive(Debug)]
pub struct CertKeyPair {
    /// PEM-encoded certificate
    pub certificate_pem: String,
    /// PEM-encoded private key
    pub private_key_pem: String,
    /// Certificate in DER format for rustls
    pub certificate_der: Vec<u8>,
    /// Private key in DER format for rustls
    pub private_key_der: Vec<u8>,
}

impl CertKeyPair {
    pub fn save_to_files(&self, cert_path: &Utf8PathBuf, key_path: &Utf8PathBuf) -> Result<()> {
        fs::write(cert_path, &self.certificate_pem)
            .with_context(|| format!("Failed to write certificate to {}", cert_path))?;

        fs::write(key_path, &self.private_key_pem)
            .with_context(|| format!("Failed to write private key to {}", key_path))?;

        Ok(())
    }

    /// Convert to rustls-compatible types
    pub fn to_rustls(
        &self,
    ) -> (
        pki_types::CertificateDer<'static>,
        pki_types::PrivateKeyDer<'static>,
    ) {
        let cert = pki_types::CertificateDer::from(self.certificate_der.clone());
        let key = pki_types::PrivateKeyDer::Pkcs8(self.private_key_der.clone().into());
        (cert, key)
    }
}

/// Generate a self-signed certificate and private key
pub fn generate_self_signed_cert(config: CertConfig) -> Result<CertKeyPair> {
    // Create distinguished name
    let mut distinguished_name = DistinguishedName::new();
    distinguished_name.push(DnType::CommonName, &config.common_name);

    // Set subject alternative names
    let mut san_names = Vec::new();

    // Add DNS names
    for dns_name in &config.dns_names {
        san_names.push(SanType::DnsName(dns_name.clone()));
    }

    // Add IP addresses
    for ip_addr in &config.ip_addresses {
        san_names.push(SanType::IpAddress(*ip_addr));
    }

    // Set validity period
    let now = SystemTime::now();
    let not_after = now + Duration::from_secs(config.validity_days as u64 * 24 * 60 * 60);

    // Create certificate parameters
    let mut params = CertificateParams::default();
    params.distinguished_name = distinguished_name;
    params.subject_alt_names = san_names;
    params.not_before = now.into();
    params.not_after = not_after.into();

    // Set key usage extensions
    params.key_usages = vec![
        KeyUsagePurpose::DigitalSignature,
        KeyUsagePurpose::KeyEncipherment,
    ];

    // Set extended key usage
    params.extended_key_usages = vec![ExtendedKeyUsagePurpose::ServerAuth];

    // Set basic constraints (not a CA)
    params.is_ca = IsCa::NoCa;

    // Generate the certificate
    let cert = Certificate::from_params(params).context("Failed to generate certificate")?;

    // Get PEM encodings
    let certificate_pem = cert
        .serialize_pem()
        .context("Failed to serialize certificate to PEM")?;
    let private_key_pem = cert.serialize_private_key_pem();

    // Get DER encodings for rustls compatibility
    let certificate_der = cert
        .serialize_der()
        .context("Failed to serialize certificate to DER")?;

    let private_key_der = cert.serialize_private_key_der();

    Ok(CertKeyPair {
        certificate_pem,
        private_key_pem,
        certificate_der,
        private_key_der,
    })
}

/// Generate a self-signed certificate with default settings for gateway.hoop
pub fn generate_gateway_cert(ip_addresses: Vec<std::net::IpAddr>) -> Result<CertKeyPair> {
    let config = CertConfig {
        common_name: "gateway.hoop".to_string(),
        dns_names: vec!["gateway.hoop".to_string()],
        ip_addresses,
        validity_days: 365,
        key_size: 2048,
    };

    generate_self_signed_cert(config)
}

/// Generate a self-signed certificate and save it to the specified paths
pub fn generate_and_save_cert(
    config: CertConfig,
    cert_path: &Utf8PathBuf,
    key_path: &Utf8PathBuf,
) -> Result<CertKeyPair> {
    let cert_key_pair = generate_self_signed_cert(config)?;
    cert_key_pair.save_to_files(cert_path, key_path)?;
    Ok(cert_key_pair)
}

/// Generate a gateway certificate and save it to the specified paths
pub fn generate_and_save_gateway_cert(
    ip_addresses: Vec<std::net::IpAddr>,
    cert_path: &Utf8PathBuf,
    key_path: &Utf8PathBuf,
) -> Result<CertKeyPair> {
    let cert_key_pair = generate_gateway_cert(ip_addresses)?;
    cert_key_pair.save_to_files(cert_path, key_path)?;
    Ok(cert_key_pair)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::IpAddr;

    #[test]
    fn test_generate_default_cert() {
        let config = CertConfig::default();
        let result = generate_self_signed_cert(config);
        assert!(result.is_ok());

        let cert_key_pair = result.unwrap();
        assert!(!cert_key_pair.certificate_pem.is_empty());
        assert!(!cert_key_pair.private_key_pem.is_empty());
        assert!(cert_key_pair.certificate_pem.contains("BEGIN CERTIFICATE"));
        assert!(cert_key_pair.private_key_pem.contains("BEGIN PRIVATE KEY"));
    }

    #[test]
    fn test_generate_gateway_cert_with_ips() {
        let ip_addresses = vec![
            "127.0.0.1".parse::<IpAddr>().unwrap(),
            "::1".parse::<IpAddr>().unwrap(),
        ];

        let result = generate_gateway_cert(ip_addresses);
        assert!(result.is_ok());

        let cert_key_pair = result.unwrap();
        assert!(!cert_key_pair.certificate_pem.is_empty());
        assert!(!cert_key_pair.private_key_pem.is_empty());
    }

    #[test]
    fn test_cert_to_rustls() {
        let config = CertConfig::default();
        let cert_key_pair = generate_self_signed_cert(config).unwrap();
        let (cert, key) = cert_key_pair.to_rustls();

        // Verify the types are correct for rustls
        match key {
            pki_types::PrivateKeyDer::Pkcs8(_) => {}
            _ => panic!("Expected PKCS8 private key"),
        }
    }
}
