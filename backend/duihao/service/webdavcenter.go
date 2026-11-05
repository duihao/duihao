// WebDAV 共享管理服务（M5）：多共享增删改 + 连接信息 + 从站点目录选根目录。
//
// 与 FTPService 同构：共享表是唯一真相（instances/<id>/webdavshares.json），
// 改动后重启实例让内置服务重新加载（WebDAV 长连接为主，重启前提示前端）。
package service

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/instance"
	"github.com/duihao/duihao/backend/duihao/processd"
	"github.com/duihao/duihao/backend/duihao/site"
	"github.com/duihao/duihao/backend/duihao/webdavd"
	"github.com/duihao/duihao/backend/duihao/webdavd/credentials"
	"github.com/duihao/duihao/backend/duihao/webdavd/hubusers"
	"github.com/duihao/duihao/backend/duihao/webdavd/store"
	"github.com/duihao/duihao/backend/duihao/webdavd/sysinfo"
)

// WebDavService 共享管理。
type WebDavService struct {
	mgr   *instance.Manager
	sites *site.Manager // 可选：「从站点选择根目录」用
	audit *audit.Logger
	emit  func(name string, data any) // 可选：广播给前端（改端口后首屏地址跟着变）
}

// NewWebDavService 构建服务。
func NewWebDavService(mgr *instance.Manager, a *audit.Logger) *WebDavService {
	return &WebDavService{mgr: mgr, audit: a}
}

// SetSiteManager 注入站点管理器（可选能力）。
func (s *WebDavService) SetSiteManager(sites *site.Manager) { s.sites = sites }

// SetEventEmitter 注入事件广播（与 DeployService 同款）。没有它服务照常工作，
// 只是「改端口后首屏地址自动刷新」这类联动失效。
func (s *WebDavService) SetEventEmitter(fn func(name string, data any)) { s.emit = fn }

// WebDavShareView 共享列表一行（不含密码与哈希）。
type WebDavShareView struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Name      string `json:"name,omitempty"`
	Root      string `json:"root"`
	ReadOnly  bool   `json:"readonly"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// WebDavConnInfo 连接信息卡（前端展示「往哪挂载」）。
type WebDavConnInfo struct {
	Scheme  string `json:"scheme"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	URL     string `json:"url"`     // http://127.0.0.1:9111/ 或 https://局域网IP:9111/
	TLS     bool   `json:"tls"`
	Running bool   `json:"running"`
	// Addrs 是首屏「访问地址」下拉的候选：本机 / 局域网 / IPv6。
	// 顺序固定 local → lan → v6，前端按下标记住上次选择。
	Addrs []WebDavAddrView `json:"addrs"`
}

// WebDavAddrView 一个访问地址。OK=false 表示在当前监听设置下外面连不上
// （如 bind=127.0.0.1 时局域网/IPv6 地址无意义），前端置灰并显示 Note。
type WebDavAddrView struct {
	// ID 下拉里唯一：local / lan / v6-0 / v6-1…
	// 本机常有多条公网 IPv6（宽带动态段、临时隐私地址、二级路由下发的段），
	// 全都要能选，光靠 Kind 区分不出来，所以另给一个稳定 id 供前端记住选择。
	ID    string `json:"id"`
	Kind  string `json:"kind"`  // local | lan | v6
	Label string `json:"label"` // 本机 / 局域网 / IPv6
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Note  string `json:"note,omitempty"`
	// Fix 非空表示这一项「改一下设置就能用」，值是需要的修法：
	// lan = 把监听地址改成 0.0.0.0 并开自签 HTTPS（首屏一键按钮用）。
	// 空 = 环境问题（没有局域网 IP / 没有公网 IPv6），点也没用。
	Fix string `json:"fix,omitempty"`
}

// SiteRootView 复用 ftpcenter.go 里的定义（同包）。

