package camoufoxnative

import (
	"context"
	"errors"
)

// ErrLiveViewAttached 表示账户浏览器已挂载远程操作窗口
var ErrLiveViewAttached = errors.New("账户浏览器已在远程操作中")

// LiveViewAvailable 报告当前 Worker 能否挂载远程操作窗口，不能时返回原因
func (worker *Worker) LiveViewAvailable() (bool, string) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return false, "Camoufox runtime 已关闭"
	}
	if worker.liveView != nil {
		return false, ErrLiveViewAttached.Error()
	}
	return true, ""
}

// HasLiveView 报告 Worker 是否正被远程操作窗口占用
func (worker *Worker) HasLiveView() bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.liveView != nil
}

// AttachLiveView 把远程操作窗口接到运行中的 Worker，返回停止画面与输入转发的函数；同一 Worker 同时只允许一个窗口
func (worker *Worker) AttachLiveView(ctx context.Context, view *LiveView) (func(), error) {
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	if worker.liveView != nil {
		worker.mu.Unlock()
		return nil, ErrLiveViewAttached
	}
	client := worker.client
	worker.liveView = view
	worker.mu.Unlock()

	stop, err := view.attachTo(ctx, client)
	if err != nil {
		worker.mu.Lock()
		worker.liveView = nil
		worker.mu.Unlock()
		return nil, err
	}
	return func() {
		stop()
		worker.mu.Lock()
		if worker.liveView == view {
			worker.liveView = nil
		}
		worker.mu.Unlock()
	}, nil
}
