package main

import (
	"context"
	"time"

	"github.com/ys-ll/uniterm/backend/log"
	"github.com/ys-ll/uniterm/licence"
)

// ─────────────────────────────────────────────────────────────
// B6 授权门禁：App ↔ licence.Manager 桥接
//
// 原则（实施清单 §B6）：
//   - 启动不弹窗、不锁死：Verify 失败一律按 free 继续，前端按 Allows 降级；
//   - 门禁唯一入口 Allows(cap)，前端不参与判权；
//   - 静默续签失败绝不回退登录态；
//   - 本地文件 / 既有站点永远可用（不受任何 cap 约束）。
// ─────────────────────────────────────────────────────────────

// licenceRefreshInterval：低频续签巡检。MaybeRefresh 自带「exp 不足 7 天才真正
// 出网」的判断，这里转得再勤也只是每小时查一次内存快照，不出网。
const licenceRefreshInterval = time.Hour

// initLicence 构建授权管理器并启动后台校验。initStores 末尾调用（首次运行
// 由 SetDataDir 走同一条路径），dataDir 提供 keys.json 公钥缓存的落盘位置。
func (a *App) initLicence(dataDir string) {
	mgr := licence.New(dataDir)
	a.licence = mgr

	go func() {
		// Verify 采集设备指纹（Windows 走 WMI，可能数百 ms），放 goroutine
		// 不阻塞启动；首帧前端拿到的 State 是 free，结果出来后补发事件。
		st := mgr.Verify()
		a.emit("licence:state", st)
		log.Writef("[licence] verify: plan=%s ent=%d reason=%s", st.Plan, st.Ent, st.Reason)

		mgr.MaybeRefresh()
		if st2 := mgr.State(); st2 != st {
			a.emit("licence:state", st2)
		}

		ticker := time.NewTicker(licenceRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if a.ctx == nil {
					continue
				}
				// MaybeRefresh 出网（仅当快到期）；状态有变才广播，
				// 前端据此刷新门禁 UI（如到期降级提示）。
				before := mgr.State()
				mgr.MaybeRefresh()
				if after := mgr.State(); after != before {
					a.emit("licence:state", after)
					log.Writef("[licence] state changed: plan=%s reason=%s", after.Plan, after.Reason)
				}
			case <-a.ctx.Done():
				return
			}
		}
	}()
}

// LicenceState 当前授权状态（前端只读）。
func (a *App) LicenceState() licence.State {
	if a.licence == nil {
		return licence.State{LoggedIn: false, Plan: licence.PlanFree, PlanText: "免费版", Reason: "not_ready"}
	}
	return a.licence.State()
}

// LicenceAllows 门禁唯一入口。cap ∈ {"paid","annual","lifetime"}，未知能力一律 false。
// 典型用法：AI 助手 / 云同步等付费能力在调用前先问 Allows("paid")。
func (a *App) LicenceAllows(cap string) bool {
	if a.licence == nil {
		return false
	}
	return a.licence.Allows(cap)
}

// LicenceSendCode 发送登录验证码（/api/auth/sms）。
func (a *App) LicenceSendCode(mobile string) error {
	if a.licence == nil {
		return context.Canceled
	}
	return a.licence.SendCode(mobile)
}

// LicenceLogin 手机号 + 验证码登录并绑定本机设备；成功后广播新状态。
func (a *App) LicenceLogin(mobile, code string) (licence.State, error) {
	if a.licence == nil {
		return licence.State{}, context.Canceled
	}
	st, err := a.licence.Login(mobile, code)
	if err != nil {
		return st, err
	}
	a.emit("licence:state", st)
	return st, nil
}

// LicenceLogout 清除本机令牌并广播降级为 free。
// 注意：只清本机，服务器侧设备解绑走后台自助（30 天内可恢复）。
func (a *App) LicenceLogout() error {
	if a.licence == nil {
		return nil
	}
	if err := a.licence.Logout(); err != nil {
		return err
	}
	a.emit("licence:state", a.licence.State())
	return nil
}