func (s *WebDavService) dir(instanceID string) (string, error) {
	if s.mgr == nil {
		return "", fmt.Errorf("网盘管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return "", fmt.Errorf("实例不存在: %s", instanceID)
	}
	if rec.Plugin != "webdav" {
		return "", fmt.Errorf("实例 %s 不是 WebDAV 网盘（%s）", instanceID, rec.Plugin)
	}
	return s.mgr.InstanceDirPath(instanceID), nil
}

// ListWebDavShares 共享列表。表为空时返回设置默认提示行（同 FTP 逻辑）。
func (s *WebDavService) ListWebDavShares(instanceID string) ([]WebDavShareView, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return nil, err
	}
	out := make([]WebDavShareView, 0, len(shares))
	for _, sh := range shares {
		out = append(out, WebDavShareView{
			ID: sh.ID, User: sh.User, Name: sh.Name, Root: sh.Root,
			ReadOnly: sh.ReadOnly, Enabled: sh.Enabled, Note: sh.Note, CreatedAt: sh.CreatedAt,
		})
	}
	return out, nil
}

// CreateWebDavShare 新增共享。
func (s *WebDavService) CreateWebDavShare(instanceID, user, password, root string, readonly bool, name, note string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	sh, err := webdavd.NewShare(user, password, root, readonly, name, note)
	if err != nil {
		return err
	}
	for _, ex := range shares {
		if strings.EqualFold(ex.User, sh.User) {
			return fmt.Errorf("用户名 %s 已存在", sh.User)
		}
	}
	shares = append(shares, sh)
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.create-share", instanceID, map[string]any{"user": sh.User, "root": sh.Root, "readonly": readonly})
	return s.restartIfRunning(instanceID)
}

// UpdateWebDavShare 改共享基本信息（不改密码）。
func (s *WebDavService) UpdateWebDavShare(instanceID, shareID, user, root string, readonly, enabled bool, name, note string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	idx := -1
	for i := range shares {
		if shares[i].ID == shareID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	user = strings.TrimSpace(user)
	for i := range shares {
		if i != idx && strings.EqualFold(shares[i].User, user) {
			return fmt.Errorf("用户名 %s 已被其它共享使用", user)
		}
	}
	shares[idx].User = user
	shares[idx].Root = strings.TrimSpace(root)
	shares[idx].Name = strings.TrimSpace(name)
	shares[idx].Note = strings.TrimSpace(note)
	shares[idx].ReadOnly = readonly
	shares[idx].Enabled = enabled
	if err := shares[idx].Validate(); err != nil {
		return err
	}
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.update-share", instanceID, map[string]any{
		"user": shares[idx].User, "root": shares[idx].Root,
		"readonly": readonly, "enabled": enabled,
	})
	return s.restartIfRunning(instanceID)
}

