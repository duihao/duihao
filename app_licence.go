package main

import (
	"context"
	"time"

	"github.com/duihao/duihao/backend/log"
	"github.com/duihao/duihao/licence"
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

// LicencePasswordLogin 用户名（或邮箱）+ 密码登录并绑定本机设备；成功后广播新状态。
func (a *App) LicencePasswordLogin(username, password string) (licence.State, error) {
	if a.licence == nil {
		return licence.State{}, context.Canceled
	}
	st, err := a.licence.PasswordLogin(username, password)
	if err != nil {
		return st, err
	}
	a.emit("licence:state", st)
	return st, nil
}

// ─────────────────────────────────────────────────────────────
//  会员中心（应用内购买）：/api/order* 透传
//  前端拿到的金额一律以「分」为单位，展示用服务端回传的 *_text 字段。
// ─────────────────────────────────────────────────────────────

// LicenceProducts 当前产品的可售规格（会员中心套餐卡）。需已登录。
func (a *App) LicenceProducts() (*licence.ProductsResp, error) {
	if a.licence == nil {
		return nil, context.Canceled
	}
	return a.licence.Products()
}

// LicenceOrderCreate 下单并发起支付（价格服务端重算；金额单位=分）。
// 返回 pay.type=qrcode 时拿 pay.qr 直接展示微信收款码。
func (a *App) LicenceOrderCreate(skuCode string, qty int) (*licence.OrderCreateResp, error) {
	if a.licence == nil {
		return nil, context.Canceled
	}
	return a.licence.OrderCreate(skuCode, qty)
}

// LicenceOrderStatus 查单（服务端 pending 时主动核对网关）。
// paid=true 表示已入账，entitlement 为最新权益；前端据此刷新登录态。
// Go 侧会在首个 paid 轮询时强制续签一次，状态真的变了才广播 —— 之前只写了
// 注释没发事件，NAS 主窗的档位/到期日要等重开窗口才变（用户报的问题）。
func (a *App) LicenceOrderStatus(orderNo string) (*licence.OrderStatusResp, error) {
	if a.licence == nil {
		return nil, context.Canceled
	}
	before := a.licence.State()
	resp, err := a.licence.OrderStatus(orderNo)
	if after := a.licence.State(); after != before {
		a.emit("licence:state", after)
		log.Writef("[licence] order %s -> plan=%s ent=%d", orderNo, after.Plan, after.Ent)
	}
	return resp, err
}

// LicenceFetchQR 把服务端下发的二维码图 URL 拉成本地 data URL。
// 前端 <img> 直连外链会受 CSP 与断网影响，统一由 Go 拉取转 base64。
func (a *App) LicenceFetchQR(qrURL string) (string, error) {
	if a.licence == nil {
		return "", context.Canceled
	}
	return a.licence.FetchQRData(qrURL)
}

// LicenceWechatQR 取微信扫码登录二维码，并直接把二维码图转成 data URL。
// 返回 scene/key 供 LicenceWechatPoll 成对轮询（key 是一次性密钥）。
func (a *App) LicenceWechatQR() (*licence.WechatQRInfo, error) {
	if a.licence == nil {
		return nil, context.Canceled
	}
	qrURL, scene, key, err := a.licence.WechatQR()
	if err != nil {
		return nil, err
	}
	dataURL, err := a.licence.FetchQRData(qrURL)
	if err != nil {
		return nil, err
	}
	return &licence.WechatQRInfo{DataURL: dataURL, Scene: scene, Key: key}, nil
}

// LicenceWechatPoll 轮询微信扫码结果。
// 返回 waiting=true 表示尚未扫码（前端 1.5s 后继续）；命中即登录完成，
// licence:state 随之广播（前端 applyState 即可）。扫码是「另一条登录路径」，
// 漏了这一步主窗问候行就不会变 —— 谁调 poll 谁只更新自己的窗。
func (a *App) LicenceWechatPoll(scene, key string) (*licence.WechatPollResult, error) {
	if a.licence == nil {
		return nil, context.Canceled
	}
	waiting, st, err := a.licence.WechatPoll(scene, key)
	if err != nil {
		return nil, err
	}
	if waiting {
		return &licence.WechatPollResult{Waiting: true}, nil
	}
	a.emit("licence:state", st)
	return &licence.WechatPollResult{Waiting: false, State: st}, nil
}
