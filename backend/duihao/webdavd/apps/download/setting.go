package download

import (
	"encoding/json"
	"strings"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// Setting 账号级下载设置（一个账号一份，不按任务配）。
//
// 为什么是全局而不是逐条：限速与并发是**整机带宽**的概念，逐条配等于每加
// 一个任务都要想起「我上次限了多少」，实际没人这么用。跟语音的播报引擎同理。
type Setting struct {
	MaxConc   int    `json:"maxConc"`   // 同时下载数 1~10（默认 3）
	DownLimit int    `json:"downLimit"` // 全局限速 KB/s，0 = 不限
	UpLimit   int    `json:"upLimit"`   // 上传限速 KB/s，0 = 不限
	BtNet     bool   `json:"btNet"`     // BT 网络（DHT + 监听端口）；关掉则磁力基本不可用
	Seed      bool   `json:"seed"`      // 下完继续做种；默认 false（下完即停）
	AutoDir   string `json:"autoDir"`   // 新建任务默认落点：download | video | music
}

// defaultSetting 默认值：3 路并发、不限速、开 BT 网络、不做种、落 _appfiles/download。
func defaultSetting() Setting {
	return Setting{MaxConc: 3, BtNet: true, AutoDir: "download"}
}

func normSetting(s Setting) Setting {
	if s.MaxConc < 1 || s.MaxConc > 10 {
		s.MaxConc = 3
	}
	if s.DownLimit < 0 {
		s.DownLimit = 0
	}
	if s.UpLimit < 0 {
		s.UpLimit = 0
	}
	if s.AutoDir != "video" && s.AutoDir != "music" {
		s.AutoDir = "download"
	}
	return s
}

// loadSetting 读账号设置；没有记录或解析失败都回默认值（坏数据不该挡住功能）。
func loadSetting(c appkit.Context) Setting {
	s := defaultSetting()
	if c.DB == nil {
		return s
	}
	var raw string
	err := c.DB.QueryRow(`select v from download_settings where uid=? and k='setting'`, c.User).Scan(&raw)
	if err != nil || strings.TrimSpace(raw) == "" {
		return s
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return defaultSetting()
	}
	return normSetting(s)
}

func saveSetting(c appkit.Context, s Setting) error {
	s = normSetting(s)
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = c.DB.Exec(`insert into download_settings(uid,k,v) values(?,?,?)
		on conflict(uid,k) do update set v=excluded.v`, c.User, "setting", string(raw))
	return err
}