// SetWebDavSharePassword 改密码。
func (s *WebDavService) SetWebDavSharePassword(instanceID, shareID, password string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("密码不能为空")
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	ok := false
	shareUser := ""
	for i := range shares {
		if shares[i].ID == shareID {
			hash, herr := webdavd.HashPassword(password)
			if herr != nil {
				return herr
			}
			shares[i].PassHash = hash
			ok = true
			shareUser = shares[i].User
			break
		}
	}
	if !ok {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	// 自动创建的共享密码密文同步更新，连接管理页「查看密码」不失真。
	if st, oerr := s.openHubDB(instanceID); oerr == nil {
		if hubusers.GetSecret(st.DB(), hubusers.SecretSharePass(shareUser)) != "" {
			_ = hubusers.PutSecret(st.DB(), hubusers.SecretSharePass(shareUser), mustSeal(password))
		}
		_ = st.Close()
	}
	s.audit.Ok("ui", "webdav.set-password", instanceID, map[string]any{"id": shareID})
	return s.restartIfRunning(instanceID)
}

// DeleteWebDavShare 删共享。
func (s *WebDavService) DeleteWebDavShare(instanceID, shareID string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	// 第一个创建的共享账号不许删（它是「默认账号」：默认共享地址、扫码
	// 快传、引导 admin 的 uid 兜底都指望它）。要收权可以停用或改只读。
	// CreatedAt 缺失（老数据）视为最早，按列表序兜底。
	firstIdx := 0
	for i := 1; i < len(shares); i++ {
		if earlierCreatedAt(shares[i].CreatedAt, shares[firstIdx].CreatedAt) {
			firstIdx = i
		}
	}
	if shares[firstIdx].ID == shareID {
		return fmt.Errorf("第一个创建的账号不允许删除；如需收权可停用或改为只读")
	}
	out := make([]webdavd.Share, 0, len(shares))
	found := false
	for _, sh := range shares {
		if sh.ID == shareID {
			found = true
			continue
		}
		out = append(out, sh)
	}
	if !found {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	if err := webdavd.SaveShares(dir, out); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.delete-share", instanceID, map[string]any{"id": shareID})
	return s.restartIfRunning(instanceID)
}

// GetWebDavConnInfo 连接信息（URL 拼装给前端展示/复制）。
func (s *WebDavService) GetWebDavConnInfo(instanceID string) (*WebDavConnInfo, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("网盘管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	// 首屏每次刷新都兜一下：一个共享都没有就自动建「duihao」默认共享，
	// 普通模式开箱即用（幂等，已有共享时零开销）。
	if _, err := s.EnsureDefaultShare(instanceID); err != nil {
		return nil, err
	}
	tls := false
	plain := true
	bind := "127.0.0.1"
	if values, err := s.mgr.EffectiveSettings(instanceID); err == nil {
		tls = values["dav.tls"] == "1"
		// dav.plain 缺省视为允许（M58 口径：局域网/公网都直接 http）
		plain = values["dav.plain"] != "0"
		if v := strings.TrimSpace(values["dav.bind"]); v != "" {
			bind = v
		}
	}
	// 展示口径：允许明文时一律给 http 地址（挂载端最省事，同端口仍能走 https）；
	// 只有「强制加密」（plain=0）才把地址写成 https。
	scheme := "http"
	if tls && !plain {
		scheme = "https"
	}
	// 「对外可听」才谈得上局域网/IPv6 地址；只听 127.0.0.1 时那两条只是噪音，
	// 但仍然列出来置灰，用户才知道该去设置里改什么。
	publicBind := bind != "127.0.0.1" && bind != "localhost"
	host := "127.0.0.1"
	if publicBind {
		if ip := firstLANIPv4(); ip != "" {
			host = ip
		} else {
			host = "本机IP"
		}
	}
	port := rec.Ports["dav"]
	running := s.mgr.Status(instanceID) == processd.StateRunning
	addrs := buildDavAddrs(scheme, port, bind, firstLANIPv4(), sysinfo.GlobalIPv6All())

	return &WebDavConnInfo{
		Scheme: scheme, Host: host, Port: port,
		URL: fmt.Sprintf("%s://%s:%d/", scheme, host, port),
		TLS: tls, Running: running, Addrs: addrs,
	}, nil
}

// buildDavAddrs 拼首屏「访问地址」下拉：本机 / 局域网 / 每条公网 IPv6 一项。
//
// 纯函数，便于单测：只有 bind 不是回环时局域网与 IPv6 才真正可达，否则那些项
// 置灰并写明原因 —— 让用户知道该去设置里把监听地址改成 0.0.0.0。
//
// ip6s 传**全部** GUA（sysinfo.GlobalIPv6All）：一台机器同时挂着宽带动态段、
// Windows 临时隐私地址、二级路由下发的段是常态，只显示第一条会让用户拿错地址去
// 手机验证。
func buildDavAddrs(scheme string, port int, bind, lanIP string, ip6s []string) []WebDavAddrView {
	publicBind := bind != "127.0.0.1" && bind != "localhost"
	// 明文 + 对外监听 = Basic Auth 密码可被同链路嗅探，提示一句但不拦
	// （M58 用户定稿：局域网与公网都直接 http，https 作为可选能力保留）。
	plainNote := ""
	if scheme != "https" {
		plainNote = "明文 HTTP 直连，密码可被同链路嗅探"
	}
	noteOf := func() string {
		if !publicBind {
			return "仅本机可访问"
		}
		return plainNote
	}
	fixOf := func() string {
		if !publicBind {
			return "lan"
		}
		return ""
	}

	out := []WebDavAddrView{{
		ID:   "local",
		Kind: "local", Label: "本机", OK: true,
		// 本机恒 http：回环没有窃听面，TLS 开着也走明文（嗅探监听器
		// 对回环恒放行明文），免掉自签证书的浏览器警告。
		URL: fmt.Sprintf("http://127.0.0.1:%d/", port),
	}}

	lan := WebDavAddrView{ID: "lan", Kind: "lan", Label: "局域网", OK: publicBind}
	if lanIP != "" {
		lan.URL = fmt.Sprintf("%s://%s:%d/", scheme, lanIP, port)
	} else {
		lan.URL = "—"
		lan.OK = false
		lan.Note = "没有找到局域网 IPv4"
	}
	if lan.Note == "" {
		lan.Note = noteOf()
		lan.Fix = fixOf()
	}
	out = append(out, lan)

	// 一条 IPv6 都没有时留一条占位，说明为什么没有（而不是整个下拉少一节）。
	if len(ip6s) == 0 {
		out = append(out, WebDavAddrView{
			ID: "v6-0", Kind: "v6", Label: "IPv6", URL: "—",
			Note: "没有公网 IPv6（光猫/路由要开 IPv6）",
		})
		return out
	}
	for i, ip6 := range ip6s {
		v6 := WebDavAddrView{
			ID:   fmt.Sprintf("v6-%d", i),
			Kind: "v6", Label: "IPv6", OK: publicBind,
			URL: fmt.Sprintf("%s://[%s]:%d/", scheme, ip6, port),
		}
		v6.Note = noteOf()
		v6.Fix = fixOf()
		out = append(out, v6)
	}
	return out
}

// EnableLanAccess 一键开启局域网访问：监听地址改 0.0.0.0 + 开自签 HTTPS。
//
// 三个键一起写：bind=0.0.0.0（对外监听）、tls=1（同时提供 https 能力）、
// plain=1（同端口也放行明文 http —— M58 用户定稿：局域网与公网都能直接 http，
// 挂载端不必处理自签证书）。只动这三个键，其它设置保持原样。
func (s *WebDavService) EnableLanAccess(instanceID string) (*WebDavConnInfo, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("网盘管理尚未就绪")
	}
	if rec := s.mgr.GetRecord(instanceID); rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if err := s.mgr.ApplySettingsDiff(instanceID, map[string]string{
		"dav.bind":  "0.0.0.0",
		"dav.tls":   "1",
		"dav.plain": "1",
	}); err != nil {
		return nil, err
	}
	s.audit.Ok("ui", "webdav.enable-lan", instanceID, nil)
	return s.GetWebDavConnInfo(instanceID)
}

// SetWebDavPort 改网盘监听端口（NAS配置窗「NAS 端口」一行用）。
//
// 走 ApplySettingsDiff → ApplySettings 的端口链路：dav.port 在清单里声明了
// portRole，因此会经过 SetPort 的占用校验、端口池另选与失败回滚，运行中还会
// 自动重启实例 —— 与「设置」对话框里改端口是同一条路，不另开一套。
func (s *WebDavService) SetWebDavPort(instanceID string, port int) (*WebDavConnInfo, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("网盘管理尚未就绪")
	}
	if rec := s.mgr.GetRecord(instanceID); rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法（1-65535）", port)
	}
	if err := s.mgr.ApplySettingsDiff(instanceID, map[string]string{
		"dav.port": strconv.Itoa(port),
	}); err != nil {
		return nil, err
	}
	s.audit.Ok("ui", "webdav.set-port", instanceID, map[string]any{"port": port})
	if s.emit != nil {
		// 首屏地址卡、扫码链接都从端口推导，改完必须让它们重取一次。
		s.emit("webdav:conn:changed", map[string]any{"instanceId": instanceID, "port": port})
	}
	return s.GetWebDavConnInfo(instanceID)
}

