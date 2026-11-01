package remote

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ddnsUpdate 把 AAAA 记录指到 ip。返回 (人话结果, err)。
// err != nil = 失败；msg 总是给人看的描述（成功也带一句）。
//
// 两个服务商共用「查记录 → 有就改 / 没就建」的最小闭环：
// 不做 TTL/线路/多记录这些进阶参数——远程访问一条记录够用，界面也只暴露这些。
func ddnsUpdate(provider, domain, sub, token, ip string) (string, error) {
	switch provider {
	case "aliyun":
		return aliyunUpdate(domain, sub, token, ip)
	case "dnspod", "":
		return dnspodUpdate(domain, sub, token, ip)
	default:
		return "", fmt.Errorf("不支持的服务商：%s", provider)
	}
}

var dnsHTTPClient = &http.Client{Timeout: 15 * time.Second}

// API 地址做成变量：测试里换成 httptest 假服务，不用真连服务商。
var (
	dnspodAPI = "https://dnsapi.cn"
	aliyunAPI = "https://alidns.aliyuncs.com"
)

// postForm 带 User-Agent 的表单 POST（DNSPod 明确要求 UA，缺了会被拒）。
func postForm(api string, form url.Values) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Duihao/1.0 (remote-access)")
	resp, err := dnsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------- 腾讯 DNSPod ----------
//
// token 格式「id,token」（控制台生成的 API Token 两段）。DNS 有独立 token 体系，
// 不是账号密码，泄露了也只动 DNS——但照样进凭证保险箱。

type dnspodResp struct {
	Status struct {
		Code     string `json:"statuscode"`
		Message  string `json:"message"`
		RecordID int64  `json:"record_id"`
	} `json:"status"`
}

