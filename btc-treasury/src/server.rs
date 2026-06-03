use std::sync::Arc;
use crate::engine::AdvisoryEngine;
use crate::memory::MemoryStore;
use crate::config::AppConfig;
pub struct BotShared {
    pub engine: Arc<AdvisoryEngine>,
    pub mem: Arc<MemoryStore>,
}
pub async fn init(cfg: &AppConfig) -> std::io::Result<BotShared> {
    let mem = Arc::new(MemoryStore::new(&cfg.data_dir));
    let engine = Arc::new(AdvisoryEngine::new(cfg.llm_url.clone(), cfg.llm_model.clone(), cfg.llm_api_key.clone(), mem.clone()));
    Ok(BotShared { engine, mem })
}
