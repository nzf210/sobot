package reporter

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"btc-treasury/internal/config"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/scanner"
	"btc-treasury/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	pairDetailThreshold = 20
	maxReasonChars      = 120
)

type PerAccountReport struct {
	AccountID string
	Exchange  config.ExchangeKind
	State     *scanner.ScannerState
	Mem       memory.Store
	ChatIDs   []int64
}

type reportTitle struct {
	Legacy        bool
	MultiAccount  bool
	MultiExchange bool
	AccountID     string
	Exchange      string
}

func (t reportTitle) Format() string {
	if t.Legacy {
		return fmt.Sprintf("*BTC Scan Report — %s Spot*\n", t.Exchange)
	}
	if t.MultiExchange {
		return fmt.Sprintf("*BTC Scan Report — [%s/%s]*\n", t.AccountID, t.Exchange)
	}
	return fmt.Sprintf("*BTC Scan Report — [%s]*\n", t.AccountID)
}

func Run(
	ctx context.Context,
	accounts []PerAccountReport,
	botToken string,
	fallbackChatIDs []int64,
	intervalMins uint64,
) {
	var effective []struct {
		Report  PerAccountReport
		ChatIDs []int64
	}

	for _, a := range accounts {
		chats := a.ChatIDs
		if len(chats) == 0 {
			chats = fallbackChatIDs
		}
		if len(chats) > 0 {
			effective = append(effective, struct {
				Report  PerAccountReport
				ChatIDs []int64
			}{
				Report:  a,
				ChatIDs: chats,
			})
		}
	}

	if len(effective) == 0 {
		log.Printf("Reporter: No report chat IDs configured — reporter disabled")
		return
	}

	total := len(effective)
	distinctIDs := make(map[string]bool)
	for _, entry := range effective {
		distinctIDs[entry.Report.AccountID] = true
	}

	bindingsPerID := countBindingsPerID(effective)
	multi := total > 1

	log.Printf("BTC reporter started (every %d min) for %d binding(s) across %d account(s)",
		intervalMins, total, len(distinctIDs))

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Printf("Reporter: Failed to initialize Telegram bot: %v", err)
		return
	}

	lastLessonCount := make(map[string]int)
	for _, entry := range effective {
		key := fmt.Sprintf("%s|%s", entry.Report.AccountID, entry.Report.Exchange)
		lastLessonCount[key] = len(entry.Report.Mem.GetLessons())
	}

	ticker := time.NewTicker(time.Duration(intervalMins) * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Reporter: Stopping loop")
			return
		case <-ticker.C:
			for _, entry := range effective {
				acct := entry.Report
				key := fmt.Sprintf("%s|%s", acct.AccountID, acct.Exchange)
				prev := lastLessonCount[key]

				snapshots := acct.State.AllSnapshots()
				
				recentDecisions := acct.State.GetRecentDecisions()
				var recent []scanner.RecentDecision
				for i := len(recentDecisions) - 1; i >= 0 && len(recent) < 5; i-- {
					recent = append(recent, recentDecisions[i])
				}

				allLessons := acct.Mem.GetLessons()
				var newLessons []string
				if len(allLessons) > prev {
					newLessons = allLessons[prev:]
				}

				if len(snapshots) == 0 && len(recent) == 0 && len(newLessons) == 0 {
					continue
				}

				var title reportTitle
				if total == 1 {
					title = reportTitle{Legacy: true, Exchange: strings.Title(string(acct.Exchange))}
				} else if bindingsPerID[acct.AccountID] > 1 {
					title = reportTitle{MultiExchange: true, AccountID: acct.AccountID, Exchange: string(acct.Exchange)}
				} else {
					title = reportTitle{MultiAccount: true, AccountID: acct.AccountID}
				}

				msg := formatReport(title, snapshots, recent, newLessons)

				for _, chatID := range entry.ChatIDs {
					_, err := utils.SendMdv2Safe(bot, chatID, msg)
					if err != nil {
						log.Printf("Reporter [%s/%s]: failed to send to %d: %v",
							acct.AccountID, acct.Exchange, chatID, err)
					}
				}
			}

			for _, entry := range effective {
				acct := entry.Report
				key := fmt.Sprintf("%s|%s", acct.AccountID, acct.Exchange)
				lastLessonCount[key] = len(acct.Mem.GetLessons())
			}

			if multi {
				if len(effective) > 0 {
					firstEntry := effective[0]
					var totalBTC float64
					var totalVault float64
					var totalTrades int

					for _, entry := range effective {
						state := entry.Report.Mem.GetTreasuryState()
						totalBTC += state.CurrentBtc
						totalVault += state.BtcTreasuryVault
						totalTrades += state.TotalTrades
					}

					aggregateMsg := fmt.Sprintf(
						"\n──────────\n*Aggregate — All Bindings*\nBTC: %.8f \\| Vault: %.8f \\| Trades: %d",
						totalBTC, totalVault, totalTrades,
					)

					for _, chatID := range firstEntry.ChatIDs {
						_, err := utils.SendMdv2Safe(bot, chatID, aggregateMsg)
						if err != nil {
							log.Printf("Reporter [aggregate]: failed to send to %d: %v", chatID, err)
						}
					}
				}
			}
		}
	}
}

