use std::sync::Arc;

use tokio::time::{interval, Duration};

use crate::account_spec::ExchangeKind;
use crate::format::{escape_mdv2, send_mdv2_safe};
use crate::scanner::{ScannerState, RecentDecision};
use crate::memory::MemoryStore;

/// When there are more pairs than this threshold, the report switches from
/// per-pair lines to a compact summary-only mode to stay within Telegram's
/// 4096-char message limit. `send_mdv2_safe` will chunk even if this limit
/// is reached, but keeping individual messages meaningful is better UX.
const PAIR_DETAIL_THRESHOLD: usize = 20;

/// Maximum chars for a `reason` field in Recent Decisions before we truncate.
const MAX_REASON_CHARS: usize = 120;

/// Per-account, per-exchange report target. One reporter loop iterates
/// `Vec<PerAccountReport>` and emits a per-binding block. With one `default`
/// account on a single exchange the output is byte-identical to the
/// pre-Fase-1.5 reporter. With N accounts × M exchanges each binding gets
/// its own block prefixed `[id/exchange]`.
#[derive(Clone)]
pub struct PerAccountReport {
    pub account_id: String,
    pub exchange: ExchangeKind,
    pub state: Arc<ScannerState>,
    pub mem: Arc<MemoryStore>,
    /// Per-account chat IDs (from `AccountSpec.telegram_chat_ids`). If empty,
    /// the reporter falls back to `fallback_chat_ids` (the legacy global
    /// `telegram_report_chat_ids` env). This preserves single-account behavior
    /// where the global list is the only source.
    pub chat_ids: Vec<i64>,
}

pub async fn run(
    accounts: Vec<PerAccountReport>,
    bot_token: String,
    fallback_chat_ids: Vec<i64>,
    interval_mins: u64,
) {
    // Resolve effective chat targets per account (per-account → fallback).
    let effective: Vec<(PerAccountReport, Vec<i64>)> = accounts
        .into_iter()
        .map(|a| {
            let chats = if a.chat_ids.is_empty() {
                fallback_chat_ids.clone()
            } else {
                a.chat_ids.clone()
            };
            (a, chats)
        })
        .filter(|(_, chats)| !chats.is_empty())
        .collect();

    if effective.is_empty() {
        tracing::warn!("No report chat IDs configured — reporter disabled");
        return;
    }

    // Decide the title mode for each binding up front. Three modes:
    //   - `Legacy`: only one binding overall → "*BTC Scan Report — {exchange} Spot*"
    //     (preserves pre-Fase-1.5 byte-for-byte output)
    //   - `MultiAccount { id }`: multiple distinct ids, each single exchange
    //     → "*BTC Scan Report — [{id}]*"
    //   - `MultiExchange { id, exchange }`: same id has multiple exchanges
    //     → "*BTC Scan Report — [{id}/{exchange}]*"
    let total = effective.len();
    let distinct_ids: std::collections::HashSet<String> =
        effective.iter().map(|(a, _)| a.account_id.clone()).collect();
    let bindings_per_id = count_bindings_per_id(&effective);
    let multi = total > 1;

    tracing::info!(
        "BTC reporter started (every {} min) for {} binding(s) across {} account(s)",
        interval_mins, total, distinct_ids.len()
    );

    let mut tick = interval(Duration::from_secs(interval_mins * 60));
    // Lesson counters keyed by (account_id, exchange) so two bindings under
    // the same id don't share the same delta-detection window.
    let mut last_lesson_count: std::collections::HashMap<(String, ExchangeKind), usize> = effective
        .iter()
        .map(|(a, _)| ((a.account_id.clone(), a.exchange), a.mem.get_lessons().len()))
        .collect();

    loop {
        tick.tick().await;

        let bot = teloxide::prelude::Bot::new(&bot_token);
        for (acct, chat_ids) in &effective {
            let prev = last_lesson_count
                .get(&(acct.account_id.clone(), acct.exchange))
                .copied()
                .unwrap_or(0);

            // Async fetches (reporter is itself async).
            let snapshots = acct.state.all_snapshots().await;
            let recent: Vec<RecentDecision> = {
                let recents = acct.state.recent_decisions.read().await;
                recents.iter().rev().take(5).cloned().collect()
            };
            let all_lessons = acct.mem.get_lessons();
            let new_lessons: Vec<String> = if all_lessons.len() > prev {
                all_lessons[prev..].to_vec()
            } else {
                vec![]
            };

            // Skip empty reports to avoid Telegram spam.
            if snapshots.is_empty() && recent.is_empty() && new_lessons.is_empty() {
                continue;
            }

            // Per-binding title mode.
            let title = if total == 1 {
                ReportTitle::Legacy(acct.exchange.as_str())
            } else if bindings_per_id.get(&acct.account_id).copied().unwrap_or(1) > 1 {
                ReportTitle::MultiExchange(&acct.account_id, acct.exchange.as_str())
            } else {
                ReportTitle::MultiAccount(&acct.account_id)
            };

            let msg = format_report(title, &snapshots, &recent, &new_lessons);
            for chat_id in chat_ids {
                let chat = teloxide::prelude::ChatId(*chat_id);
                if let Err(e) = send_mdv2_safe(&bot, chat, &msg).await {
                    tracing::error!(
                        "Reporter [{}/{}]: failed to send to {}: {}",
                        acct.account_id, acct.exchange.as_str(), chat_id, e
                    );
                }
            }
        }
        // Update lesson counters AFTER the per-account send so the next tick
        // detects only genuinely new lessons.
        for (acct, _) in &effective {
            last_lesson_count.insert(
                (acct.account_id.clone(), acct.exchange),
                acct.mem.get_lessons().len(),
            );
        }

        // Multi-binding aggregate footer (sent once per loop to the first
        // binding's chat list to avoid spamming every chat with the same
        // digest).
        if multi {
            if let Some((_, first_chats)) = effective.first() {
                let total_btc: f64 = effective
                    .iter()
                    .map(|(a, _)| a.mem.get_treasury_state().current_btc)
                    .sum();
                let total_vault: f64 = effective
                    .iter()
                    .map(|(a, _)| a.mem.get_treasury_state().btc_treasury_vault)
                    .sum();
                let total_trades: u64 = effective
                    .iter()
                    .map(|(a, _)| a.mem.get_treasury_state().total_trades as u64)
                    .sum();
                let aggregate = format!(
                    "\n──────────\n*Aggregate — All Bindings*\nBTC: {:.8} \\| Vault: {:.8} \\| Trades: {}",
                    total_btc, total_vault, total_trades
                );
                for chat_id in first_chats {
                    let chat = teloxide::prelude::ChatId(*chat_id);
                    if let Err(e) = send_mdv2_safe(&bot, chat, &aggregate).await {
                        tracing::error!("Reporter [aggregate]: failed to send to {}: {}", chat_id, e);
                    }
                }
            }
        }
    }
}

