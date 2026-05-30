package memory

type HistoricalMemory struct {}

func New() *HistoricalMemory {
    return &HistoricalMemory{}
}

func (m *HistoricalMemory) HasBadHistory(deployer string) bool {
    return false
}