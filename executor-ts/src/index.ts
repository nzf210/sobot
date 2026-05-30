import express from "express"
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

app.get("/health", (_, res) => {
  res.json({ status: "ok" })
})

app.post("/execute", async (req, res) => {

  const body = req.body

  console.log("received action", body)

  const result = await executeSwap()

  res.json({
    success: true,
    result
  })
})

app.listen(3000, () => {
  console.log("executor running on :3000")
})