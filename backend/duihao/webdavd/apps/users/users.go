// Package users NAS 内部多用户的账号管理（迁移 019）。
//
// 口径：
//   - 页面入口由 appkit 按权限自动拦（users 不在 perms 里，member 进不来）；
//     API 放行进本应用后自检：list/create/update/delete 仅 admin，
//     passwd 管理员可重置任何人、成员只能改自己（且要验旧密码）；
//   - uid = 用户名（见 019 注释）；改名走 rename（同一事务迁账号行与全部
//     带 uid 列的业务表，第一个 admin 也能改）；
//   - 管理员密码改密时同步 hub_secrets（连接管理页「查看密码」永远有效）。
package users

import (
	"net/http"
	"strings"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/hubusers"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "users" }
func (a *App) Title() string { return "用户" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能管理用户")
	}
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	if c.DB == nil {
		return appkit.Errorf(http.StatusServiceUnavailable, "个人空间库没就绪，先启动网盘服务")
	}
	switch action {
	case "list":
		return a.list(c, w)
	case "create":
		return a.create(c, w, r)
	case "update":
		return a.update(c, w, r)
	case "rename":
		return a.rename(c, w, r)
	case "passwd":
		return a.passwd(c, w, r)
	case "delete":
		return a.del(c, w, r)
	default:
		return appkit.Errorf(http.StatusNotFound, "未知动作：%s", action)
	}
}

// view 给前端的账号行（PassHash 永不出库）。
type view struct {
	UID       string            `json:"uid"`
	Role      string            `json:"role"`
	Perms     map[string]string `json:"perms"`
	Enabled   bool              `json:"enabled"`
	CreatedAt string            `json:"createdAt"`
	Self      bool              `json:"self"`
}

func toView(u hubusers.User, selfUID string) view {
	return view{UID: u.UID, Role: u.Role, Perms: u.Perms, Enabled: u.Enabled,
		CreatedAt: u.CreatedAt, Self: u.UID == selfUID}
}

func (a *App) list(c appkit.Context, w http.ResponseWriter) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能查看用户列表")
	}
	us, err := hubusers.List(c.DB)
	if err != nil {
		return err
	}
	views := make([]view, 0, len(us))
	for _, u := range us {
		views = append(views, toView(u, c.User))
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"users": views, "modules": hubusers.Modules, "me": c.User,
	})
	return nil
}

func (a *App) create(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能创建用户")
	}
	var in struct {
		Name     string            `json:"name"`
		Password string            `json:"password"`
		Role     string            `json:"role"`
		Perms    map[string]string `json:"perms"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	if err := hubusers.Create(c.DB, in.Name, in.Password, in.Role, in.Perms); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (a *App) update(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能修改用户")
	}
	var in struct {
		UID     string            `json:"uid"`
		Role    string            `json:"role"`
		Perms   map[string]string `json:"perms"`
		Enabled bool              `json:"enabled"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	if in.UID == c.User && (in.Role != hubusers.RoleAdmin || !in.Enabled) {
		return appkit.Errorf(http.StatusBadRequest, "不能降级或停用自己")
	}
	if err := hubusers.UpdatePerm(c.DB, in.UID, in.Role, in.Perms, in.Enabled); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	// 防呆：最后一个可用管理员不能被改没。
	if in.Role != hubusers.RoleAdmin || !in.Enabled {
		if n, _ := hubusers.CountAdmins(c.DB); n == 0 {
			_ = hubusers.UpdatePerm(c.DB, in.UID, hubusers.RoleAdmin, in.Perms, true)
			return appkit.Errorf(http.StatusBadRequest, "至少要保留一个可用的管理员")
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// rename 改用户名（含第一个 admin 账号）。uid 是各业务表的账号键，
// hubusers.Rename 会在一个事务里把账号行与所有业务数据一起迁到新名。
// 改自己 = 旧会话失效，前端改完回登录页。
func (a *App) rename(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能修改用户")
	}
	var in struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	if err := hubusers.Rename(c.DB, in.UID, in.Name); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (a *App) passwd(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		UID      string `json:"uid"`
		Password string `json:"password"`
		Old      string `json:"old"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	in.UID = strings.TrimSpace(in.UID)
	if in.UID == "" {
		in.UID = c.User
	}
	secretKey := ""
	if c.IsAdmin() {
		// 管理员重置任何人；自己的密码进查看名单（连接管理页同步）。
		if in.UID == c.User {
			secretKey = hubusers.SecretAdminPass
		}
	} else {
		// 成员只能改自己，且必须验旧密码。
		if in.UID != c.User {
			return appkit.Errorf(http.StatusForbidden, "只能修改自己的密码")
		}
		old, err := hubusers.Verify(c.DB, c.User, in.Old)
		if err != nil || old == nil {
			return appkit.Errorf(http.StatusBadRequest, "旧密码不对")
		}
	}
	if err := hubusers.SetPassword(c.DB, in.UID, in.Password, secretKey); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (a *App) del(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if !c.IsAdmin() {
		return appkit.Errorf(http.StatusForbidden, "只有管理员能删除用户")
	}
	var in struct {
		UID string `json:"uid"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	in.UID = strings.TrimSpace(in.UID)
	if in.UID == "" || in.UID == c.User {
		return appkit.Errorf(http.StatusBadRequest, "不能删除自己")
	}
	if err := hubusers.Delete(c.DB, in.UID); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	_ = hubusers.DelSecret(c.DB, hubusers.SecretSharePass(in.UID))
	if n, _ := hubusers.CountAdmins(c.DB); n == 0 {
		_ = hubusers.DelSecret(c.DB, hubusers.SecretAdminPass)
		// 理论上 update 挡住了这条路，这里兜底记一笔。
		return appkit.Errorf(http.StatusInternalServerError, "管理员被删空，请重建账号")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
