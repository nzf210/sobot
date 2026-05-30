use teloxide::prelude::*;
use teloxide::types::ParseMode;

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

/// Send a message with MarkdownV2 formatting. If Telegram rejects the
/// message (e.g. because of malformed MarkdownV2), automatically retry
/// as plain text so the user always sees something.
pub async fn send_mdv2_safe(
    bot: &Bot,
    chat_id: ChatId,
    text: &str,
) -> Result<Message, teloxide::RequestError> {
    match bot
        .send_message(chat_id, text)
        .parse_mode(ParseMode::MarkdownV2)
        .await
    {
        Ok(msg) => Ok(msg),
        Err(e) => {
            tracing::warn!(
                "MarkdownV2 send failed (falling back to plain text): {}",
                e
            );
            bot.send_message(chat_id, text).await
        }
    }
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
