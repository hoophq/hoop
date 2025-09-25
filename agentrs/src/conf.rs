use std::fs::File;
use std::io::BufReader;
use std::sync::Arc;
use std::{fmt, net::IpAddr};

use anyhow::Context;
use camino::{Utf8Path, Utf8PathBuf};
use picky::pem::Pem;
use serde::{Deserialize, Serialize};
use tap::prelude::*;
//use tokio::sync::Notify;
use tokio_rustls::rustls::pki_types;

const CERTIFICATE_LABELS: &[&str] = &["CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE"];
const PRIVATE_KEY_LABELS: &[&str] = &["PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY"];

#[derive(PartialEq, Eq, Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub enum CertSource {
    /// Provided by filesystem
    #[default]
    External,
    // Need to implement SystemStore for windows
}

#[derive(Clone)]
pub struct ConfigHandleManager {
    pub conf: Arc<Conf>,
}

fn get_default_path() -> Utf8PathBuf {
    Utf8PathBuf::from("/Users/chico/.hoop/gateway.json")
}

fn get_path() -> Utf8PathBuf {
    std::env::var("HOOP_PATH")
        .map(Utf8PathBuf::from)
        .unwrap_or_else(|_| get_default_path())
}

impl ConfigHandleManager {
    pub fn auto_gen_conf() -> anyhow::Result<Self> {
        println!("Auto-generating TLS certificate for gateway...");
        let hoop_key = get_token();
        if hoop_key.is_none() {
            Err(anyhow::anyhow!(
                "HOOP_KEY environment variable is not set. This may lead to insecure configurations."
            ))?;
        }
        let ip_addresses = vec!["127.0.0.1".parse::<IpAddr>()?, "::1".parse::<IpAddr>()?];
        let cert_key_pair = crate::certs::x509::generate_gateway_cert(ip_addresses);
        let tls = match cert_key_pair {
            Ok(cert_key_pair) => {
                let (certificates, private_key) = cert_key_pair.to_rustls();
                let certificates = vec![certificates];
                let cert_source = crate::tls::CertificateSource::External {
                    certificates,
                    private_key,
                };
                Some(Tls::init(cert_source, true)?)
            }
            Err(e) => {
                println!("Failed to generate certificate: {e}");
                None
            }
        };
        let conf = Conf {
            hostname: "gateway.hoop".to_string(),
            tls,
            token: hoop_key,
        };

        let config_handler = Self {
            conf: Arc::new(conf),
        };
        Ok(config_handler)
    }

    pub fn init() -> anyhow::Result<Self> {
        let hoop_auto_gencert = std::env::var("HOOP_CERT")
            .unwrap_or_else(|_| "true".to_string())
            .parse::<bool>()
            .unwrap_or(true);
        let conf_manager = match hoop_auto_gencert {
            true => Self::auto_gen_conf(),
            false => Self::init_from_files(),
        }?;
        return Ok(conf_manager);
    }

    pub fn init_from_files() -> anyhow::Result<Self> {
        let hoop_key = get_token();
        if hoop_key.is_none() {
            Err(anyhow::anyhow!(
                "HOOP_KEY environment variable is not set. This may lead to insecure configurations."
            ))?;
        }
        let path = get_path();
        let conf = load_conf_file(&path)
            .unwrap_or_else(|e| panic!("failed to load config file at {path}: {e}"));
        let conf_file = match conf {
            Some(c) => ConfFile {
                token: hoop_key,
                ..c
            },
            None => {
                println!("No config file found at {path}, using defaults");
                // Default configuration implment this for development only
                // do not use defaults in production
                ConfFile {
                    hostname: None,
                    token: hoop_key,
                    tls_certificate_source: Some(CertSource::External),
                    tls_certificate_file: None,
                    tls_private_key_file: None,
                    tls_verify_strict: Some(true),
                }
            }
        };

        let conf = Conf::from_conf_file(&conf_file).context("invalid configuration file")?;
        let config_handler = Self {
            conf: Arc::new(conf),
        };

        Ok(config_handler)
    }
}

fn get_token() -> Option<String> {
    let hoop_key = std::env::var("HOOP_KEY").ok();
    if hoop_key.is_none() {
        // format: <scheme>://<agent-name>:<secret-key>@<host>:<port>?mode=<agent-mode>
    }

    return hoop_key;
}

