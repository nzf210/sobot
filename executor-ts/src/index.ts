import express, { Request, Response, NextFunction } from "express"
import { executeSwap } from "./executors/jupiter"
import { loadWallet } from "./wallets/wallet"
import { Connection } from '@solana/web3.js'

const API_KEY = process.env.EXECUTOR_API_KEY || ""

const app = express()

try {
  const wallet = loadWallet()
  console.log("Loaded wallet:", wallet.publicKey.toBase58())
} catch (e) {
  console.error("Failed to load wallet on boot:", e)
}

app.use(express.json())

function authMiddleware(req: Request, res: Response, next: NextFunction): void {
  if (!API_KEY) {
    console.warn("EXECUTOR_API_KEY not set — auth disabled (insecure)")
    next()
    return
  }
  const provided = req.headers["x-api-key"] as string | undefined
  if (provided !== API_KEY) {
    res.status(401).json({ success: false, error: "Unauthorized" })
    return
  }
  next()
}

app.get("/health", (_: Request, res: Response) => {
  res.json({ status: "ok" })
})

app.use(authMiddleware)

app.get("/wallet", async (_: Request, res: Response) => {
  try {
    const wallet = loadWallet();
    const connection = new Connection(process.env.RPC_URL || 'https://api.mainnet-beta.solana.com');
    const balance = await connection.getBalance(wallet.publicKey);
    res.json({
      success: true,
      address: wallet.publicKey.toBase58(),
      balanceSol: balance / 1e9
    });
  } catch (e: any) {
    res.status(500).json({ success: false, error: e.message });
  }
})

app.post("/execute", async (req: Request, res: Response): Promise<any> => {
  const { inputMint, outputMint, amount } = req.body;

  if (!inputMint || !outputMint || !amount) {
    return res.status(400).json({ success: false, error: "Missing required fields: inputMint, outputMint, amount" });
  }

  console.log("received swap action", req.body)

  const result = await executeSwap(inputMint, outputMint, amount)

  res.json({
    success: result.status === "success",
    result
  })
})

app.post("/deploy-dlmm", async (req: Request, res: Response): Promise<any> => {
  const { tokenAddress, liquiditySOL, confidenceScore, dlmmSuitability, recommendedSize } = req.body;

  console.log("DLMM deployment requested", {
    tokenAddress,
    liquiditySOL,
    confidenceScore,
    dlmmSuitability,
    recommendedSize
  });

  try {
    const { deployDLMM } = await import("./meteora/dlmm");
    const result = await deployDLMM();

    res.json({
      success: true,
      position: result.position,
      deployed: result.deployed
    });
  } catch (e: any) {
    console.error("DLMM deployment failed", e);
    res.status(500).json({ success: false, error: e.message });
  }
})

const PORT = process.env.EXECUTOR_PORT || 3000;
app.listen(PORT, () => {
  console.log(`executor running on :${PORT}`)
})