func countBindingsPerID(effective []struct {
	Report  PerAccountReport
	ChatIDs []int64
}) map[string]int {
	m := make(map[string]int)
	for _, entry := range effective {
		m[entry.Report.AccountID]++
	}
	return m
}

func formatReport(
	title reportTitle,
	snapshots []scanner.PairSnapshot,
	recent []scanner.RecentDecision,
	newLessons []string,
) string {
	var lines []string

	lines = append(lines, title.Format())

	var totalScanned uint64
	var totalErrors uint64
	for _, s := range snapshots {
		totalScanned += s.Stats.Scanned
		totalErrors += s.Stats.Errors
	}

	if totalScanned == 0 && len(recent) == 0 && len(newLessons) == 0 {
		lines = append(lines, "No scans in this period\\.")
		return strings.Join(lines, "\n")
	}

	if len(snapshots) > 0 {
		var approveCnt, monitorCnt, protectCnt, rejectCnt int
		for _, s := range snapshots {
			switch s.LastRecommendation {
			case "APPROVE":
				approveCnt++
			case "MONITOR":
				monitorCnt++
			case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
				protectCnt++
			default:
				if s.LastRecommendation != "" {
					rejectCnt++
				}
			}
		}

		lines = append(lines, fmt.Sprintf(
			"Pairs: %d \\| Scans: %d \\| Errors: %d\n✅ %d \\| 👁 %d \\| 🛡 %d \\| ❌ %d\n",
			len(snapshots), totalScanned, totalErrors,
			approveCnt, monitorCnt, protectCnt, rejectCnt,
		))

		if len(snapshots) <= pairDetailThreshold {
			for _, s := range snapshots {
				var icon string
				switch s.LastRecommendation {
				case "APPROVE":
					icon = "✅"
				case "MONITOR":
					icon = "👁"
				case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
					icon = "🛡"
				default:
					if s.LastRecommendation == "" {
						icon = "⏳"
					} else {
						icon = "❌"
					}
				}

				lines = append(lines, fmt.Sprintf(
					"%s %s %d scans \\| %s \\(conf: %.2f\\)",
					icon,
					utils.EscapeMdv2(s.Pair),
					s.Stats.Scanned,
					utils.EscapeMdv2(s.LastRecommendation),
					s.LastConfidence,
				))
			}
		} else {
			var approvePairs []scanner.PairSnapshot
			for _, s := range snapshots {
				if s.LastRecommendation == "APPROVE" {
					approvePairs = append(approvePairs, s)
				}
			}

			sort.Slice(approvePairs, func(i, j int) bool {
				return approvePairs[i].LastConfidence > approvePairs[j].LastConfidence
			})

			if len(approvePairs) > 0 {
				lines = append(lines, "*Top APPROVE pairs:*")
				limit := 5
				if len(approvePairs) < 5 {
					limit = len(approvePairs)
				}
				for i := 0; i < limit; i++ {
					s := approvePairs[i]
					lines = append(lines, fmt.Sprintf(
						"✅ %s conf:%.2f",
						utils.EscapeMdv2(s.Pair),
						s.LastConfidence,
					))
				}
			}
		}
	}

	if len(recent) > 0 {
		lines = append(lines, "\n*Recent Decisions:*")
		for i, d := range recent {
			var statusIcon string
			switch d.Recommendation {
			case "APPROVE":
				statusIcon = "✅"
			case "MONITOR":
				statusIcon = "👁"
			case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
				statusIcon = "🛡"
			default:
				statusIcon = "❌"
			}

			timeShort := d.Timestamp
			if len(d.Timestamp) > 16 {
				timeShort = d.Timestamp[11:19]
			}

			reasonShort := d.Reason
			if len(d.Reason) > maxReasonChars {
				reasonShort = d.Reason[:maxReasonChars] + "…"
			}

			lines = append(lines, fmt.Sprintf(
				"%d\\. %s %s %s \\- %s \\(conf: %.2f, risk: %s\\)\n  \\_`%s`",
				i+1,
				utils.EscapeMdv2(timeShort),
				utils.EscapeMdv2(d.Pair),
				statusIcon,
				utils.EscapeMdv2(d.Recommendation),
				d.Confidence,
				utils.EscapeMdv2(d.RiskLevel),
				utils.EscapeMdv2(reasonShort),
			))
		}
	}

	if len(newLessons) > 0 {
		lines = append(lines, "\n*New Lessons:*")
		limit := 3
		if len(newLessons) < 3 {
			limit = len(newLessons)
		}
		for i := 0; i < limit; i++ {
			lesson := newLessons[i]
			short := lesson
			if len(lesson) > 150 {
				short = lesson[:147] + "..."
			}
			lines = append(lines, fmt.Sprintf(
				"%d\\. %s",
				i+1,
				utils.EscapeMdv2(short),
			))
		}
	}

	return strings.Join(lines, "\n")
}