/// How a report block should title itself. `Legacy` is the pre-Fase-1.5
/// single-binding title (no `[id]` prefix). `MultiAccount` and
/// `MultiExchange` add prefixes so multi-binding users can tell blocks apart.
#[derive(Debug, Clone, Copy)]
enum ReportTitle<'a> {
    Legacy(&'a str),                    // "*BTC Scan Report — {exchange} Spot*"
    MultiAccount(&'a str),              // "*BTC Scan Report — [{id}]*"
    MultiExchange(&'a str, &'a str),    // "*BTC Scan Report — [{id}/{exchange}]*"
}

fn count_bindings_per_id(
    effective: &[(PerAccountReport, Vec<i64>)],
) -> std::collections::HashMap<String, usize> {
    let mut map: std::collections::HashMap<String, usize> = std::collections::HashMap::new();
    for (a, _) in effective {
        *map.entry(a.account_id.clone()).or_insert(0) += 1;
    }
    map
}

fn format_report(
    title: ReportTitle<'_>,
    snapshots: &[crate::scanner::PairSnapshot],
    recent: &[RecentDecision],
    new_lessons: &[String],
) -> String {
    let mut lines: Vec<String> = Vec::new();

    // Single-binding legacy: "*BTC Scan Report — {exchange} Spot*" (e.g.
    // "Binance Spot"). This is byte-identical to pre-Fase-1.5 for the
    // single-account user.
    match title {
        ReportTitle::Legacy(exchange) => {
            lines.push(format!("*BTC Scan Report — {} Spot*\n", exchange));
        }
        ReportTitle::MultiAccount(id) => {
            lines.push(format!("*BTC Scan Report — [{}]*\n", id));
        }
        ReportTitle::MultiExchange(id, exchange) => {
            lines.push(format!("*BTC Scan Report — [{}/{}]*\n", id, exchange));
        }
    }

    let total_scanned: u64 = snapshots.iter().map(|s| s.stats.scanned).sum();
    let total_errors: u64 = snapshots.iter().map(|s| s.stats.errors).sum();

    if total_scanned == 0 && recent.is_empty() && new_lessons.is_empty() {
        lines.push("No scans in this period\\.".into());
        return lines.join("\n");
    }

    if !snapshots.is_empty() {
        // Count per-recommendation bucket for compact summary
        let approve_cnt = snapshots.iter().filter(|s| s.last_recommendation == "APPROVE").count();
        let monitor_cnt = snapshots.iter().filter(|s| s.last_recommendation == "MONITOR").count();
        let protect_cnt = snapshots.iter().filter(|s| {
            matches!(s.last_recommendation.as_str(), "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE")
        }).count();
        let reject_cnt = snapshots.iter().filter(|s| {
            !s.last_recommendation.is_empty()
                && !matches!(s.last_recommendation.as_str(),
                    "APPROVE" | "MONITOR" | "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE")
        }).count();

        lines.push(format!(
            "Pairs: {} \\| Scans: {} \\| Errors: {}\n✅ {} \\| 👁 {} \\| 🛡 {} \\| ❌ {}\n",
            snapshots.len(), total_scanned, total_errors,
            approve_cnt, monitor_cnt, protect_cnt, reject_cnt
        ));

        if snapshots.len() <= PAIR_DETAIL_THRESHOLD {
            // ── Detailed mode: one line per pair ────────────────────────
            for s in snapshots {
                let icon = match s.last_recommendation.as_str() {
                    "APPROVE" => "✅",
                    "MONITOR" => "👁",
                    "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => "🛡",
                    _ if s.last_recommendation.is_empty() => "⏳",
                    _ => "❌",
                };
                lines.push(format!(
                    "{} {} {} scans \\| {} \\(conf: {:.2}\\)",
                    icon,
                    escape_mdv2(&s.pair),
                    s.stats.scanned,
                    escape_mdv2(&s.last_recommendation),
                    s.last_confidence,
                ));
            }
        } else {
            // ── Compact mode: only show top APPROVE and notable pairs ───
            // Show up to 5 APPROVE pairs (highest confidence first)
            let mut approve_pairs: Vec<&crate::scanner::PairSnapshot> = snapshots
                .iter()
                .filter(|s| s.last_recommendation == "APPROVE")
                .collect();
            approve_pairs.sort_by(|a, b| b.last_confidence.partial_cmp(&a.last_confidence).unwrap_or(std::cmp::Ordering::Equal));

            if !approve_pairs.is_empty() {
                lines.push("*Top APPROVE pairs:*".into());
                for s in approve_pairs.iter().take(5) {
                    lines.push(format!(
                        "✅ {} conf:{:.2}",
                        escape_mdv2(&s.pair),
                        s.last_confidence,
                    ));
                }
            }
        }
    }

    if !recent.is_empty() {
        lines.push("\n*Recent Decisions:*".into());
        for (i, d) in recent.iter().enumerate() {
            let status_icon = match d.recommendation.as_str() {
                "APPROVE" => "✅",
                "MONITOR" => "👁",
                "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => "🛡",
                _ => "❌",
            };
            let time_short = if d.timestamp.len() > 16 {
                &d.timestamp[11..19]
            } else {
                &d.timestamp
            };
            // Truncate reason to avoid huge messages
            let reason_short = if d.reason.len() > MAX_REASON_CHARS {
                format!("{}…", &d.reason[..MAX_REASON_CHARS])
            } else {
                d.reason.clone()
            };
            lines.push(format!(
                "{}\\. {} {} {} \\- {} \\(conf: {:.2}, risk: {}\\)\n  \\_`{}`",
                i + 1,
                escape_mdv2(time_short),
                escape_mdv2(&d.pair),
                status_icon,
                escape_mdv2(&d.recommendation),
                d.confidence,
                escape_mdv2(&d.risk_level),
                escape_mdv2(&reason_short),
            ));
        }
    }

    if !new_lessons.is_empty() {
        lines.push("\n*New Lessons:*".into());
        for (i, lesson) in new_lessons.iter().take(3).enumerate() {
            let short = if lesson.len() > 150 {
                format!("{}...", &lesson[..147])
            } else {
                lesson.clone()
            };
            lines.push(format!(
                "{}{}\\. {}",
                i + 1,
                r"\.",
                escape_mdv2(&short),
            ));
        }
    }

    lines.join("\n")
}