#[derive(PartialEq, Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct ConfFile {
    /// This Gateway hostname (e.g.: hoop.example.com)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hostname: Option<String>,

    pub token: Option<String>,
    pub tls_certificate_source: Option<CertSource>,
    pub tls_certificate_file: Option<Utf8PathBuf>,
    pub tls_private_key_file: Option<Utf8PathBuf>,
    pub tls_verify_strict: Option<bool>,
}

fn load_conf_file(conf_path: &Utf8Path) -> anyhow::Result<Option<ConfFile>> {
    match File::open(conf_path) {
        Ok(file) => BufReader::new(file)
            .pipe(serde_json::from_reader)
            .map(Some)
            .with_context(|| format!("invalid config file at {conf_path}")),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => {
            Err(anyhow::anyhow!(e).context(format!("couldn't open config file at {conf_path}")))
        }
    }
}

#[derive(Clone, Debug)]
pub struct Conf {
    pub hostname: String,
    pub token: Option<String>,
    pub tls: Option<Tls>,
}

impl Conf {
    pub fn from_conf_file(conf_file: &ConfFile) -> anyhow::Result<Self> {
        let hostname = conf_file
            .hostname
            .clone()
            .unwrap_or_else(|| "localhost".to_owned());

        let strict_checks = conf_file.tls_verify_strict.unwrap_or(false);

        let tls = match conf_file.tls_certificate_source.unwrap_or_default() {
            CertSource::External => match conf_file.tls_certificate_file.as_ref() {
                None => None,
                Some(certificate_path) => {
                    println!("Using TLS certificate from file: {}", certificate_path);
                    let (certificates, private_key) = match certificate_path.extension() {
                        None | Some(_) => {
                            let certificates = read_rustls_certificate_file(certificate_path)
                                .context("read TLS certificate")?;
                            let private_key = conf_file
                                .tls_private_key_file
                                .as_ref()
                                .context("TLS private key file is missing")?
                                .pipe_deref(read_rustls_priv_key_file)
                                .context("read TLS private key")?;

                            (certificates, private_key)
                        }
                    };

                    let cert_source = crate::tls::CertificateSource::External {
                        certificates,
                        private_key,
                    };

                    Tls::init(cert_source, strict_checks)
                        .context("failed to initialize TLS configuration")?
                        .pipe(Some)
                }
            },
        };

        Ok(Conf {
            hostname,
            tls,
            token: conf_file.token.clone(),
        })
    }
}
#[derive(Clone)]
pub struct Tls {
    pub acceptor: tokio_rustls::TlsAcceptor,
}

impl fmt::Debug for Tls {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("TlsConfig").finish_non_exhaustive()
    }
}

impl Tls {
    fn init(
        cert_source: crate::tls::CertificateSource,
        strict_checks: bool,
    ) -> anyhow::Result<Self> {
        let tls_server_config = crate::tls::build_server_config(cert_source, strict_checks)?;

        let acceptor = tokio_rustls::TlsAcceptor::from(Arc::new(tls_server_config));

        Ok(Self { acceptor })
    }
}

fn read_rustls_certificate_file(
    path: &Utf8Path,
) -> anyhow::Result<Vec<pki_types::CertificateDer<'static>>> {
    read_rustls_certificate(Some(path))
        .transpose()
        .expect("a path is provided, so it’s never None")
}

fn normalize_data_path(path: &Utf8Path, data_dir: &Utf8Path) -> Utf8PathBuf {
    if path.is_absolute() {
        path.to_owned()
    } else {
        data_dir.join(path)
    }
}

fn default_data_dir() -> Utf8PathBuf {
    Utf8PathBuf::from("/Users/chico/.hoop")
}

fn get_data_dir() -> Utf8PathBuf {
    //#TODO change this
    std::env::var("HOOP_DATA_DIR")
        .map(Utf8PathBuf::from)
        .unwrap_or_else(|_| default_data_dir())
}

#[derive(PartialEq, Eq, Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub enum DataEncoding {
    #[default]
    Multibase,
    Base64,
    Base64Pad,
    Base64Url,
    Base64UrlPad,
}

#[derive(PartialEq, Eq, Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct ConfData<Format> {
    pub value: String,
    #[serde(default)]
    pub format: Format,
    #[serde(default)]
    pub encoding: DataEncoding,
}

