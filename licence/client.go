package licence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultBaseURL 是对号官网 API 根（/api 前缀独立分发，不走前台路由）。
const defaultBaseURL = "https://duihao.cn"

// Client 是 /api/auth* 的最小客户端。超时压短（3~5s）：登录是同步链路，
// 默认 8s 会把登录页卡死（与服务端 wechat_send timeout=2 同一纪律）。
type Client struct {
	Base string
	HTTP *http.Client
}

func NewClient(base string) *Client {
	// Proxy 显式置 nil：默认 Transport 会读 HTTP(S)_PROXY / 系统代理，
	// 在代理环境下轮询与登录 POST 会被拖到 2~3s（实测直连 120ms），
	// 1s 一拍的状态轮询因此「看起来没在检测」。连官网走直连即可。
	tr := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Client{
		Base: strings.TrimRight(base, "/"),
		HTTP: &http.Client{Timeout: 5 * time.Second, Transport: tr},
	}
}

// Entitlement 与服务端 auth_api_ent() 的键一一对应。
type Entitlement struct {
	Product string `json:"product"`
	Plan    string `json:"plan"`
	Ent     int64  `json:"ent"`
	Seats   int    `json:"seats"`
}

type apiResp struct {
	OK    bool    `json:"ok"`
	Error *apiErr `json:"error"`
}

type apiErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *Client) post(path string, in any) (json.RawMessage, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *Client) get(path string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (json.RawMessage, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("网络不可用，请检查网络后重试")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("响应读取失败")
	}
	var r apiResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("响应解析失败（HTTP %d）", resp.StatusCode)
	}
	if !r.OK {
		msg := "请求失败"
		code := ""
		if r.Error != nil {
			msg = r.Error.Message
			code = r.Error.Code
		}
		return nil, &APIError{Code: code, Message: msg, Status: resp.StatusCode}
	}
	// ★ 服务端成功包裹是扁平结构（{ok, token, entitlement, ...} 同级），
	//   不存在 data 嵌套 —— 返回完整响应体，由各方法从顶层解自己的键。
	return raw, nil
}

// APIError 携带服务端错误码（E_SEAT/E_REVOKED/E_LOGIN…），前端按码提示。
type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string { return e.Message }

// SendCode：POST /api/auth/sms {mobile}。
func (c *Client) SendCode(mobile string) error {
	_, err := c.post("/api/auth/sms", map[string]string{"mobile": mobile})
	return err
}

type loginResp struct {
	Token       string      `json:"token"`
	TokenTTL    int         `json:"token_ttl"`
	User        struct {
		Uid      int    `json:"uid"`
		Nickname string `json:"nickname"`
		Mobile   string `json:"mobile"` // 已打码（如 138****5678）
	} `json:"user"`
	Entitlement Entitlement `json:"entitlement"`
}

// PasswordLogin：POST /api/auth/login {mode:'password', username, password, ...}。
// 与验证码登录同端点，服务端按 mode 分派；响应结构与 Login 完全一致。
func (c *Client) PasswordLogin(username, password, dvh, devName, devOS string) (*loginResp, error) {
	raw, err := c.post("/api/auth/login", map[string]any{
		"mode":     "password",
		"username": username,
		"password": password,
		"product":  AudDuihaoDesktop,
		"device":   map[string]string{"hash": dvh, "name": devName, "os": devOS},
	})
	if err != nil {
		return nil, err
	}
	return parseLoginResp(raw)
}

func parseLoginResp(raw []byte) (*loginResp, error) {
	var lr loginResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, errors.New("登录响应解析失败")
	}
	if lr.Token == "" {
		return nil, errors.New("登录响应缺少令牌")
	}
	return &lr, nil
}

// Login：POST /api/auth/login {mobile, code, product, device:{hash,name,os}}。
func (c *Client) Login(mobile, code, dvh, devName, devOS string) (*loginResp, error) {
	raw, err := c.post("/api/auth/login", map[string]any{
		"mobile":  mobile,
		"code":    code,
		"product": AudDuihaoDesktop,
		"device": map[string]string{"hash": dvh, "name": devName, "os": devOS},
	})
	if err != nil {
		return nil, err
	}
	return parseLoginResp(raw)
}

