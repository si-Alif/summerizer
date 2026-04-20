package data

type EmbeddingJob struct {
	ID        int64  `json:"id"`
	SourceID  int64  `json:"source_id"`
	Status    string `json:"status"`
	SourceVersion int    `json:"source_version"`
	Attempts  int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	Run_after int64  `json:"run_after"`
	Locked_at int64  `json:"locked_at,omitempty"`
	Locked_by int64  `json:"locked_by,omitempty"`
	StepError     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}