// ---------- NAS 内部多用户（迁移 019）：默认共享 / 管理员账号 / 桌面直登票 ----------

// openHubDB 打开（必要时创建）实例的 hub.db。webdavd 内置服务在跑时共享
// 同一文件（modernc sqlite + WAL 支持多连接），这里用完即关。
func (s *WebDavService) openHubDB(instanceID string) (*store.Store, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dir, "hub.db"))
}

// EnsureDefaultShare 一个共享都没有时自动建「admin」默认共享：普通模式
// 开箱即用（首屏地址、扫码快传都依赖它）。随机密码进 hub_secrets 密文，
// 连接管理页「NAS 账号」可查看。返回是否新建了共享。
func (s *WebDavService) EnsureDefaultShare(instanceID string) (bool, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return false, err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return false, err
	}
	if len(shares) > 0 {
		return false, nil
	}
	pass, err := hubusers.GenPassword()
	if err != nil {
		return false, err
	}
	sh, err := webdavd.NewShare("admin", pass, "{dir}/www", false, "默认共享", "自动创建：手机/电脑挂载用")
	if err != nil {
		return false, err
	}
	if err := webdavd.SaveShares(dir, []webdavd.Share{sh}); err != nil {
		return false, err
	}
	if st, err := store.Open(filepath.Join(dir, "hub.db")); err == nil {
		_ = hubusers.PutSecret(st.DB(), hubusers.SecretSharePass(sh.User),
			mustSeal(pass))
		_ = st.Close()
	}
	s.audit.Ok("ui", "webdav.default-share", instanceID, map[string]any{"user": sh.User})
	return true, nil
}

