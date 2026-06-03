use teloxide::prelude::*;
use teloxide::types::ParseMode;

/// Telegram hard limit for a single message (4096 chars). We use a slightly
/// smaller budget so there is headroom for any extra escaping added at the
/// call-site or by Telegram's own parser.
const MAX_TG_LEN: usize = 4000;

/// Escape all characters that Telegram's MarkdownV2 parser treats as special.
/// Characters: _ * [ ] ( ) ~ ` > # + - = | { } . !
///
/// Since `.` and `-` appear frequently in dynamic data (timestamps, decimals),
/// this function is mandatory before embedding ANY dynamic string inside a
/// MarkdownV2-formatted message.
pub fn escape_mdv2(s: &str) -> String {
    s.replace('\\', r"\\")
        .replace('_', r"\_")
        .replace('*', r"\*")
        .replace('[', r"\[")
        .replace(']', r"\]")
        .replace('(', r"\(")
        .replace(')', r"\)")
        .replace('~', r"\~")
        .replace('`', r"\`")
        .replace('>', r"\>")
        .replace('#', r"\#")
        .replace('+', r"\+")
        .replace('-', r"\-")
        .replace('=', r"\=")
        .replace('|', r"\|")
        .replace('{', r"\{")
        .replace('}', r"\}")
        .replace('.', r"\.")
        .replace('!', r"\!")
}

/// Split `text` into chunks each ≤ `max_len` chars, breaking on newlines
/// whenever possible. This prevents splitting inside a MarkdownV2 entity.
fn chunk_text(text: &str, max_len: usize) -> Vec<String> {
    if text.len() <= max_len {
        return vec![text.to_string()];
    }

    let mut chunks: Vec<String> = Vec::new();
    let mut current = String::new();

    for line in text.split('\n') {
        // +1 accounts for the newline we will re-add
        let needed = if current.is_empty() {
            line.len()
        } else {
            current.len() + 1 + line.len()
        };

        if needed > max_len && !current.is_empty() {
            // Flush current chunk and start a new one
            chunks.push(current.trim_end_matches('\n').to_string());
            current = String::new();
        }

        // Edge case: a single line that is itself longer than max_len
        if line.len() > max_len {
            // Hard-split at character boundary
            let mut remaining = line;
            while !remaining.is_empty() {
                let split_at = remaining
                    .char_indices()
                    .take_while(|(i, _)| *i < max_len)
                    .last()
                    .map(|(i, c)| i + c.len_utf8())
                    .unwrap_or(max_len.min(remaining.len()));
                chunks.push(remaining[..split_at].to_string());
                remaining = &remaining[split_at..];
            }
            continue;
        }

        if !current.is_empty() {
            current.push('\n');
        }
        current.push_str(line);
    }

    if !current.is_empty() {
        chunks.push(current);
    }

    chunks
}

/// Send a message with MarkdownV2 formatting. If the message exceeds
/// Telegram's 4096-char limit it is automatically split into multiple
/// sequential messages. If Telegram rejects a chunk (e.g. malformed
/// MarkdownV2), that chunk is retried as plain text so the user always sees
/// something.
pub async fn send_mdv2_safe(
    bot: &Bot,
    chat_id: ChatId,
    text: &str,
) -> Result<Message, teloxide::RequestError> {
    let chunks = chunk_text(text, MAX_TG_LEN);
    let total = chunks.len();

    let mut last_msg: Option<Message> = None;
    for (i, chunk) in chunks.iter().enumerate() {
        // For multi-chunk messages, annotate the part number so the user
        // can see when a report has been split.
        let body: String = if total > 1 {
            format!("_{}/{}_\n{}", i + 1, total, chunk)
        } else {
            chunk.clone()
        };

        let result = bot
            .send_message(chat_id, &body)
            .parse_mode(ParseMode::MarkdownV2)
            .await;

        let msg = match result {
            Ok(m) => m,
            Err(e) => {
                tracing::warn!(
                    "MarkdownV2 send failed (chunk {}/{}, falling back to plain text): {}",
                    i + 1,
                    total,
                    e
                );
                // Fallback: strip the part-header and send as plain text.
                // Use the raw chunk so escaping artifacts don't appear.
                bot.send_message(chat_id, chunk).await?
            }
        };
        last_msg = Some(msg);
    }

    // Unwrap is safe: chunks is non-empty (we return early if text is empty).
    Ok(last_msg.unwrap_or_else(|| {
        unreachable!("chunk_text must produce at least one chunk for non-empty text")
    }))
}

/// Convenience — call on a `Message` received from the user.
#[allow(dead_code)]
pub async fn bot_send_mdv2(
    bot: &Bot,
    msg: &Message,
    text: &str,
) -> Result<Message, teloxide::RequestError> {
    send_mdv2_safe(bot, msg.chat.id, text).await
}

/// Simple plain-text send (no formatting, no escaping needed).
pub async fn bot_send_plain(
    bot: &Bot,
    msg: &Message,
    text: &str,
) -> Result<(), teloxide::RequestError> {
    bot.send_message(msg.chat.id, text).await?;
    Ok(())
}
