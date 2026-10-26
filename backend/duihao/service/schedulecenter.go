// 定时任务服务（M23）：库备份 / Redis 备份 / 日志切割。
package service

import (
	"fmt"

	"github.com/ys-ll/uniterm/backend/duihao/scheduler"
)

// ScheduleService 定时任务服务绑定。
type ScheduleService struct {
	mgr *scheduler.Manager
}

// NewScheduleService 构建（调度器在 main 里统一创建并已 Start）。
func NewScheduleService(m *scheduler.Manager) *ScheduleService {
	return &ScheduleService{mgr: m}
}

// ListScheduleTasks 任务列表（含 lastRun / nextRun）。
func (s *ScheduleService) ListScheduleTasks() []scheduler.Task {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.List()
}

// SaveScheduleTasks 整表保存（前端把整份列表提交上来）。
func (s *ScheduleService) SaveScheduleTasks(tasks []scheduler.Task) error {
	if s.mgr == nil {
		return fmt.Errorf("调度器尚未就绪")
	}
	return s.mgr.Save(tasks)
}

// RunScheduleTask 立刻执行一次（用户点「立即运行」）。
func (s *ScheduleService) RunScheduleTask(id string) (string, error) {
	if s.mgr == nil {
		return "", fmt.Errorf("调度器尚未就绪")
	}
	return s.mgr.RunNow(id)
}
