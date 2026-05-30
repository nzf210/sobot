use std::fs;
use std::path::PathBuf;
use std::sync::RwLock;

use crate::models::*;

pub struct MemoryStore {
    data_dir: PathBuf,
    lock: RwLock<()>,
}

impl MemoryStore {
    pub fn new(data_dir: &str) -> Self {
        let dir = PathBuf::from(data_dir);
        fs::create_dir_all(&dir).expect("Failed to create data directory");
        let store = Self {
            data_dir: dir,
            lock: RwLock::new(()),
        };
        store.init_defaults();
        store
    }

    fn init_defaults(&self) {
        let defaults: Vec<(&str, &str)> = vec![
            ("btc-treasury.json", r#"{"current_btc":0,"previous_btc":0,"btc_growth_7d":0,"btc_growth_30d":0,"stable_value":0,"usdt_balance":0,"last_update":""}"#),
            ("btc-decision-log.json", "[]"),
            ("btc-config.json", r#"{"enabled":false,"llm_activation_threshold":0.75,"min_confidence":0.80,"max_exposure":0.50,"daily_loss_limit_btc":0.0005,"max_consecutive_losses":3,"safe_mode_volatility":9.0,"safe_mode_drawdown":0.05,"scanner_pairs":["BTCUSDT"]}"#),
            ("btc-positions.json", "[]"),
            ("btc-lessons.json", "[]"),
        ];

        // Write SKILL.md from source if exists, otherwise create default
        let skill_path = self.data_dir.join("SKILL.md");
        if !skill_path.exists() {
            // Try to copy from project root's SKILL.md
            let src_skill = PathBuf::from("SKILL.md");
            let skill_content = if src_skill.exists() {
                fs::read_to_string(&src_skill).unwrap_or_else(|_| "# BTC Treasury Advisor (Spot)\n- Autonomous Binance spot scanner\n- Market regime detection\n- Risk assessment\n- LLM reasoning".into())
            } else {
                "# BTC Treasury Advisor (Spot)\n- Autonomous Binance spot scanner\n- Market regime detection\n- Risk assessment\n- LLM reasoning".into()
            };
            fs::write(&skill_path, skill_content).expect("Failed to write SKILL.md");
        }

        for (filename, content) in defaults {
            let path = self.data_dir.join(filename);
            if !path.exists() {
                fs::write(&path, content).expect("Failed to write default file");
            }
        }
    }

    fn read_json<T: serde::de::DeserializeOwned>(&self, filename: &str, default: T) -> T {
        let _guard = self.lock.read().unwrap();
        let path = self.data_dir.join(filename);
        fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or(default)
    }

    fn write_json<T: serde::Serialize>(&self, filename: &str, data: &T) {
        let _guard = self.lock.write().unwrap();
        let path = self.data_dir.join(filename);
        let json = serde_json::to_string_pretty(data).expect("Failed to serialize");
        fs::write(&path, json).expect("Failed to write file");
    }

    pub fn get_treasury_state(&self) -> BtcTreasuryState {
        self.read_json("btc-treasury.json", BtcTreasuryState::default())
    }

    pub fn save_treasury_state(&self, mut state: BtcTreasuryState) {
        state.last_update = chrono::Utc::now().to_rfc3339();
        self.write_json("btc-treasury.json", &state);
    }

    pub fn log_decision(&self, record: BtcDecisionRecord) {
        let _guard = self.lock.write().unwrap();
        let path = self.data_dir.join("btc-decision-log.json");
        let mut records: Vec<BtcDecisionRecord> = fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        records.push(record);
        let json = serde_json::to_string_pretty(&records).unwrap();
        fs::write(&path, json).expect("Failed to write decision log");
    }

    pub fn get_decisions(&self) -> Vec<BtcDecisionRecord> {
        self.read_json("btc-decision-log.json", vec![])
    }

    pub fn get_config(&self) -> BtcConfig {
        self.read_json("btc-config.json", BtcConfig::default())
    }

    pub fn save_config(&self, config: &BtcConfig) {
        self.write_json("btc-config.json", config);
    }

    pub fn get_positions(&self) -> Vec<BtcAdvisoryPosition> {
        self.read_json("btc-positions.json", vec![])
    }

    #[allow(dead_code)]
    pub fn save_positions(&self, positions: &[BtcAdvisoryPosition]) {
        let _guard = self.lock.write().unwrap();
        let path = self.data_dir.join("btc-positions.json");
        let json = serde_json::to_string_pretty(positions).expect("Failed to serialize");
        fs::write(&path, json).expect("Failed to write file");
    }

    pub fn get_lessons(&self) -> Vec<String> {
        self.read_json("btc-lessons.json", vec![])
    }

    pub fn add_lesson(&self, lesson: String) {
        let _guard = self.lock.write().unwrap();
        let path = self.data_dir.join("btc-lessons.json");
        let mut lessons: Vec<String> = fs::read_to_string(&path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        lessons.push(lesson);
        let json = serde_json::to_string_pretty(&lessons).unwrap();
        fs::write(&path, json).expect("Failed to write lessons");
    }

    pub fn load_skills(&self) -> String {
        let path = self.data_dir.join("SKILL.md");
        fs::read_to_string(&path).unwrap_or_default()
    }

    pub fn load_lessons_context(&self) -> String {
        let lessons = self.get_lessons();
        if lessons.is_empty() {
            return String::new();
        }
        let recent: Vec<&String> = lessons.iter().rev().take(10).collect();
        format!(
            "\n\nRECENT SELF-LEARNING LESSONS (learn from these):\n{}",
            recent
                .iter()
                .enumerate()
                .map(|(i, l)| format!("{}. {}", i + 1, l))
                .collect::<Vec<_>>()
                .join("\n")
        )
    }
}
