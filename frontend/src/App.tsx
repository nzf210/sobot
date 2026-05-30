import { useState, useEffect } from 'react'

function App() {
  const [currentTime, setCurrentTime] = useState(new Date().toLocaleTimeString())

  useEffect(() => {
    const timer = setInterval(() => {
      setCurrentTime(new Date().toLocaleTimeString())
    }, 1000)
    return () => clearInterval(timer)
  }, [])

  const activities = [
    { id: 1, type: 'analyze', title: 'Analyzed Token $PEPE', time: '2 mins ago', action: 'ANALYZED' },
    { id: 2, type: 'buy', title: 'Sniper executed buy on $DOGE', time: '15 mins ago', action: 'BOUGHT' },
    { id: 3, type: 'sell', title: 'Sold $SHIB position for +12%', time: '1 hour ago', action: 'SOLD' },
    { id: 4, type: 'analyze', title: 'LLM rejected $SCAM token', time: '3 hours ago', action: 'REJECTED' },
  ]

  return (
    <div className="app-container">
      <header className="header">
        <h1>Hybrid Orchestrator</h1>
        <div className="status-badge glass-panel">
          <div className="status-indicator"></div>
          System Active
        </div>
      </header>

      <div className="dashboard-grid">
        <div className="stat-card glass-panel">
          <div className="stat-title">Total Profit (24h)</div>
          <div className="stat-value">$1,245.80</div>
          <div className="stat-trend trend-up">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M23 6l-9.5 9.5-5-5L1 18" />
              <path d="M17 6h6v6" />
            </svg>
            +12.5% vs yesterday
          </div>
        </div>

        <div className="stat-card glass-panel">
          <div className="stat-title">Active Positions</div>
          <div className="stat-value">3 / 5</div>
          <div className="stat-trend" style={{ color: 'var(--text-muted)' }}>
            Capacity at 60%
          </div>
        </div>

        <div className="stat-card glass-panel">
          <div className="stat-title">Tokens Analyzed</div>
          <div className="stat-value">142</div>
          <div className="stat-trend trend-up">
            Since last boot
          </div>
        </div>
      </div>

      <div className="activity-feed glass-panel">
        <h2 className="activity-header">Recent Activity</h2>
        {activities.map((activity) => (
          <div className="activity-item" key={activity.id}>
            <div className="activity-info">
              <div className="activity-title">{activity.title}</div>
              <div className="activity-time">{activity.time}</div>
            </div>
            <div className={`activity-action action-${activity.type}`}>
              {activity.action}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export default App
