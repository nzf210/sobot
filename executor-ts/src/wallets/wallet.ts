import { Keypair } from "@solana/web3.js";
import fs from "fs";
import path from "path";
import dotenv from "dotenv";
import { decrypt } from "./crypto";

const projectRoot = path.resolve(__dirname, '../../../');
dotenv.config({ path: path.resolve(projectRoot, '.env') });

export function loadWallet(): Keypair {
  const walletPath = process.env.WALLET_PATH || 'executor-ts/wallet.enc';
  const password = process.env.WALLET_PASSWORD;

  if (!password) {
    throw new Error("WALLET_PASSWORD is not set in environment variables.");
  }

  const fullPath = path.resolve(projectRoot, walletPath);
  
  if (!fs.existsSync(fullPath)) {
    throw new Error(`Encrypted wallet file not found at ${fullPath}. Run generate-wallet script first.`);
  }

  const encryptedData = fs.readFileSync(fullPath);
  const decryptedStr = decrypt(encryptedData, password);
  
  const secretKeyArray = JSON.parse(decryptedStr);
  return Keypair.fromSecretKey(new Uint8Array(secretKeyArray));
}