// ─────────────────────────────────────────────────────────────
//  微信扫码登录（/api/auth/wechat/*，公开面）
//  与官网 /wxlogin 同一取码双轨（自有公众号 / 上游中转）；
//  差别是桌面没有 cookie 会话，scene 换成 scene+一次性 key 对。
// ─────────────────────────────────────────────────────────────

type wechatQRResp struct {
	QRURL string `json:"qr_url"` // 微信 showqrcode 或中转图 URL（交给 FetchQRData 转 data URL）
	Scene string `json:"scene"`
	Key   string `json:"key"` // 一次性轮询密钥：poll 必须成对持有
	TTL   int    `json:"ttl"` // 二维码有效期（秒）
}

// WechatQrcode：POST /api/auth/wechat/qrcode —— 取桌面扫码二维码。
func (c *Client) WechatQrcode() (*wechatQRResp, error) {
	raw, err := c.post("/api/auth/wechat/qrcode", map[string]any{})
	if err != nil {
		return nil, err
	}
	var wr wechatQRResp
	if err := json.Unmarshal(raw, &wr); err != nil {
		return nil, errors.New("二维码响应解析失败")
	}
	if wr.QRURL == "" || wr.Scene == "" || wr.Key == "" {
		return nil, errors.New("二维码响应缺少数据")
	}
	return &wr, nil
}

// WechatPoll：POST /api/auth/wechat/poll。
// waiting=true 表示用户尚未扫码（客户端 1.5s 后继续轮询）；
// 命中时服务端已按登录同一条链签好令牌，响应结构与 Login 完全一致。
func (c *Client) WechatPoll(scene, key, dvh, devName, devOS string) (*loginResp, bool, error) {
	raw, err := c.post("/api/auth/wechat/poll", map[string]any{
		"scene":   scene,
		"key":     key,
		"product": AudDuihaoDesktop,
		"device":  map[string]string{"hash": dvh, "name": devName, "os": devOS},
	})
	if err != nil {
		return nil, false, err
	}
	var lr loginResp
	_ = json.Unmarshal(raw, &lr)
	if lr.Token != "" {
		return &lr, false, nil
	}
	var pr struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &pr)
	if pr.Status == "waiting" {
		return nil, true, nil
	}
	return nil, false, errors.New("扫码结果异常，请刷新二维码")
}

// ─────────────────────────────────────────────────────────────
//  授权态请求 helper（Bearer 令牌）+ 订单 API（/api/order*，会员中心）
// ─────────────────────────────────────────────────────────────

func (c *Client) postAuth(path, token string, in any) (json.RawMessage, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req)
}

func (c *Client) getAuth(path, token string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req)
}

// OrderSku 是可售规格（会员中心套餐卡）。price 单位=分，与服务端一致。
type OrderSku struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Price      int    `json:"price"`
	PriceText  string `json:"price_text"`
	PeriodText string `json:"period_text"`
	Note       string `json:"note"`
}

type ProductsResp struct {
	Product string `json:"product"`
	Goods   struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
		// 会员类长文档（服务端已净化）：会员中心左栏特权取 Benefits（空则回落
		// Summary），用户协议弹窗取 Agreement（空则用客户端内置占位条款）。
		Benefits  string `json:"benefits"`
		Agreement string `json:"agreement"`
	} `json:"goods"`
	Skus []OrderSku `json:"skus"`
}

// Products：GET /api/order/product —— 当前产品的可售规格。
// token 为空走匿名 GET（服务端公开面，按默认产品出）；
// 带令牌则按令牌里的产品出。
func (c *Client) Products(token string) (*ProductsResp, error) {
	var (
		raw json.RawMessage
		err error
	)
	if token == "" {
		raw, err = c.get("/api/order/product")
	} else {
		raw, err = c.getAuth("/api/order/product", token)
	}
	if err != nil {
		return nil, err
	}
	var pr ProductsResp
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, errors.New("套餐解析失败")
	}
	return &pr, nil
}

