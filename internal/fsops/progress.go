package fsops

import "strconv"

// Stage は進捗で報告する処理の段階。
type Stage int

const (
	StageCopy Stage = iota + 1
	StageMove       // 同一ボリュームの移動
	StageVerify
	StageRemoveSource
	StageTrash
	StageDelete
)

func (s Stage) String() string {
	switch s {
	case StageCopy:
		return "StageCopy"
	case StageMove:
		return "StageMove"
	case StageVerify:
		return "StageVerify"
	case StageRemoveSource:
		return "StageRemoveSource"
	case StageTrash:
		return "StageTrash"
	case StageDelete:
		return "StageDelete"
	}
	return "Stage(" + strconv.Itoa(int(s)) + ")"
}

// Progress は ExecOptions.Progress に渡す進捗（SPEC §16）。
type Progress struct {
	Stage      Stage
	Current    string // 処理中のパス
	DoneFiles  int
	TotalFiles int
	DoneBytes  int64
	TotalBytes int64
}
