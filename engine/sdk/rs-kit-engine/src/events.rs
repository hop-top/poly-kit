use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::Message;

use crate::EngineError;

type Ws =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>;

/// Subscription to one topic pattern on `/events`. Each connection
/// sends `{"type":"subscribe","topic":...}`; only `message` frames
/// (`{"type":"message","topic":...,"payload":...}`) whose topic matches
/// are yielded, as their `payload`. `welcome`, `ack`, `error` and
/// malformed frames are skipped. Patterns are the hub's: `*` one
/// segment, `**` one or more; the bare `*` means every topic.
pub struct EventStream {
    url: String,
    topic: String,
    token: Option<String>,
    ws: Option<Ws>,
}

/// Hub topic match: `*` one segment, `**` one or more; bare `*` is all.
fn matches(pattern: &str, topic: &str) -> bool {
    fn seg(p: &[&str], t: &[&str]) -> bool {
        match (p.first(), t.first()) {
            (None, _) => t.is_empty(),
            (_, None) => false,
            (Some(&"**"), _) => (1..=t.len()).any(|i| seg(&p[1..], &t[i..])),
            (Some(&ph), Some(&th)) => (ph == "*" || ph == th) && seg(&p[1..], &t[1..]),
        }
    }
    pattern == "*"
        || seg(
            &pattern.split('.').collect::<Vec<_>>(),
            &topic.split('.').collect::<Vec<_>>(),
        )
}

impl EventStream {
    pub(crate) fn new(url: String, topic: &str, token: Option<String>) -> Self {
        Self {
            url,
            topic: topic.to_owned(),
            token,
            ws: None,
        }
    }

    /// Open the socket and send the subscribe frame. The hub's `*` is
    /// one segment, so "everything" goes out as `**`.
    async fn connect(&self) -> Result<Ws, String> {
        let mut req = self
            .url
            .as_str()
            .into_client_request()
            .map_err(|e| e.to_string())?;
        if let Some(t) = self.token.as_deref().filter(|t| !t.is_empty()) {
            let v = format!("Bearer {t}").parse().map_err(|e| format!("{e}"))?;
            req.headers_mut().insert("Authorization", v);
        }
        let (mut ws, _) = tokio_tungstenite::connect_async(req)
            .await
            .map_err(|e| e.to_string())?;
        let topic = if self.topic == "*" {
            "**"
        } else {
            self.topic.as_str()
        };
        let frame = serde_json::json!({"type": "subscribe", "topic": topic});
        ws.send(Message::text(frame.to_string()))
            .await
            .map_err(|e| e.to_string())?;
        Ok(ws)
    }

    /// Next matching event. Reconnects and resubscribes after a dropped
    /// socket (1s backoff); returns `Err` only if the first connect fails.
    pub async fn next(&mut self) -> Result<serde_json::Value, EngineError> {
        let mut first = true;
        loop {
            if self.ws.is_none() {
                match self.connect().await {
                    Ok(ws) => self.ws = Some(ws),
                    Err(e) if first => {
                        return Err(EngineError::Startup(format!("events connect: {e}")))
                    }
                    Err(_) => {
                        tokio::time::sleep(Duration::from_secs(1)).await;
                        continue;
                    }
                }
            }
            first = false;
            match self.ws.as_mut().expect("connected").next().await {
                Some(Ok(Message::Text(t))) => {
                    let Ok(v) = serde_json::from_str::<serde_json::Value>(t.as_str()) else {
                        continue; // ignore malformed
                    };
                    if v.get("type").and_then(|x| x.as_str()) != Some("message") {
                        continue; // welcome / ack / error
                    }
                    let Some(topic) = v.get("topic").and_then(|x| x.as_str()) else {
                        continue;
                    };
                    if matches(&self.topic, topic) {
                        return Ok(v.get("payload").cloned().unwrap_or(serde_json::Value::Null));
                    }
                }
                Some(Ok(_)) => {}
                Some(Err(_)) | None => {
                    self.ws = None;
                    tokio::time::sleep(Duration::from_secs(1)).await;
                }
            }
        }
    }
}
