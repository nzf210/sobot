use aes_gcm::{
    aead::{Aead, KeyInit},
    Aes256Gcm, Nonce,
};
use anyhow::{Context, Result};
use rand::Rng;

const SALT_LEN: usize = 16;
const IV_LEN: usize = 12;
const TAG_LEN: usize = 16;

/// Encrypts plaintext with password using AES-256-GCM.
/// Output format: salt(16) || iv(12) || tag(16) || ciphertext
/// Mirrors executor-ts/src/wallets/crypto.ts format.
#[allow(dead_code)]
pub fn encrypt(plaintext: &str, password: &str) -> Result<Vec<u8>> {
    let mut rng = rand::thread_rng();

    let salt: [u8; SALT_LEN] = rng.gen();
    let key = derive_key(password, &salt);

    let iv: [u8; IV_LEN] = rng.gen();
    let nonce = Nonce::from_slice(&iv);

    let cipher = Aes256Gcm::new(&key);
    let encrypted = cipher
        .encrypt(nonce, plaintext.as_bytes())
        .map_err(|e| anyhow::anyhow!("encryption failed: {}", e))?;

    let mut output = Vec::with_capacity(SALT_LEN + IV_LEN + TAG_LEN + encrypted.len());
    output.extend_from_slice(&salt);
    output.extend_from_slice(&iv);
    // GCM tag is appended to encrypted by the library, extract last 16 bytes
    let ciphertext_len = encrypted.len() - TAG_LEN;
    output.extend_from_slice(&encrypted[encrypted.len() - TAG_LEN..]); // tag first
    output.extend_from_slice(&encrypted[..ciphertext_len]); // then ciphertext

    Ok(output)
}

/// Decrypts data encrypted by `encrypt()` (or executor-ts crypto.ts).
/// Input format: salt(16) || iv(12) || tag(16) || ciphertext
pub fn decrypt(encrypted_data: &[u8], password: &str) -> Result<String> {
    if encrypted_data.len() < SALT_LEN + IV_LEN + TAG_LEN + 1 {
        anyhow::bail!("encrypted data too short");
    }

    let salt: [u8; SALT_LEN] = encrypted_data[..SALT_LEN].try_into().unwrap();
    let iv: [u8; IV_LEN] = encrypted_data[SALT_LEN..SALT_LEN + IV_LEN]
        .try_into()
        .unwrap();
    let tag: [u8; TAG_LEN] = encrypted_data[SALT_LEN + IV_LEN..SALT_LEN + IV_LEN + TAG_LEN]
        .try_into()
        .unwrap();
    let ciphertext = &encrypted_data[SALT_LEN + IV_LEN + TAG_LEN..];

    let key = derive_key(password, &salt);

    let mut ciphertext_with_tag = Vec::with_capacity(ciphertext.len() + TAG_LEN);
    ciphertext_with_tag.extend_from_slice(ciphertext);
    ciphertext_with_tag.extend_from_slice(&tag);

    let cipher = Aes256Gcm::new(&key);
    let nonce = Nonce::from_slice(&iv);

    let plaintext = cipher
        .decrypt(nonce, ciphertext_with_tag.as_ref())
        .map_err(|e| anyhow::anyhow!("decryption failed (wrong password?): {}", e))?;

    String::from_utf8(plaintext).context("decrypted data is not valid UTF-8")
}

fn derive_key(password: &str, salt: &[u8; SALT_LEN]) -> aes_gcm::Key<Aes256Gcm> {
    let params = scrypt::Params::new(14, 8, 1, 16).expect("valid scrypt params");
    let mut key_bytes = [0u8; 32];
    scrypt::scrypt(password.as_bytes(), salt, &params, &mut key_bytes)
        .expect("scrypt key derivation should not fail");
    *aes_gcm::Key::<Aes256Gcm>::from_slice(&key_bytes)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_encrypt_decrypt_roundtrip() {
        let plaintext = "0xabc123def456";
        let password = "test_password";
        let encrypted = encrypt(plaintext, password).unwrap();
        let decrypted = decrypt(&encrypted, password).unwrap();
        assert_eq!(decrypted, plaintext);
    }

    #[test]
    fn test_wrong_password_fails() {
        let plaintext = "secret";
        let encrypted = encrypt(plaintext, "correct").unwrap();
        assert!(decrypt(&encrypted, "wrong").is_err());
    }

    #[test]
    fn test_short_data_fails() {
        assert!(decrypt(b"short", "pw").is_err());
    }
}
