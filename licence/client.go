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
	return &Client{
		Base: strings.TrimRight(base, "/"),
		HTTP: &http.Client{Timeout: 5 * time.Second},
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
	OK    bool          `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *apiErr       `json:"error"`
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
	return r.Data, nil
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
	} `json:"user"`
	Entitlement Entitlement `json:"entitlement"`
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
	var lr loginResp
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, errors.New("登录响应解析失败")
	}
	if lr.Token == "" {
		return nil, errors.New("登录响应缺少令牌")
	}
	return &lr, nil
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
