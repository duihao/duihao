package health

import (
	"net/http"
	"strings"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// 隐私开关：存 hub_meta（uid + 'health.privacy'），不建新表。
// 开启后前端把概览/详情里的数值打码，点「眼睛」才临时显示 —— 只影响呈现，
// 不改数据、不改接口返回（自己看自己的数据，没必要服务端藏）。
const privacyKey = "health.privacy"

func readPrivacy(c appkit.Context) bool {
	var v string
	err := c.DB.QueryRow(`select val from hub_meta where uid=? and key=?`, c.User, privacyKey).Scan(&v)
	return err == nil && strings.TrimSpace(v) == "1"
}

func settingsGet(c appkit.Context, w http.ResponseWriter) error {
	appkit.JSON(w, http.StatusOK, map[string]any{"privacy": readPrivacy(c)})
	return nil
}

func settingsSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Privacy bool `json:"privacy"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	val := "0"
	if in.Privacy {
		val = "1"
	}
	if _, err := c.DB.Exec(`insert or replace into hub_meta(uid,key,val) values(?,?,?)`,
		c.User, privacyKey, val); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"privacy": in.Privacy})
	return nil
}
