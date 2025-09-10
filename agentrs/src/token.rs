use jsonwebtoken::{DecodingKey, EncodingKey};
use once_cell::sync::Lazy;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub struct Keys<'a> {
    encoding: EncodingKey,
    decoding: DecodingKey<'a>,
}

static KEYS: Lazy<Keys> = Lazy::new(|| {
    let secret = "your-256-bit-secret";
    Keys::new(secret.as_bytes())
});

impl<'a> Keys<'a> {
    fn new(secret: &'a [u8]) -> Self {
        Self {
            encoding: EncodingKey::from_secret(secret),
            decoding: DecodingKey::from_secret(secret),
        }
    }
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Claims {
    pub server_addrs: String,
    pub username: String,
    pub password: String,
    exp: usize,
}

impl Claims {
    pub async fn from_token(token: &str) -> Option<Self> {
        let token_data = jsonwebtoken::decode::<Claims>(
            token,
            &KEYS.decoding,
            &jsonwebtoken::Validation::default(),
        )
        .ok()?;
        Some(token_data.claims)
    }
    async fn new_cliams_token(server_addrs: String) -> Option<String> {
        let expiration = chrono::Utc::now()
            .checked_add_signed(chrono::Duration::hours(24))?
            .timestamp() as usize;

        let claims = Claims {
            server_addrs,
            username: "chico".to_string(),
            password: "xxx".to_string(),
            exp: expiration,
        };
        let token =
            jsonwebtoken::encode(&jsonwebtoken::Header::default(), &claims, &KEYS.encoding).ok()?;
        println!("{}", token);
        Some(token)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn test_token_generation_and_validation() {
        let token = Claims::new_cliams_token("10.211.55.6".to_string())
            .await
            .expect("Token generation failed");
        let claims = Claims::from_token(&token)
            .await
            .expect("Token validation failed");
        dbg!("Generated Token: {}", token);
        assert_eq!(claims.password, "xxxxx");
        dbg!("Generated Token: {}", claims);
    }
}
