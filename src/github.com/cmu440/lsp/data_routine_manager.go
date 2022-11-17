package lsp

import "time"

type dataRoutineManager struct {
	seqNum         int
	expectedSeqNum int
	timer          *time.Timer
}
