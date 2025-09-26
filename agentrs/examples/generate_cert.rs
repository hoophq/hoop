use agentrs::certs::x509::{CertConfig, generate_gateway_cert, generate_self_signed_cert};
use camino::Utf8PathBuf;
use std::net::IpAddr;

// this is a simple example showing how to generate a self-signed certificate
// for use with the hoop gateway server
fn main() -> anyhow::Result<()> {
    println!("Generating self-signed certificate for gateway.hoop...");

    // Example 1: Generate a simple gateway certificate
    let ip_addresses = vec!["127.0.0.1".parse::<IpAddr>()?, "::1".parse::<IpAddr>()?];

    let cert_key_pair = generate_gateway_cert(ip_addresses)?;

    println!("Certificate generated successfully!");
    println!("Certificate PEM:\n{}", cert_key_pair.certificate_pem);
    println!("Private Key PEM:\n{}", cert_key_pair.private_key_pem);

    // Example 2: Generate with custom configuration
    let custom_config = CertConfig {
        common_name: "custom.hoop".to_string(),
        dns_names: vec!["custom.hoop".to_string(), "localhost".to_string()],
        ip_addresses: vec!["192.168.1.100".parse::<IpAddr>()?],
        validity_days: 730, // 2 years
        key_size: 2048,
    };

    let custom_cert = generate_self_signed_cert(custom_config)?;

    println!("\nCustom certificate generated successfully!");
    println!("Custom Certificate PEM:\n{}", custom_cert.certificate_pem);

    // Example 3: Save to files
    let cert_path = Utf8PathBuf::from("server.crt");
    let key_path = Utf8PathBuf::from("server.key");

    cert_key_pair.save_to_files(&cert_path, &key_path)?;
    println!("\nCertificate and key saved to files:");
    println!("  Certificate: {}", cert_path);
    println!("  Private Key: {}", key_path);

    // Example 4: Convert to rustls types
    let (cert_der, key_der) = cert_key_pair.to_rustls();
    println!("\nCertificate converted to rustls-compatible types");
    println!("  Certificate DER length: {} bytes", cert_der.len());
    println!("  Private Key type: {:?}", std::mem::discriminant(&key_der));

    Ok(())
}