// HubAccountView 连接管理页「NAS 账号」区块的数据。
type HubAccountView struct {
	AdminUID  string `json:"adminUid"`
	AdminPass string `json:"adminPass"` // 明文（本机 DPAPI 密文解开；解不开为空）
	ShareUser string `json:"shareUser"` // 自动创建的默认共享（有密文才回）
	SharePass string `json:"sharePass"`
	Created   bool   `json:"created"` // 本次调用是否新建了管理员
}

// HubAccountInfo 管理员账号查看（顺带兜底 EnsureAdmin：服务没起过也能查）。
func (s *WebDavService) HubAccountInfo(instanceID string) (*HubAccountView, error) {
	if _, err := s.EnsureDefaultShare(instanceID); err != nil {
		return nil, err
	}
	st, err := s.openHubDB(instanceID)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	db := st.DB()
	uid, pass, created, err := hubusers.EnsureAdmin(db, "admin")
	if err != nil {
		return nil, err
	}
	out := &HubAccountView{Created: created}
	if uid != "" {
		out.AdminUID, out.AdminPass = uid, pass
	} else {
		us, lerr := hubusers.List(db)
		if lerr != nil {
			return nil, lerr
		}
		for _, u := range us {
			if u.Role == hubusers.RoleAdmin && u.Enabled {
				out.AdminUID = u.UID
				out.AdminPass = hubusers.OpenSecret(hubusers.GetSecret(db, hubusers.SecretAdminPass))
				break
			}
		}
	}
	// 默认共享密码：只回我们存过密文的（自动创建的那份），用户手建的不管。
	dir, _ := s.dir(instanceID)
	if shares, lerr := webdavd.LoadShares(dir); lerr == nil {
		for _, sh := range shares {
			if sealed := hubusers.GetSecret(db, hubusers.SecretSharePass(sh.User)); sealed != "" {
				out.ShareUser = sh.User
				out.SharePass = hubusers.OpenSecret(sealed)
				break
			}
		}
	}
	s.audit.Ok("ui", "webdav.hub-account-view", instanceID, nil)
	return out, nil
}

// NASEntryURL 桌面首屏宫格开 NAS 窗用的直登地址：127.0.0.1（恒 http——
// 回环明文不外泄，浏览器/WebView 对 localhost 也不弹证书警告）+ 管理员的
// 60 秒一次性登录票（?_k=）。票只认本机回环，扫码/浏览器直连不受影响。
func (s *WebDavService) NASEntryURL(instanceID, path string) (string, error) {
	if path != "/" && !strings.HasPrefix(path, "/-") {
		return "", fmt.Errorf("路径必须是 / 或 /-<app> 形式")
	}
	dir, err := s.dir(instanceID)
	if err != nil {
		return "", err
	}
	st, err := store.Open(filepath.Join(dir, "hub.db"))
	if err != nil {
		return "", err
	}
	defer st.Close()
	uid, _, created, err := hubusers.EnsureAdmin(st.DB(), "admin")
	if err != nil {
		return "", err
	}
	if uid == "" {
		// 已有账号：挑第一个启用的管理员（默认就是引导生成的那个）。
		us, lerr := hubusers.List(st.DB())
		if lerr != nil {
			return "", lerr
		}
		for _, u := range us {
			if u.Role == hubusers.RoleAdmin && u.Enabled {
				uid = u.UID
				break
			}
		}
	}
	if uid == "" {
		return "", fmt.Errorf("没有可用的管理员账号")
	}
	_ = created
	token, err := webdavd.IssueEntryToken(dir, uid, time.Minute)
	if err != nil {
		return "", err
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return "", fmt.Errorf("实例不存在: %s", instanceID)
	}
	// 本机恒 http：回环上没有窃听面（嗅探监听器对回环恒放行明文），
	// 自签证书的警告一个都不要弹。
	return fmt.Sprintf("http://127.0.0.1:%d%s?_k=%s", rec.Ports["dav"], path, url.QueryEscape(token)), nil
}

