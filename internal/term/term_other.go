//go:build !darwin && !linux && !windows

package term

import (
	"errors"
	"os"
)

func resizeSignals() []os.Signal    { return nil }
func isResizeSignal(os.Signal) bool { return false }
func exitSignals() []os.Signal      { return []os.Signal{os.Interrupt} }

// sysTerm は、対応していない OS での空の実装。Open は常に失敗する。
type sysTerm struct{}

func openSys(Options) (*sysTerm, error)                           { return nil, errors.ErrUnsupported }
func (*sysTerm) write([]byte) error                               { return errors.ErrUnsupported }
func (*sysTerm) size() (int, int, error)                          { return 0, 0, errors.ErrUnsupported }
func (*sysTerm) readLoop(func(Input) bool, <-chan struct{}) error { return errors.ErrUnsupported }
func (*sysTerm) wake()                                            {}
func (*sysTerm) restore() error                                   { return nil }
func (*sysTerm) info() map[string]string                          { return map[string]string{} }
