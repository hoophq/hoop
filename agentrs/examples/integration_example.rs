use agentrs::certs::x509::{CertKeyPair, generate_gateway_cert};
use agentrs::conf::ConfigHandleManager;
use camino::Utf8PathBuf;
use std::net::IpAddr;

/// Example showing how to integrate certificate generation with the existing config system
fn main() -> anyhow::Result<()> {
    println!("Integration example: Certificate generation with config system");

    // Initialize the config manager (this would normally be done in your application)
    let config_manager = ConfigHandleManager::init()?;

    // Get the hostname from config
    let hostname = &config_manager.conf.hostname;
    println!("Hostname from config: {}", hostname);

    // Generate a certificate for the configured hostname
    let ip_addresses = vec!["127.0.0.1".parse::<IpAddr>()?, "::1".parse::<IpAddr>()?];

    let cert_key_pair = generate_gateway_cert(ip_addresses)?;

    // Save the certificate to the data directory
    let data_dir = Utf8PathBuf::from("/Users/chico/.hoop");
    let cert_path = data_dir.join("server.crt");
    let key_path = data_dir.join("server.key");

    cert_key_pair.save_to_files(&cert_path, &key_path)?;

    println!("Certificate generated and saved:");
    println!("  Certificate: {}", cert_path);
    println!("  Private Key: {}", key_path);

    // Convert to rustls types for use with TLS
    let (cert_der, key_der) = cert_key_pair.to_rustls();

    println!("\nCertificate details:");
    println!("  Subject: CN=gateway.hoop");
    println!("  Issuer: CN=gateway.hoop (Self-signed)");
    println!("  Validity: 1 year from now");
    println!("  Key Algorithm: RSA 2048-bit");
    println!("  Key Usage: Digital Signature, Key Encipherment");
    println!("  Extended Key Usage: Server Authentication");
    println!("  Basic Constraints: Not a CA");

    // The certificate and key are now ready to be used with rustls
    // You can pass cert_der and key_der to your TLS configuration

    Ok(())
}
