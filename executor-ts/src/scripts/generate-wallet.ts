import { Keypair } from '@solana/web3.js';
import bs58 from 'bs58';
import fs from 'fs';
import path from 'path';
import readline from 'readline';
import dotenv from 'dotenv';
import { encrypt } from '../wallets/crypto';

// Load environment variables from the root .env
const projectRoot = path.resolve(__dirname, '../../../');
dotenv.config({ path: path.resolve(projectRoot, '.env') });

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
});

function question(query: string): Promise<string> {
  return new Promise((resolve) => rl.question(query, resolve));
}

async function main() {
  console.log('--- Secure Wallet Generation ---');
  let privateKeyInput = await question('Enter an existing base58 private key (leave empty to generate a new one): ');
  
  let keypair: Keypair;
  if (privateKeyInput.trim() === '') {
    keypair = Keypair.generate();
    console.log('Generated a new Solana wallet.');
  } else {
    try {
      keypair = Keypair.fromSecretKey(bs58.decode(privateKeyInput.trim()));
      console.log('Loaded existing wallet successfully.');
    } catch (error) {
      console.error('Invalid private key provided.');
      process.exit(1);
    }
  }

  console.log(`Public Key: ${keypair.publicKey.toBase58()}`);

  let password = await question('Enter a password to encrypt the wallet: ');
  password = password.trim();
  if (!password) {
    console.error('Password cannot be empty.');
    process.exit(1);
  }

  const secretKeyArray = Array.from(keypair.secretKey);
  const encryptedData = encrypt(JSON.stringify(secretKeyArray), password);

  const envPath = process.env.WALLET_PATH || 'executor-ts/wallet.enc';
  const outPath = path.resolve(projectRoot, envPath);
  
  fs.writeFileSync(outPath, encryptedData);
  console.log(`Wallet encrypted and saved to ${outPath}`);

  rl.close();
}

main().catch(console.error);
