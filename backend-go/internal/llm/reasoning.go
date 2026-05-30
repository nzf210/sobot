package llm

type Response struct {
    Decision string `json:"decision"`
    Confidence float64 `json:"confidence"`
    NarrativeScore float64 `json:"narrative_score"`
    DLMMSuitability float64 `json:"dlmm_suitability"`
}

func Analyze() Response {
    return Response{
        Decision: "MICRO_ENTRY_ONLY",
        Confidence: 0.78,
        NarrativeScore: 0.72,
        DLMMSuitability: 0.61,
    }
}