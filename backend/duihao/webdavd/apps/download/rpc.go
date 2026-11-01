package download

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// aria2 的 JSON-RPC 客户端。
//
// 为什么走 RPC 而不是解析命令行输出：`aria2c` 的进度行是给人看的（\r 刷新、
// 按秒变化、格式随版本微调），解析它等于把 aria2 的 UI 当成 API。开
// --enable-rpc 之后，进度、暂停、续传、限速、多任务全是结构化数据，而且
// 一个 aria2c 进程就能承载整个队列 —— 不用为每个任务起一个进程。
//
// 安全：RPC 只绑 127.0.0.1（--rpc-listen-all=false），每次启动随机 secret，
// 请求第一个参数固定是 "token:<secret>"。别人就算扫到端口也调不动。

// rpcError aria2 返回的 error 对象。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("aria2 错误 %d：%s", e.Code, e.Message)
}

type rpcClient struct {
	base   string
	secret string
	hc     *http.Client
}

func newRPC(port int, secret string) *rpcClient {
	return &rpcClient{
		base:   fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port),
		secret: secret,
		hc: &http.Client{
			Timeout: 10 * time.Second,
			// 本机回环也要绕过系统代理：某些代理软件会把 127.0.0.1 一起劫持，
			// 表现是「RPC 明明起来了却连不上」（Go 项目里踩过多次）。
			Transport: &http.Transport{Proxy: nil},
		},
	}
}

// call 调一个 aria2 方法，把 result 解到 out（out 为 nil 时忽略返回值）。
func (c *rpcClient) call(method string, out any, params ...any) error {
	all := make([]any, 0, len(params)+1)
	all = append(all, "token:"+c.secret)
	all = append(all, params...)

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "duihao", "method": method, "params": all,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.base, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("连不上下载引擎：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("下载引擎返回了看不懂的内容（HTTP %d）", resp.StatusCode)
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// ---------- 高层封装 ----------

// rpcStatus aria2.tellStatus 的返回。aria2 的数字一律是**字符串**，别当 number 解析。
type rpcStatus struct {
	GID             string `json:"gid"`
	Status          string `json:"status"` // active|waiting|paused|error|complete|removed
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	UploadSpeed     string `json:"uploadSpeed"`
	ErrorMessage    string `json:"errorMessage"`
	NumSeeders      string `json:"numSeeders"`
	Dir             string `json:"dir"`
	Files           []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// fileName 从 files[0].path 取文件名（BT 多文件时取种子名）。
func (s rpcStatus) fileName() string {
	if len(s.Files) == 0 {
		return ""
	}
	p := s.Files[0].Path
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (c *rpcClient) version() (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.call("aria2.getVersion", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// add 添加一个任务，返回 gid。opts 是 aria2 的任务级选项（dir / out 等）。
func (c *rpcClient) add(uri string, opts map[string]string) (string, error) {
	var gid string
	if err := c.call("aria2.addUri", &gid, []string{uri}, opts); err != nil {
		return "", err
	}
	return gid, nil
}

func (c *rpcClient) status(gid string) (rpcStatus, error) {
	var s rpcStatus
	err := c.call("aria2.tellStatus", &s, gid)
	return s, err
}

func (c *rpcClient) pause(gid string) error {
	var out string
	return c.call("aria2.forcePause", &out, gid)
}

func (c *rpcClient) unpause(gid string) error {
	var out string
	return c.call("aria2.unpause", &out, gid)
}

// remove 从队列里摘掉（不删文件）。已完成的要用 removeResult 清记录。
func (c *rpcClient) remove(gid string) error {
	var out string
	return c.call("aria2.forceRemove", &out, gid)
}

func (c *rpcClient) removeResult(gid string) error {
	return c.call("aria2.removeDownloadResult", nil, gid)
}

// globalStat 全局速度（页面顶部显示总速率）。
func (c *rpcClient) globalStat() (down, up int64, active, waiting int, err error) {
	var v struct {
		DownloadSpeed   string `json:"downloadSpeed"`
		UploadSpeed     string `json:"uploadSpeed"`
		NumActive       string `json:"numActive"`
		NumWaiting      string `json:"numWaiting"`
		NumStoppedTotal string `json:"numStoppedTotal"`
	}
	if err = c.call("aria2.getGlobalStat", &v); err != nil {
		return
	}
	down, _ = strconv.ParseInt(v.DownloadSpeed, 10, 64)
	up, _ = strconv.ParseInt(v.UploadSpeed, 10, 64)
	active, _ = strconv.Atoi(v.NumActive)
	waiting, _ = strconv.Atoi(v.NumWaiting)
	return
}

// applyLimits 动态改限速与并发（不用重启引擎）。0 表示不限。
func (c *rpcClient) applyLimits(downKB, upKB, maxConc int) error {
	opt := map[string]string{
		"max-overall-download-limit": fmt.Sprintf("%dK", downKB),
		"max-overall-upload-limit":   fmt.Sprintf("%dK", upKB),
	}
	if maxConc > 0 {
		opt["max-concurrent-downloads"] = strconv.Itoa(maxConc)
	}
	return c.call("aria2.changeGlobalOption", nil, opt)
}

// purgeStopped 清掉引擎里已结束的记录（对号侧自己存了，不必留两份）。
func (c *rpcClient) purgeStopped() {
	_ = c.call("aria2.purgeDownloadResult", nil)
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
