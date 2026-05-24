package ingest_test

import "github.com/szymonrychu/tatara-memory/internal/ingest"

var _ ingest.JobStore = (*ingest.PGStore)(nil)