// mustSeal 凭证加密（DPAPI/AES），失败只能放弃（密码查看会为空，不影响登录）。
func mustSeal(plain string) string {
	sealed, err := credentials.Seal(plain)
	if err != nil {
		return ""
	}
	return sealed
}

// StorageStats 首页「存储环」数据。口径（用户定稿）：
//
//	Total/Free = **当前盘符**（网盘根目录所在的 D: / E:）的总量与剩余，
//	             不汇总物理硬盘、也不算别的分区；
//	Used       = 网盘根目录自身的占用（"NAS 占用"），-1 = 还没算完；
//	Drive      = 盘符（形如 "D:"），前端显示在存储环上，一眼看出算的是哪个盘。
type StorageStats struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Used  int64  `json:"used"`
	Drive string `json:"drive"`
	Root  string `json:"root"`
}

// statsCacheEntry 一个目录的统计快照。盘容量（便宜）与目录占用（贵）分开计时：
// 前者秒级刷新，后者 60 秒一次且**后台算**，算的时候先把旧值顶上。
type statsCacheEntry struct {
	total, free uint64
	used        int64
	volAt       time.Time // 盘容量取数时间
	usedAt      time.Time // 占用算完时间
	walking     bool      // 占用正在后台算（防重入）
}

var (
	statsCacheMu sync.Mutex
	statsCache   = map[string]*statsCacheEntry{}
)

const (
	statsVolumeTTL = 5 * time.Second
	statsUsageTTL  = time.Minute
)

// GetStorageStats 取网盘容量统计（普通模式首页用）。优先用第一个启用共享的
// 根目录，没有共享时退回实例数据目录。
func (s *WebDavService) GetStorageStats(instanceID string) (*StorageStats, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	root := dir
	if shares, err := webdavd.LoadShares(dir); err == nil {
		for _, sh := range shares {
			if sh.Enabled {
				if r := webdavd.ShareRoot(sh, dir); r != "" {
					root = r
				}
				break
			}
		}
	}

	statsCacheMu.Lock()
	e := statsCache[root]
	if e == nil {
		e = &statsCacheEntry{used: -1}
		statsCache[root] = e
	}
	staleVol := time.Since(e.volAt) > statsVolumeTTL
	staleUsed := e.used < 0 || time.Since(e.usedAt) > statsUsageTTL
	walk := staleUsed && !e.walking
	if walk {
		e.walking = true
	}
	statsCacheMu.Unlock()

	if staleVol {
		if total, free, ok := webdavd.VolumeStatsOf(root); ok {
			statsCacheMu.Lock()
			e.total, e.free, e.volAt = total, free, time.Now()
			statsCacheMu.Unlock()
		}
	}
	// 目录占用要 walk 整个根目录（几十万文件就是几十秒），必须异步：
	// 同步算会把首页卡住，而首页是启动后立刻要画的那一屏。
	if walk {
		go func() {
			n, ok := webdavd.DirUsage(root)
			statsCacheMu.Lock()
			if ok {
				e.used, e.usedAt = n, time.Now()
			}
			e.walking = false
			statsCacheMu.Unlock()
		}()
	}

	statsCacheMu.Lock()
	st := StorageStats{
		Total: e.total,
		Free:  e.free,
		Used:  e.used,
		Drive: webdavd.DriveOf(root),
		Root:  root,
	}
	statsCacheMu.Unlock()
	return &st, nil
}

// WebDavQuickInfo 扫码快传：二维码内容 + 有效期。
type WebDavQuickInfo struct {
	URL  string `json:"url"`
	User string `json:"user"`
	TTL  int    `json:"ttl"` // token 有效期（秒）
}

