import express, { Request, Response } from "express"
import { executeSwap } from "./executors/jupiter"
import { loadWallet } from "./wallets/wallet"

const app = express()

try {
  const wallet = loadWallet()
  console.log("Loaded wallet:", wallet.publicKey.toBase58())
} catch (e) {
  console.error("Failed to load wallet on boot:", e)
}

app.use(express.json())

app.get("/health", (_: Request, res: Response) => {
  res.json({ status: "ok" })
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

const PORT = process.env.EXECUTOR_PORT || 3000;
app.listen(PORT, () => {
  console.log(`executor running on :${PORT}`)
})