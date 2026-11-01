package camera

import (
	"io"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/childproc"
)

// childProc 监控内部用的子进程句柄。真正的实现在 childproc 包（下载模块的
// aria2c 也用同一套「隐藏黑框 + Job Object 兜底回收」），这里只是套一层，
// 保持包内原来的 p.running() / p.stop() 调用点不变。
type childProc struct{ *childproc.Proc }

func (p *childProc) running() bool {
	return p != nil && p.Proc != nil && p.Proc.Running()
}

func (p *childProc) stop() {
	if p != nil && p.Proc != nil {
		p.Proc.Stop()
	}
}

// startChild 启动受管子进程。stdoutPipe=true 时返回 stdout 管道（MJPEG 预览用），
// 否则 stdout/stderr 都追加到 logPath。
func startChild(bin string, args []string, logPath string, stdoutPipe bool) (*childProc, io.ReadCloser, error) {
	p, r, err := childproc.Start(bin, args, childproc.Options{LogPath: logPath, StdoutPipe: stdoutPipe})
	if err != nil {
		return nil, nil, err
	}
	return &childProc{p}, r, nil
}
