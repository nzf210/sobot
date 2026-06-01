use std::sync::Arc;

use actix_web::{web, App, HttpResponse, HttpServer};
use chrono::Utc;

use crate::config::AppConfig;
use crate::engine::AdvisoryEngine;
use crate::memory::MemoryStore;
use crate::models::*;

pub struct AppState {
    pub engine: Arc<AdvisoryEngine>,
    pub mem: Arc<MemoryStore>,
}

pub struct ServerShared {
    pub engine: Arc<AdvisoryEngine>,
    pub mem: Arc<MemoryStore>,
}

pub async fn run(cfg: &AppConfig) -> std::io::Result<ServerShared> {
    let mem = Arc::new(MemoryStore::new(&cfg.data_dir));
    let engine = Arc::new(AdvisoryEngine::new(
        cfg.llm_url.clone(),
        cfg.llm_model.clone(),
        cfg.llm_api_key.clone(),
        mem.clone(),
    ));

    let state = web::Data::new(AppState {
        engine: engine.clone(),
        mem: mem.clone(),
    });

    let shared = ServerShared { engine, mem };

    let bind_addr = format!("0.0.0.0:{}", cfg.backend_port);
    tracing::info!("BTC Treasury API starting on {}", bind_addr);

    let server = HttpServer::new(move || {
        App::new()
            .app_data(state.clone())
            .route("/health", web::get().to(health))
            .route("/btc/advisory", web::post().to(advisory))
            .route("/btc/treasury", web::get().to(get_treasury))
            .route("/btc/treasury", web::post().to(update_treasury))
            .route("/btc/market", web::post().to(market_update))
            .route("/btc/positions", web::get().to(get_positions))
            .route("/btc/config", web::get().to(get_config))
            .route("/btc/config", web::post().to(update_config))
    })
    .bind(&bind_addr)?
    .run();

    tokio::spawn(server);

    Ok(shared)
}

async fn health() -> HttpResponse {
    HttpResponse::Ok().json(serde_json::json!({"status": "ok"}))
}

async fn advisory(
    state: web::Data<AppState>,
    body: web::Json<BtcAdvisoryInput>,
) -> HttpResponse {
    let input = body.into_inner();
    let advisory = state.engine.analyze(&input).await;

    let record = BtcDecisionRecord {
        timestamp: Utc::now().to_rfc3339(),
        treasury_before: state.mem.get_treasury_state(),
        treasury_after: state.mem.get_treasury_state(),
        market_data: input.market_data,
        action_taken: advisory.recommendation.clone(),
        advisory: advisory.clone(),
    };
    state.mem.log_decision(record);

    HttpResponse::Ok().json(advisory)
}

async fn get_treasury(state: web::Data<AppState>) -> HttpResponse {
    let ts = state.mem.get_treasury_state();
    HttpResponse::Ok().json(ts)
}

async fn update_treasury(
    state: web::Data<AppState>,
    body: web::Json<BtcTreasuryState>,
) -> HttpResponse {
    state.mem.save_treasury_state(body.into_inner());
    let ts = state.mem.get_treasury_state();
    HttpResponse::Ok().json(ts)
}

async fn market_update(
    state: web::Data<AppState>,
    body: web::Json<BtcMarketData>,
) -> HttpResponse {
    let market_data = body.into_inner();
    let treasury = state.mem.get_treasury_state();
    let positions = state.mem.get_positions();

    let loss_streak = {
        let mut streak = 0;
        for pos in positions.iter().rev() {
            if pos.pnl_btc < 0.0 {
                streak += 1;
            } else {
                break;
            }
        }
        streak
    };

    let input = BtcAdvisoryInput {
        market_data: market_data.clone(),
        treasury,
        open_positions: positions,
        loss_streak,
        ai_score: None,
        risk_assessment: None,
        pair_metrics: None,
    };

    let advisory = state.engine.analyze(&input).await;

    let record = BtcDecisionRecord {
        timestamp: Utc::now().to_rfc3339(),
        treasury_before: state.mem.get_treasury_state(),
        treasury_after: state.mem.get_treasury_state(),
        market_data,
        action_taken: advisory.recommendation.clone(),
        advisory: advisory.clone(),
    };
    state.mem.log_decision(record);

    HttpResponse::Ok().json(advisory)
}

async fn get_positions(state: web::Data<AppState>) -> HttpResponse {
    let positions = state.mem.get_positions();
    HttpResponse::Ok().json(positions)
}

async fn get_config(state: web::Data<AppState>) -> HttpResponse {
    let cfg = state.mem.get_config();
    HttpResponse::Ok().json(cfg)
}

async fn update_config(
    state: web::Data<AppState>,
    body: web::Json<BtcConfig>,
) -> HttpResponse {
    state.mem.save_config(&body.into_inner());
    let cfg = state.mem.get_config();
    HttpResponse::Ok().json(cfg)
}