type OrderPayInfo struct {
	OK     bool   `json:"ok"`
	Type   string `json:"type"` // qrcode | redirect | manual | none
	QR     string `json:"qr"`   // 二维码图 URL（type=qrcode）
	PayURL string `json:"pay_url"`
	Msg    string `json:"msg"`
}

type OrderCreateResp struct {
	OrderNo  string       `json:"order_no"`
	Amount   int          `json:"amount"`
	Status   string       `json:"status"`
	ExpireAt int64        `json:"expire_at"`
	Pay      OrderPayInfo `json:"pay"`
}

// OrderCreate：POST /api/order/create {sku_code, qty} —— 下单 + 发起支付。
// ★ 价格服务端重算，客户端传什么都不参与计价。
func (c *Client) OrderCreate(token, skuCode string, qty int) (*OrderCreateResp, error) {
	raw, err := c.postAuth("/api/order/create", token, map[string]any{
		"sku_code": skuCode,
		"qty":      qty,
	})
	if err != nil {
		return nil, err
	}
	var or OrderCreateResp
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, errors.New("下单响应解析失败")
	}
	if or.OrderNo == "" {
		return nil, errors.New("下单响应缺少订单号")
	}
	return &or, nil
}

type OrderEntitlement struct {
	Product  string `json:"product"`
	Plan     string `json:"plan"`
	PlanText string `json:"plan_text"`
	Ent      int64  `json:"ent"`
	EntText  string `json:"ent_text"`
	Seats    int    `json:"seats"`
}

type OrderStatusResp struct {
	OrderNo     string            `json:"order_no"`
	Status      string            `json:"status"`
	StatusLabel string            `json:"status_label"`
	Paid        bool              `json:"paid"`
	Amount      int               `json:"amount"`
	HasLicense  bool              `json:"has_license"`
	Msg         string            `json:"msg"`
	Entitlement *OrderEntitlement `json:"entitlement"`
}

// OrderStatus：GET /api/order/status?order_no= —— 查单；服务端在 pending 时
// 会主动向网关核对（payment_query），入账唯一可信来源在服务端，客户端只轮询。
func (c *Client) OrderStatus(token, orderNo string) (*OrderStatusResp, error) {
	raw, err := c.getAuth("/api/order/status?order_no="+urlQueryEscape(orderNo), token)
	if err != nil {
		return nil, err
	}
	var sr OrderStatusResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, errors.New("查单响应解析失败")
	}
	return &sr, nil
}

func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[ch>>4])
			b.WriteByte(hex[ch&0x0f])
		}
	}
	return b.String()
}

type refreshResp struct {
	Token       string      `json:"token"`
	TokenTTL    int         `json:"token_ttl"`
	Entitlement Entitlement `json:"entitlement"`
}

// Refresh：POST /api/auth/refresh {token}。
func (c *Client) Refresh(token string) (*refreshResp, error) {
	raw, err := c.post("/api/auth/refresh", map[string]string{"token": token})
	if err != nil {
		return nil, err
	}
	var rr refreshResp
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, errors.New("续签响应解析失败")
	}
	if rr.Token == "" {
		return nil, errors.New("续签响应缺少令牌")
	}
	return &rr, nil
}

// Keys：GET /api/auth/keys → 公钥清单。
func (c *Client) Keys() ([]pubKeyEntry, error) {
	raw, err := c.get("/api/auth/keys")
	if err != nil {
		return nil, err
	}
	var kr struct {
		Keys []pubKeyEntry `json:"keys"`
	}
	if err := json.Unmarshal(raw, &kr); err != nil {
		return nil, errors.New("公钥清单解析失败")
	}
	if len(kr.Keys) == 0 {
		return nil, errors.New("服务端没有可用公钥")
	}
	return kr.Keys, nil
}
