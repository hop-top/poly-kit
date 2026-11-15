use std::time::Duration;

use futures_util::StreamExt;
use tokio_tungstenite::tungstenite::Message;

use crate::EngineError;

type Ws =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>;

/// Subscription to one event type on `/events`. Frames are
/// `{"type": ..., "data": ...}`; only frames whose `type` equals the
/// topic are yielded, as their `data`.
pub struct EventStream {
    url: String,
    topic: String,
    ws: Option<Ws>,
}

impl EventStream {
    pub(crate) fn new(url: String, topic: &str) -> Self {
        Self {
            url,
            topic: topic.to_owned(),
            ws: None,
        }
    }

    /// Next matching event. Reconnects after a dropped socket (1s
    /// backoff); returns `Err` only if the first connect fails.
    pub async fn next(&mut self) -> Result<serde_json::Value, EngineError> {
        let mut first = true;
        loop {
            if self.ws.is_none() {
                match tokio_tungstenite::connect_async(&self.url).await {
                    Ok((ws, _)) => self.ws = Some(ws),
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
                    if v.get("type").and_then(|x| x.as_str()) == Some(self.topic.as_str()) {
                        return Ok(v.get("data").cloned().unwrap_or(serde_json::Value::Null));
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
