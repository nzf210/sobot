import { Connection, VersionedTransaction } from '@solana/web3.js';
import axios from 'axios';
import { loadWallet } from '../wallets/wallet';

export async function executeSwap(inputMint: string, outputMint: string, amount: number) {
  try {
    const wallet = loadWallet();
    const connection = new Connection(process.env.RPC_URL || 'https://api.mainnet-beta.solana.com');

    // 1. Get Quote
    const quoteResponse = await axios.get(`https://quote-api.jup.ag/v6/quote?inputMint=${inputMint}&outputMint=${outputMint}&amount=${amount}&slippageBps=50`);
    
    // 2. Get Swap Transaction
    const swapResponse = await axios.post('https://quote-api.jup.ag/v6/swap', {
      quoteResponse: quoteResponse.data,
      userPublicKey: wallet.publicKey.toString(),
      wrapAndUnwrapSol: true,
    });

    const swapTransactionBuf = Buffer.from(swapResponse.data.swapTransaction, 'base64');
    const transaction = VersionedTransaction.deserialize(swapTransactionBuf);

    // 3. Sign and Send
    transaction.sign([wallet]);
    const txid = await connection.sendTransaction(transaction, { skipPreflight: true });

    return {
      txHash: txid,
      status: "success"
    };
  } catch (error: any) {
    console.error("Swap execution failed:", error);
    return {
      txHash: null,
      status: "failed",
      error: error.message
    };
  }
}