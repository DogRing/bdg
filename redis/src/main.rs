use axum::{
    extract::{Path, State},
    response::sse::{Event, KeepAlive, Sse},
    routing::get,
    Router,
};
use futures::Stream;
use redis::AsyncCommands;
use serde_json::json;
use std::{collections::HashMap, convert::Infallible, sync::Arc, time::Duration};
use tokio::sync::{broadcast, Mutex};
use tokio_stream::{wrappers::BroadcastStream, StreamExt};
use tower_http::cors::CorsLayer;

// ─── 상수 ────────────────────────────────────────────────────────────────────

const BROADCAST_CAPACITY: usize = 32;
const SSE_KEEPALIVE_SECS: u64 = 15;
const REDIS_KEYSPACE_PATTERN: &str = "__keyspace@0__:board:*";
const BOARD_KEY_PREFIX: &str = "board:";
const KEYSPACE_PREFIX: &str = "__keyspace@0__:";

// ─── 타입 별칭 ──────────────────────────────────────────────────────────────

type RoomSenders = Arc<Mutex<HashMap<String, broadcast::Sender<String>>>>;

// ─── 상태 ────────────────────────────────────────────────────────────────────

#[derive(Clone)]
struct AppState {
    senders: RoomSenders,
    redis: redis::Client,
}

impl AppState {
    fn new(redis: redis::Client) -> Self {
        Self {
            senders: Arc::new(Mutex::new(HashMap::new())),
            redis,
        }
    }
}

async fn fetch_board_bytes(redis: &redis::Client, room_id: &str) -> Option<Vec<u8>> {
    let mut conn = redis.get_multiplexed_async_connection().await.ok()?;
    let key = format!("{BOARD_KEY_PREFIX}{room_id}");
    let bytes: Vec<u8> = conn.get(key).await.ok()?;
    if bytes.is_empty() { None } else { Some(bytes) }
}

fn room_id_from_channel(channel: &str) -> Option<&str> {
    channel
        .strip_prefix(KEYSPACE_PREFIX)
        .and_then(|s| s.strip_prefix(BOARD_KEY_PREFIX))
}

fn make_snapshot_event(room_id: &str, board: &[u8]) -> Event {
    let board_str = String::from_utf8_lossy(board);
    let payload = json!({
        "type": "snapshot",
        "board": board_str,
        "roomId": room_id,
    });
    Event::default().event("snapshot").data(payload.to_string())
}

fn make_update_payload(room_id: &str, board: &[u8]) -> String {
    let board_str = String::from_utf8_lossy(board);
    json!({
        "type": "board_update",
        "board": board_str,
        "roomId": room_id,
    })
    .to_string()
}

async fn sse_handler(
    Path(room_id): Path<String>,
    State(state): State<AppState>,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let rx = subscribe_to_room(&state.senders, &room_id).await;
    let snapshot = build_snapshot_event(&state.redis, &room_id).await;
    let stream = build_sse_stream(snapshot, rx);

    Sse::new(stream).keep_alive(
        KeepAlive::new()
            .interval(Duration::from_secs(SSE_KEEPALIVE_SECS))
            .text("ping"),
    )
}

async fn subscribe_to_room(
    senders: &RoomSenders,
    room_id: &str,
) -> broadcast::Receiver<String> {
    let mut map = senders.lock().await;
    let tx = map.entry(room_id.to_string()).or_insert_with(|| {
        let (tx, _) = broadcast::channel(BROADCAST_CAPACITY);
        tx
    });
    tx.subscribe()
}

async fn build_snapshot_event(
    redis: &redis::Client,
    room_id: &str,
) -> Option<Result<Event, Infallible>> {
    let bytes = fetch_board_bytes(redis, room_id).await?;
    Some(Ok(make_snapshot_event(room_id, &bytes)))
}

fn build_sse_stream(
    snapshot: Option<Result<Event, Infallible>>,
    rx: broadcast::Receiver<String>,
) -> impl Stream<Item = Result<Event, Infallible>> {
    let update_stream = BroadcastStream::new(rx)
        .filter_map(|msg| msg.ok())
        .map(|data| Ok(Event::default().event("board_update").data(data)));

    async_stream::stream! {
        if let Some(ev) = snapshot {
            yield ev;
        }
        tokio::pin!(update_stream);
        while let Some(ev) = update_stream.next().await {
            yield ev;
        }
    }
}

async fn redis_watcher(state: AppState) {
    let pubsub = match connect_pubsub(&state.redis).await {
        Some(p) => p,
        None => return,
    };

    println!("Redis keyspace 구독 시작");
    let mut msg_stream = pubsub.into_on_message();

    while let Some(msg) = msg_stream.next().await {
        let channel = msg.get_channel_name().to_string();
        if let Some(room_id) = room_id_from_channel(&channel) {
            handle_board_change(&state, room_id).await;
        }
    }
}

async fn connect_pubsub(redis: &redis::Client) -> Option<redis::aio::PubSub> {
    let conn = redis.get_async_connection().await
        .map_err(|e| eprintln!("Redis 연결 실패: {e}"))
        .ok()?;

    let mut pubsub = conn.into_pubsub();
    pubsub.psubscribe(REDIS_KEYSPACE_PATTERN).await
        .map_err(|e| eprintln!("psubscribe 실패: {e}"))
        .ok()?;

    Some(pubsub)
}

async fn handle_board_change(state: &AppState, room_id: &str) {
    let Some(bytes) = fetch_board_bytes(&state.redis, room_id).await else {
        return;
    };

    let payload = make_update_payload(room_id, &bytes);

    let senders = state.senders.lock().await;
    if let Some(tx) = senders.get(room_id) {
        let receiver_count = tx.receiver_count();
        if tx.send(payload).is_ok() {
            println!("board:{room_id} 변경 → {receiver_count} 클라이언트 전송");
        }
    }
}

// ─── 진입점 ──────────────────────────────────────────────────────────────────

#[tokio::main]
async fn main() {
    let redis_url = std::env::var("REDIS_URL")
        .unwrap_or_else(|_| "redis://redis.dogring.kr/".to_string());

    let redis_client = redis::Client::open(redis_url)
        .expect("Redis 클라이언트 생성 실패");

    let state = AppState::new(redis_client);

    tokio::spawn(redis_watcher(state.clone()));

    let app = Router::new()
        .route("/sse/room/:room_id", get(sse_handler))
        .layer(CorsLayer::permissive())
        .with_state(state);

    let addr = "0.0.0.0:3001";
    println!("SSE Server: http://{addr}");

    axum::serve(
        tokio::net::TcpListener::bind(addr).await.unwrap(),
        app,
    )
    .await
    .unwrap();
}