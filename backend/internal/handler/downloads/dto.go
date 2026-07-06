package downloads

// RunResultDTO is the JSON body of POST /api/downloads/run: an acknowledgement
// that an immediate download cycle has been kicked off. Mirrors the shape of
// handler/sources's WarmStartedDTO (POST /api/sources/warmup) — always
// {started:true}; the cycle itself runs via the async job.Runner, not inline.
type RunResultDTO struct {
	Started bool `json:"started"`
}