// CreateWebDavQuickToken 为共享用户生成扫码快传链接：手机扫码 → /-q?t=
// 自动登录 → 直接落在文件管理器页面，手机与电脑间经网盘互传文件。
// 仅对外监听时可用——127.0.0.1 手机根本连不上。
func (s *WebDavService) CreateWebDavQuickToken(instanceID, user string) (*WebDavQuickInfo, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	if s.mgr.Status(instanceID) != processd.StateRunning {
		return nil, fmt.Errorf("网盘服务未运行，请先启动")
	}
	bind := "127.0.0.1"
	tlsOn := false
	plain := true
	if values, err := s.mgr.EffectiveSettings(instanceID); err == nil {
		if v := strings.TrimSpace(values["dav.bind"]); v != "" {
			bind = v
		}
		tlsOn = values["dav.tls"] == "1"
		plain = values["dav.plain"] != "0"
	}
	if bind == "127.0.0.1" || bind == "localhost" {
		return nil, fmt.Errorf("当前网盘仅本机可访问，请先在「设置」里开启对外监听后再扫码")
	}
	host := firstLANIPv4()
	if host == "" {
		return nil, fmt.Errorf("未找到本机局域网 IPv4 地址")
	}
	token, ttl, err := webdavd.QuickToken(dir, strings.TrimSpace(user))
	if err != nil {
		return nil, err
	}
	// 手机扫码默认走 http：同端口双协议下 http 一定通，且手机不弹证书警告。
	scheme := "http"
	if tlsOn && !plain {
		scheme = "https"
	}
	port := 0
	if rec := s.mgr.GetRecord(instanceID); rec != nil {
		port = rec.Ports["dav"]
	}
	s.audit.Ok("ui", "webdav.quick-token", instanceID, map[string]any{"user": user})
	return &WebDavQuickInfo{
		URL:  webdavd.QuickLoginURL(scheme, host, port, token),
		User: strings.TrimSpace(user),
		TTL:  int(ttl.Seconds()),
	}, nil
}

// ListWebDavSiteRoots 站点根目录候选（「浏览」旁的下拉）。
func (s *WebDavService) ListWebDavSiteRoots() []SiteRootView {
	if s.sites == nil {
		return []SiteRootView{}
	}
	out := []SiteRootView{}
	for _, v := range s.sites.List() {
		out = append(out, SiteRootView{ID: v.ID, Name: v.Name, Root: v.Root})
	}
	return out
}

// restartIfRunning 共享表变化后让内置服务重新加载（停 → 等端口释放 → 启）。
func (s *WebDavService) restartIfRunning(instanceID string) error {
	if s.mgr == nil || s.mgr.Status(instanceID) != processd.StateRunning {
		return nil
	}
	port := 0
	if rec := s.mgr.GetRecord(instanceID); rec != nil {
		port = rec.Ports["dav"]
	}
	if err := s.mgr.Stop(instanceID); err != nil {
		return fmt.Errorf("共享已保存，但停止网盘服务失败: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for port > 0 && !processd.ProbeFree(port) {
		if time.Now().After(deadline) {
			return fmt.Errorf("共享已保存，但端口 %d 释放超时，请手动重启网盘实例", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.mgr.Start(instanceID); err != nil {
		return fmt.Errorf("共享已保存，但重启网盘服务失败（请手动启动）: %w", err)
	}
	return nil
}

// earlierCreatedAt a 是否早于 b。RFC3339 字符串可直接比较；空值（老数据
// 没记时间）视为最早，多个空值按列表序兜底。
func earlierCreatedAt(a, b string) bool {
	if a == "" {
		return b != ""
	}
	if b == "" {
		return false
	}
	return a < b
}

// firstLANIPv4 本机第一个非回环 IPv4（局域网挂载提示用）。
// firstLANIPv4 取本机局域网 IPv4：优先物理网卡（Wi-Fi / 以太网），避开
// Hyper-V / VMware / VirtualBox 这些虚拟网卡 —— 它们的地址手机连不上，
// 拿它生成二维码等于让人扫了个死链。
func firstLANIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	best, bestScore := "", 9
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		score := 2
		switch {
		case isVirtualIface(ifc.Name):
			score = 5
		case isPrimaryIface(ifc.Name):
			score = 0
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if score < bestScore {
				best, bestScore = ipn.IP.String(), score
			}
		}
	}
	return best
}

func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"vmware", "virtualbox", "vbox", "hyper-v", "vethernet",
		"tap", "tun", "wsl", "docker", "bluetooth", "loopback", "zerotier", "tailscale"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

func isPrimaryIface(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"wi-fi", "wifi", "wlan", "ethernet", "以太网", "无线", "本地连接"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}
