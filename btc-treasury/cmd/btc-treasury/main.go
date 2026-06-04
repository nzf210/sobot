package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"btc-treasury/internal/config"
	"btc-treasury/internal/engine"
	"btc-treasury/internal/exchange"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/reporter"
	"btc-treasury/internal/runtime"
	"btc-treasury/internal/scanner"
	"btc-treasury/internal/telegram"
)

func supervise(name string, fn func()) {
	var backoff time.Duration = 5 * time.Second
	for {
		err := func() (res error) {
			defer func() {
				if r := recover(); r != nil {
					res = fmt.Errorf("panic: %v", r)
				}
			}()
			fn()
			return nil
		}()

		if err != nil {
			log.Printf("Supervisor: %s crashed: %v. Restarting in %v...", name, err, backoff)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
		} else {
			log.Printf("Supervisor: %s exited cleanly.", name)
			break
		}
	}
}

func main() {
	log.Printf("BTC Treasury Refactored Go Service starting...")

	cfg := config.Load()

	// Check for migration flag
	for _, arg := range os.Args {
		if arg == "--migrate-to-db" {
			runDbMigration(cfg)
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load account specifications
	accountSpecs, err := config.LoadAccountSpecs(cfg.ExchangeName, cfg.DataDir, cfg.ScannerPairs, cfg.DBDriver, cfg.DBDsn)
	if err != nil {
		log.Fatalf("Failed to load account specs: %v", err)
	}

	if err := config.ValidateSpecs(accountSpecs); err != nil {
		log.Fatalf("Invalid account specifications: %v", err)
	}

	if len(accountSpecs) > 1 {
		for _, s := range accountSpecs {
			if s.ID == "default" {
				log.Printf("WARNING: Multiple account specs share id='default' — they will share the legacy layout. Create named IDs.")
			}
		}
	}

	// Initialize exchange client dispatcher
	dispatcher := exchange.FromSpecs(accountSpecs)
	if dispatcher.IsEmpty() {
		log.Printf("Scanner disabled — no exchange API credentials configured")
	}

	// Build runtimes
	runtimes := make(map[exchange.AccountKey]*runtime.AccountRuntime)
	var runtimeList []*runtime.AccountRuntime

	for _, spec := range accountSpecs {
		specCopy := spec
		key := exchange.AccountKeyFromSpec(&specCopy)
		exClient := dispatcher.ForAccount(key)
		if exClient == nil {
			log.Printf("Skipping spec %s/%s — dispatcher could not build a client (unresolved credentials?)", spec.ID, spec.Exchange)
			continue
		}

		var mem memory.Store
		if cfg.DBDsn != "" {
			mem, err = memory.NewGormDBStore(cfg.DBDriver, cfg.DBDsn, spec.ID, spec.Exchange)
			if err != nil {
				log.Fatalf("Failed to initialize database store for %s/%s: %v", spec.ID, spec.Exchange, err)
			}
		} else {
			mem = memory.NewMemoryStoreWithAccount(cfg.DataDir, spec.ID, spec.Exchange)
		}

		rt := runtime.Build(&specCopy, exClient, mem, cfg.LlmURL, cfg.LlmModel, cfg.LlmAPIKey)
		rt.InitializePairs(ctx)

		// Sync initial balances for this runtime
		balances, err := rt.Exchange.GetBalances(ctx)
		if err == nil {
			var liveBtc, liveUsdt float64
			for _, b := range balances {
				if b.Asset == "BTC" {
					liveBtc = b.Free + b.Locked
				}
				if b.Asset == "USDT" || b.Asset == "USDC" {
					liveUsdt = b.Free + b.Locked
				}
			}
			rt.Mem.SyncInitialBalances(liveBtc, liveUsdt)
			rt.Mem.UpdateGrowthRatios()
		} else {
			log.Printf("Failed to fetch live balances for treasury sync (%s/%s): %v", spec.ID, spec.Exchange, err)
		}

		runtimes[key] = rt
		runtimeList = append(runtimeList, rt)
	}

	// Start scanners and monitors for each active runtime
	for _, rt := range runtimeList {
		spec := rt.Spec
		rtCopy := rt

		// Scanner worker loop
		go supervise(fmt.Sprintf("Scanner-%s/%s", spec.ID, spec.Exchange), func() {
			scanner.Run(
				ctx,
				rtCopy.ScannerState,
				rtCopy.Exchange,
				rtCopy.Engine,
				rtCopy.Executor,
				rtCopy.Mem,
				cfg.ScannerIntervalSecs,
				rtCopy.Status,
			)
		})

		// Position monitor worker loop
		go supervise(fmt.Sprintf("Monitor-%s/%s", spec.ID, spec.Exchange), func() {
			mon := rtCopy.BuildMonitor()
			mon.Start(ctx)
		})

		log.Printf("BTC Scanner + Monitor started for %s/%s", spec.ID, spec.Exchange)
	}

	// Start Aggregate Reporter
	if len(cfg.TelegramReportChatIDs) > 0 {
		var reports []reporter.PerAccountReport
		for _, rt := range runtimeList {
			chats := rt.Spec.TelegramChatIDs
			if len(chats) == 0 {
				chats = cfg.TelegramReportChatIDs
			}
			reports = append(reports, reporter.PerAccountReport{
				AccountID: rt.Spec.ID,
				Exchange:  rt.Spec.Exchange,
				State:     rt.ScannerState,
				Mem:       rt.Mem,
				ChatIDs:   chats,
			})
		}
		if len(reports) > 0 {
			go reporter.Run(ctx, reports, cfg.TelegramBotToken, cfg.TelegramReportChatIDs, cfg.ReportIntervalMins)
		}
	} else {
		log.Printf("WARNING: TELEGRAM_REPORT_CHAT_IDS not set — reporter disabled")
	}

	// Start Telegram Bot
	if cfg.TelegramBotToken != "" {
		var defaultScanner *scanner.ScannerState
		var defaultEngine *engine.AdvisoryEngine
		var defaultMem memory.Store
		if len(runtimeList) > 0 {
			defaultScanner = runtimeList[0].ScannerState
			defaultEngine = runtimeList[0].Engine
			defaultMem = runtimeList[0].Mem
		}

		bot := telegram.NewBtcBot(
			cfg.TelegramBotToken,
			cfg.TelegramWhitelist,
			defaultEngine,
			defaultMem,
			defaultScanner,
			runtimes,
		)
		log.Printf("BTC Telegram bot starting...")
		go bot.Start(ctx)
	} else {
		log.Printf("WARNING: TELEGRAM_BOT_BTC_TOKEN not set — Telegram bot disabled")
	}

	// Graceful shutdown wait
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	sig := <-stop
	log.Printf("Received signal %v — initiating graceful shutdown", sig)
	cancel()

	// Give other background tasks 500ms to settle
	time.Sleep(500 * time.Millisecond)
	log.Printf("BTC Treasury shut down cleanly")
}
