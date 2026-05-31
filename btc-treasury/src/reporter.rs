use std::sync::Arc;

use tokio::time::{interval, Duration};

use crate::format::{escape_mdv2, send_mdv2_safe};
use crate::scanner::{ScannerState, RecentDecision};
use crate::memory::MemoryStore;

pub async fn run(
    state: Arc<ScannerState>,
    mem: Arc<MemoryStore>,
    bot_token: String,
    chat_ids: Vec<i64>,
    interval_mins: u64,
) {
    if chat_ids.is_empty() {
        tracing::warn!("No report chat IDs configured — reporter disabled");
        return;
    }

    let mut tick = interval(Duration::from_secs(interval_mins * 60));
    let mut last_lesson_count = mem.get_lessons().len();
    tracing::info!("BTC reporter started (every {} min) to {} chat(s)", interval_mins, chat_ids.len());

    loop {
        tick.tick().await;

        let snapshots = state.all_snapshots().await;

        let recent: Vec<RecentDecision> = {
            let recents = state.recent_decisions.read().await;
            recents.iter().rev().take(5).cloned().collect()
        };

        let all_lessons = mem.get_lessons();
        let new_lessons: Vec<String> = if all_lessons.len() > last_lesson_count {
            all_lessons[last_lesson_count..].to_vec()
        } else {
            vec![]
        };
        last_lesson_count = all_lessons.len();

        let msg = format_report(&snapshots, &recent, &new_lessons);

        let bot = teloxide::prelude::Bot::new(&bot_token);
        for chat_id in &chat_ids {
            let chat = teloxide::prelude::ChatId(*chat_id);
            if let Err(e) = send_mdv2_safe(&bot, chat, &msg).await {
                tracing::error!("Reporter: failed to send to {}: {}", chat_id, e);
            }
        }
    }
}

fn format_report(
    snapshots: &[crate::scanner::PairSnapshot],
    recent: &[RecentDecision],
    new_lessons: &[String],
) -> String {
    let mut lines: Vec<String> = Vec::new();

    lines.push("*BTC Scan Report — Binance Spot*\n".into());

    let total_scanned: u64 = snapshots.iter().map(|s| s.stats.scanned).sum();
    let total_errors: u64 = snapshots.iter().map(|s| s.stats.errors).sum();

    if total_scanned == 0 && recent.is_empty() && new_lessons.is_empty() {
        lines.push("No scans in this period\\.".into());
        return lines.join("\n");
    }

    if !snapshots.is_empty() {
        lines.push(format!("Pairs: {} \\| Scans: {} \\| Errors: {}\n", snapshots.len(), total_scanned, total_errors));
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
            lines.push(format!(
                "{}\\. {} {} {} \\- {} \\(conf: {:.2}, risk: {}\\)\n  \\_`{}`",
                i + 1,
                escape_mdv2(time_short),
                escape_mdv2(&d.pair),
                status_icon,
                escape_mdv2(&d.recommendation),
                d.confidence,
                escape_mdv2(&d.risk_level),
                escape_mdv2(&d.reason),
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