func dnspodUpdate(domain, sub, token, ip string) (string, error) {
	if !strings.Contains(token, ",") {
		return "", errors.New("DNSPod 令牌格式是「ID,Token」两段（控制台 → API 密钥里生成）")
	}
	base := url.Values{
		"login_token": {token},
		"format":      {"json"},
		"domain":      {domain},
		"sub_domain":  {subOrAt(sub)},
		"record_line": {"默认"},
	}

	// 1) 查现有 AAAA 记录
	ls := cloneValues(base)
	ls.Set("record_type", "AAAA")
	body, err := postForm(dnspodAPI+"/Record.List", ls)
	if err != nil {
		return "连不上 DNSPod：" + err.Error(), err
	}
	var list struct {
		Status struct {
			Code    string `json:"statuscode"`
			Message string `json:"message"`
		} `json:"status"`
		Records []struct {
			ID     int64  `json:"id"`
			Value  string `json:"value"`
			Type   string `json:"type"`
			Line   string `json:"line"`
			LineID string `json:"line_id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "DNSPod 返回看不懂", err
	}
	if list.Status.Code != "1" {
		return "DNSPod：" + list.Status.Message, errors.New(list.Status.Message)
	}

	var recordID int64
	var line string
	for _, rec := range list.Records {
		if rec.Type == "AAAA" {
			recordID, line = rec.ID, rec.Line
			break
		}
	}
	if line == "" {
		line = "默认"
	}

	// 2) 有就改，没有就建
	up := cloneValues(base)
	up.Set("record_type", "AAAA")
	up.Set("value", ip)
	var api string
	if recordID > 0 {
		up.Set("record_id", fmt.Sprint(recordID))
		api = dnspodAPI + "/Record.Modify"
	} else {
		api = dnspodAPI + "/Record.Create"
	}
	body, err = postForm(api, up)
	if err != nil {
		return "连不上 DNSPod：" + err.Error(), err
	}
	var st dnspodResp
	if err := json.Unmarshal(body, &st); err != nil {
		return "DNSPod 返回看不懂", err
	}
	if st.Status.Code != "1" {
		return "DNSPod：" + st.Status.Message, errors.New(st.Status.Message)
	}
	if recordID > 0 {
		return "AAAA 记录已更新 → " + ip, nil
	}
	return "AAAA 记录已创建 → " + ip, nil
}

func subOrAt(sub string) string {
	if sub == "" {
		return "@"
	}
	return sub
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		for _, s := range vs {
			out.Add(k, s)
		}
	}
	return out
}

// ---------- 阿里云解析 ----------
//
// token 格式「AccessKeyID:AccessKeySecret」。阿里 RPC 签名（HMAC-SHA1 + 特殊
// URL 编码）手写，不引 SDK——签名就 30 行，SDK 一大坨。
// 文档：https://help.aliyun.com/document_detail/29776.html

func aliyunUpdate(domain, sub, token, ip string) (string, error) {
	id, secret, ok := strings.Cut(token, ":")
	if !ok || id == "" || secret == "" {
		return "", errors.New("阿里云令牌格式是「AccessKeyID:AccessKeySecret」两段（RAM 访问控制台里生成）")
	}

	call := func(action string, params map[string]string) (map[string]any, error) {
		vals := url.Values{
			"Format":           {"JSON"},
			"Version":          {"2015-01-09"},
			"AccessKeyId":      {id},
			"SignatureMethod":  {"HMAC-SHA1"},
			"SignatureVersion": {"1.0"},
			"SignatureNonce":   {fmt.Sprint(time.Now().UnixNano())},
			"Timestamp":        {time.Now().UTC().Format("2006-01-02T15:04:05Z")},
			"Action":           {action},
		}
		for k, v := range params {
			vals.Set(k, v)
		}
		sign, err := aliyunSign("GET", vals, secret)
		if err != nil {
			return nil, err
		}
		vals.Set("Signature", sign)
		resp, err := dnsHTTPClient.Get(aliyunAPI + "/?" + vals.Encode())
		if err != nil {
			return nil, fmt.Errorf("连不上阿里云：%w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("阿里云返回看不懂: %s", truncate(string(body), 200))
		}
		return out, nil
	}

	// 查现有 AAAA
	list, err := call("DescribeDomainRecords", map[string]string{
		"DomainName": domain, "Type": "AAAA", "RRKeyWord": subOrAt(sub),
	})
	if err != nil {
		return err.Error(), err
	}
	if code, _ := list["Code"].(string); code != "" {
		msg := "阿里云：" + code
		if m, _ := list["Message"].(string); m != "" {
			msg += " " + m
		}
		return msg, errors.New(code)
	}

	recordID := ""
	var line string
	if records, _ := list["DomainRecords"].(map[string]any)["Record"].([]any); err == nil {
		for _, it := range records {
			rec, _ := it.(map[string]any)
			if rec == nil {
				continue
			}
			if rec["RR"] == subOrAt(sub) {
				recordID, _ = rec["RecordId"].(string)
				line, _ = rec["Line"].(string)
				break
			}
		}
	}

	params := map[string]string{"RR": subOrAt(sub), "Type": "AAAA", "Value": ip}
	var action string
	if recordID != "" {
		params["RecordId"] = recordID
		action = "UpdateDomainRecord"
		if line != "" && line != "default" {
			params["Line"] = line
		}
	} else {
		params["DomainName"] = domain
		action = "AddDomainRecord"
	}
	out, err := call(action, params)
	if err != nil {
		return err.Error(), err
	}
	if code, _ := out["Code"].(string); code != "" {
		msg := "阿里云：" + code
		if m, _ := out["Message"].(string); m != "" {
			msg += " " + m
		}
		return msg, errors.New(code)
	}
	if recordID != "" {
		return "AAAA 记录已更新 → " + ip, nil
	}
	return "AAAA 记录已创建 → " + ip, nil
}

// aliyunSign 阿里云 RPC 签名：参数按 key 排序拼接 → 特殊 URL 编码 →
// "GET&%2F&" + 编码后参数 做 HMAC-SHA1。
func aliyunSign(method string, vals url.Values, secret string) (string, error) {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, aliyunEncode(k)+"="+aliyunEncode(vals.Get(k)))
	}
	canonical := strings.Join(pairs, "&")
	strToSign := method + "&" + aliyunEncode("/") + "&" + aliyunEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(strToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// aliyunEncode 阿里云要求的特殊编码：除了 A-Za-z0-9-_.~ 全部 %XY 大写十六进制
// （url.QueryEscape 会把空格变 + 、把 ~ 原样保留，都要矫正）。
func aliyunEncode(s string) string {
	hexed := hex.EncodeToString([]byte(s))
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(hexed[i*2 : i*2+2]))
		}
	}
	return b.String()
}
