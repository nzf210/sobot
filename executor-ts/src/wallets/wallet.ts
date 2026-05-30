import { Keypair } from "@solana/web3.js";
import fs from "fs";
import path from "path";
import dotenv from "dotenv";
import { decrypt } from "./crypto";

// In Docker: process.cwd() = /app (WORKDIR). __dirname-based resolution breaks.
const appRoot = process.cwd();
dotenv.config({ path: path.join(appRoot, '.env') });

export function loadWallet(): Keypair {
  const walletPath = process.env.WALLET_PATH || 'wallet.enc';
  const password = process.env.WALLET_PASSWORD;

  if (!password) {
    throw new Error("WALLET_PASSWORD is not set in environment variables.");
  }

  // If absolute path provided, use as-is. Otherwise resolve from app root.
  const fullPath = path.isAbsolute(walletPath)
    ? walletPath
    : path.join(appRoot, walletPath);

  console.log(`[wallet] Loading from: ${fullPath}`);

  if (!fs.existsSync(fullPath)) {
    throw new Error(`Encrypted wallet file not found at ${fullPath}. Run generate-wallet script first.`);
  }

  const encryptedData = fs.readFileSync(fullPath);
  const decryptedStr = decrypt(encryptedData, password);

  const secretKeyArray = JSON.parse(decryptedStr);
  return Keypair.fromSecretKey(new Uint8Array(secretKeyArray));
}