impl<Format> ConfData<Format> {
    pub fn decode_value(&self) -> anyhow::Result<Vec<u8>> {
        match self.encoding {
            DataEncoding::Multibase => multibase::decode(&self.value).map(|o| o.1),
            DataEncoding::Base64 => multibase::Base::Base64.decode(&self.value),
            DataEncoding::Base64Pad => multibase::Base::Base64Pad.decode(&self.value),
            DataEncoding::Base64Url => multibase::Base::Base64Url.decode(&self.value),
            DataEncoding::Base64UrlPad => multibase::Base::Base64UrlPad.decode(&self.value),
        }
        .context("invalid encoding for value")
    }
}

#[derive(PartialEq, Eq, Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub enum CertFormat {
    #[default]
    X509,
}

fn read_rustls_certificate(
    path: Option<&Utf8Path>,
) -> anyhow::Result<Option<Vec<pki_types::CertificateDer<'static>>>> {
    use picky::pem::{PemError, read_pem};

    match path {
        Some(path) => {
            let mut x509_chain_file = normalize_data_path(path, &get_data_dir())
                .pipe_ref(File::open)
                .with_context(|| format!("couldn't open file at {path}"))?
                .pipe(BufReader::new);

            let mut x509_chain = Vec::new();

            loop {
                match read_pem(&mut x509_chain_file) {
                    Ok(pem) => {
                        if CERTIFICATE_LABELS.iter().all(|&label| pem.label() != label) {
                            anyhow::bail!(
                                "bad pem label (got {}, expected one of {CERTIFICATE_LABELS:?}) at position {}",
                                pem.label(),
                                x509_chain.len(),
                            );
                        }

                        x509_chain.push(pki_types::CertificateDer::from(
                            pem.into_data().into_owned(),
                        ));
                    }
                    Err(e @ PemError::HeaderNotFound) => {
                        if x509_chain.is_empty() {
                            return anyhow::Error::new(e)
                                .context("couldn't parse first pem document")
                                .pipe(Err);
                        }

                        break;
                    }
                    Err(e) => {
                        return anyhow::Error::new(e)
                            .context(format!(
                                "couldn't parse pem document at position {}",
                                x509_chain.len()
                            ))
                            .pipe(Err);
                    }
                }
            }

            Ok(Some(x509_chain))
        }
        None => Ok(None),
    }
}

fn read_rustls_priv_key_file(path: &Utf8Path) -> anyhow::Result<pki_types::PrivateKeyDer<'static>> {
    read_rustls_priv_key(Some(path), None)
        .transpose()
        .expect("path is provided, so it’s never None")
}

#[derive(PartialEq, Eq, Debug, Clone, Copy, Default, Serialize, Deserialize)]
pub enum PrivKeyFormat {
    #[default]
    Pkcs8,
    #[serde(alias = "Rsa")]
    Pkcs1,
    Ec,
}

fn read_rustls_priv_key(
    path: Option<&Utf8Path>,
    data: Option<&ConfData<PrivKeyFormat>>,
) -> anyhow::Result<Option<pki_types::PrivateKeyDer<'static>>> {
    let private_key = match (path, data) {
        (Some(path), _) => {
            let pem: Pem<'_> = normalize_data_path(path, &get_data_dir())
                .pipe_ref(std::fs::read_to_string)
                .with_context(|| format!("couldn't read file at {path}"))?
                .pipe_deref(str::parse)
                .context("couldn't parse pem document")?;

            match pem.label() {
                "PRIVATE KEY" => {
                    pki_types::PrivateKeyDer::Pkcs8(pem.into_data().into_owned().into())
                }
                "RSA PRIVATE KEY" => {
                    pki_types::PrivateKeyDer::Pkcs1(pem.into_data().into_owned().into())
                }
                "EC PRIVATE KEY" => {
                    pki_types::PrivateKeyDer::Sec1(pem.into_data().into_owned().into())
                }
                _ => {
                    anyhow::bail!(
                        "bad pem label (got {}, expected one of {PRIVATE_KEY_LABELS:?})",
                        pem.label(),
                    );
                }
            }
        }
        (None, Some(data)) => {
            let value = data.decode_value()?;

            match data.format {
                PrivKeyFormat::Pkcs8 => pki_types::PrivateKeyDer::Pkcs8(value.into()),
                PrivKeyFormat::Pkcs1 => pki_types::PrivateKeyDer::Pkcs1(value.into()),
                PrivKeyFormat::Ec => pki_types::PrivateKeyDer::Sec1(value.into()),
            }
        }
        (None, None) => return Ok(None),
    };

    Ok(Some(private_key))
}
