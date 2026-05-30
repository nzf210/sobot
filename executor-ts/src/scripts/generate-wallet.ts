import { Keypair } from '@solana/web3.js';
import crypto from 'crypto';
import bs58 from 'bs58';
import fs from 'fs';
import path from 'path';
import readline from 'readline';
import dotenv from 'dotenv';
import { encrypt } from '../wallets/crypto';
import { sanitizePath } from '../utils/sanitize';

const projectRoot = path.resolve(__dirname, '../../../');
dotenv.config({ path: path.resolve(projectRoot, '.env') });

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
});

function question(query: string): Promise<string> {
  return new Promise((resolve) => rl.question(query, resolve));
}

function getPassword(): string {
  const envPass = process.env.WALLET_PASSWORD;
  if (envPass && envPass.trim()) {
    return envPass.trim();
  }
  return '';
}

function outputPath(chain: string): string {
  if (chain === 'solana') {
    return process.env.WALLET_PATH || 'executor-ts/wallet.enc';
  }
  return process.env.HYPERLIQUID_KEY_PATH || 'hyperliquid.enc';
}

async function main() {
  console.log('═══════════════════════════════════');
  console.log('  Secure Wallet Encryption Tool');
  console.log('═══════════════════════════════════\n');

  // Pick chain
  console.log('Select chain:');
  console.log('  1) Solana (Ed25519, base58 private key)');
  console.log('  2) Hyperliquid (secp256k1, hex private key)');
  const choice = await question('\nChoice [1/2]: ');

  const chain = choice.trim() === '2' ? 'hyperliquid' : 'solana';

  // Get private key
  let plaintext: string;
  let label: string;

  if (chain === 'solana') {
    console.log('\n--- Solana Wallet ---');
    const input = await question('Enter base58 private key (empty to generate new): ');

    if (input.trim() === '') {
      const kp = Keypair.generate();
      plaintext = JSON.stringify(Array.from(kp.secretKey));
      console.log(`Generated new wallet.`);
      console.log(`Public Key: ${kp.publicKey.toBase58()}`);
    } else {
      try {
        const kp = Keypair.fromSecretKey(bs58.decode(input.trim()));
        plaintext = JSON.stringify(Array.from(kp.secretKey));
        console.log(`Loaded existing wallet.`);
        console.log(`Public Key: ${kp.publicKey.toBase58()}`);
      } catch (e) {
        console.error('Invalid base58 private key.');
        process.exit(1);
      }
    }
    label = 'Solana';
  } else {
    console.log('\n--- Hyperliquid Wallet ---');
    const input = await question('Enter hex private key (empty to generate random): ');

    if (input.trim() === '') {
      const privKey = crypto.randomBytes(32).toString('hex');
      console.log(`Generated random secp256k1 private key.`);
      console.log(`Private Key: 0x${privKey}`);
      console.log('⚠️  Save this key! It will not be shown again.');
      plaintext = `0x${privKey}`;
    } else {
      const raw = input.trim().replace(/^0x/, '');
      if (!/^[0-9a-fA-F]{64}$/.test(raw)) {
        console.error('Invalid secp256k1 hex private key. Must be 64 hex chars.');
        process.exit(1);
      }
      console.log(`Loaded existing secp256k1 private key.`);
      plaintext = `0x${raw}`;
    }
    label = 'Hyperliquid';
  }

  // Get password
  const envPassword = getPassword();
  let password: string;
  if (envPassword) {
    console.log(`\nUsing WALLET_PASSWORD from .env`);
    password = envPassword;
  } else {
    password = await question('\nEnter password to encrypt wallet: ');
    password = password.trim();
    if (!password) {
      console.error('Password cannot be empty.');
      process.exit(1);
    }
  }

  // Encrypt and save
  const encryptedData = encrypt(plaintext, password);
  const outPath = path.resolve(projectRoot, outputPath(chain));

  fs.writeFileSync(outPath, encryptedData);
  console.log(`\n✅ ${label} wallet encrypted → ${outPath}`);

  rl.close();
}

main().catch(console.error);
