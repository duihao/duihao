// 生命周期动作白名单（方案 §4.4）：mkdir / copy / write / render / exec-once。
// 铁律 3：加字段不扩权 —— 动作只触碰实例目录与运行时目录，
// exec-once 只能执行 {bin}（清单 bin 映射）或运行时目录内的二进制。
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/pluginc"
)

// Vars 动作执行时的变量上下文（token 替换源）。
type Vars struct {
	ID         string         // 实例 id
	Dir        string         // 实例数据目录 instances/<id>
	RuntimeDir string         // 运行时目录 runtimes/<plugin>/<ver>
	Bin        string         // 清单 bin 映射的绝对路径
	Version    string         // 实例 runtime 版本（{setifver:} 门控用）
	Ports      map[string]int // 角色 → 端口
	Set        map[string]string // 声明式设置（{set:key}；端口字段=实际端口）
}

// ErrConfirmNeeded exec-once 带 confirm 且未获用户确认。
type ErrConfirmNeeded struct {
	InstanceID string
	Index      int    // 动作下标
	Action     string // 动作名（展示）
	Cmd        string // 将执行的命令（展示）
}

func (e *ErrConfirmNeeded) Error() string {
	return fmt.Sprintf("动作 %d（%s %s）需要用户确认后执行", e.Index, e.Action, e.Cmd)
}

// applyTokens 统一大括号 token：{dir} {runtime} {bin} {id} {port:xxx}。
// 路径统一转正斜杠：nginx 等程序把配置文件里的 `\n`（…\instances\nginx-1）
// 当转义换行解析（实测 emerg 123）；Windows API 与 nginx/php/mysql 均接受 /。
func applyTokens(s string, v Vars) string {
	rep := func(val string) string { return strings.ReplaceAll(val, `\`, `/`) }
	s = strings.ReplaceAll(s, "{dir}", rep(v.Dir))
	s = strings.ReplaceAll(s, "{runtime}", rep(v.RuntimeDir))
	s = strings.ReplaceAll(s, "{bin}", rep(v.Bin))
	s = strings.ReplaceAll(s, "{id}", v.ID)
	for name, port := range v.Ports {
		s = strings.ReplaceAll(s, "{port:"+name+"}", fmt.Sprint(port))
	}
	// {set:key}：声明式设置值（M8）。清单 settings 里 Default 先铺底，
	// 用户 override 覆盖，端口字段由 settingValues 填实际端口。
	if len(v.Set) > 0 {
		for key, val := range v.Set {
			s = strings.ReplaceAll(s, "{set:"+key+"}", rep(val))
		}
	}
	// {setif:key:text}：key 值为 "1" 时输出 text+换行，否则整段删除 ——
	// 开关类设置的条件行（如 skip-name-resolve、log-bin）。text 里可引用
	// 已替换过的 {set:...}/{dir} 等 token（本 pass 在其之后）。
	for {
		start := strings.Index(s, "{setif:")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		inner := s[start+len("{setif:") : start+end]
		colon := strings.Index(inner, ":")
		if colon < 0 {
			s = s[:start] + s[start+end+1:]
			continue
		}
		key, text := inner[:colon], inner[colon+1:]
		out := ""
		// 语义：非空且非 "0" 才输出。switch 用 "1"/"0"；text/password 有值即输出
		//（如 requirepass <密码>——值本身就是输出内容的一部分）。
		if val := v.Set[key]; val != "" && val != "0" {
			out = text + "\n"
		}
		s = s[:start] + out + s[start+end+1:]
	}
	// {setifver:8.0.30:text}：实例版本 ≥ 阈值（逐段数值比较）时输出 text+换行。
	// 用途：同一份清单模板兼容多个版本（如 MySQL 5.7 不认识 8.0.30+ 的
	// innodb_redo_log_capacity，该行只在 8.0.30+ 渲染）。text 内的 {set:...}
	// 已在前一 pass 替换成实际值，此处不会撞上内层大括号。
	for {
		start := strings.Index(s, "{setifver:")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		inner := s[start+len("{setifver:") : start+end]
		colon := strings.Index(inner, ":")
		if colon < 0 {
			s = s[:start] + s[start+end+1:]
			continue
		}
		minVer, text := inner[:colon], inner[colon+1:]
		out := ""
		if verAtLeast(v.Version, minVer) {
			out = text + "\n"
		}
		s = s[:start] + out + s[start+end+1:]
	}
	// {setiflt:8.4.0:text}：实例版本 < 阈值时输出 text+换行（setifver 的反向）。
	// 用途：只存在于低版本的配置项 —— 如 PHP 8.4 把 imap 移到了 PECL，
	// 官方 Windows 包不再带 php_imap.dll，8.4 上写 extension=imap 只会刷警告。
	// Version 为空（老实例记录缺字段）时按「低版本」处理：宁可多写一行已知
	// 存在的扩展，也不要让老实例丢扩展。
	for {
		start := strings.Index(s, "{setiflt:")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		inner := s[start+len("{setiflt:") : start+end]
		colon := strings.Index(inner, ":")
		if colon < 0 {
			s = s[:start] + s[start+end+1:]
			continue
		}
		maxVer, text := inner[:colon], inner[colon+1:]
		out := ""
		if v.Version == "" || !verAtLeast(v.Version, maxVer) {
			out = text + "\n"
		}
		s = s[:start] + out + s[start+end+1:]
	}
	// {setonoff:key}：switch 的 "1"/"0" → on/off（nginx 的 sendfile/tcp_nopush）。
	if len(v.Set) > 0 {
		for key, val := range v.Set {
			onoff := "off"
			if val == "1" {
				onoff = "on"
			}
			s = strings.ReplaceAll(s, "{setonoff:"+key+"}", onoff)
		}
	}
	return s
}

// guardInside 确保目标路径不越出 base（M0 V5 教训：不能先 Clean 折叠 ..）。
func guardInside(base, target string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return fmt.Errorf("路径解析失败: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("路径越界，已拒绝: %s", target)
	}
	return nil
}

// resolveIn 解析 token 后的路径，并保证 target 落在 base 内。
func resolveIn(base, raw string, v Vars) (string, error) {
	p := applyTokens(raw, v)
	if err := guardInside(base, p); err != nil {
		return "", err
	}
	return p, nil
}

// RunActions 依序执行白名单动作。
//
// confirmed：exec-once 的用户确认表（key = 动作下标十进制字符串）；
// 声明 confirm 而未获确认 → 返回 *ErrConfirmNeeded，确认后重放整个序列
//（mkdir/copy/write 幂等，重放安全）。
func RunActions(actions []pluginc.Action, v Vars, confirmed map[string]bool, auditLog *audit.Logger) error {
	for i, a := range actions {
		if err := runOne(i, a, v, confirmed, auditLog); err != nil {
			return err
		}
	}
	return nil
}

func runOne(i int, a pluginc.Action, v Vars, confirmed map[string]bool, auditLog *audit.Logger) error {
	fail := func(err error) error {
		if auditLog != nil {
			auditLog.Fail("instance", "action:"+a.Action, v.ID, map[string]any{"index": i, "error": err.Error()})
		}
		return fmt.Errorf("动作 %d（%s）失败: %w", i, a.Action, err)
	}

	switch a.Action {
	case "mkdir":
		p, err := resolveIn(v.Dir, a.Path, v)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fail(err)
		}

	case "copy":
		src := applyTokens(a.From, v)
		// 源允许来自运行时目录或实例目录；目标必须在实例目录内。
		if !strings.HasPrefix(src, v.RuntimeDir) && !strings.HasPrefix(src, v.Dir) {
			return fail(fmt.Errorf("copy 源必须在运行时或实例目录内: %s", a.From))
		}
		dst, err := resolveIn(v.Dir, firstNonEmpty(a.To, a.Dst), v)
		if err != nil {
			return fail(err)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fail(err)
		}

	case "write":
		dst, err := resolveIn(v.Dir, firstNonEmpty(a.Dst, a.To), v)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		content := applyTokens(a.From, v)
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return fail(err)
		}

	case "render":
		// 模板来自运行时目录（src 相对路径），渲染后写实例目录。
		tplPath := filepath.Join(v.RuntimeDir, filepath.FromSlash(a.Src))
		tplData, err := os.ReadFile(tplPath)
		if err != nil {
			return fail(err)
		}
		dst, err := resolveIn(v.Dir, firstNonEmpty(a.Dst, a.To), v)
		if err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		rendered := applyTokens(string(tplData), v)
		if err := os.WriteFile(dst, []byte(rendered), 0o644); err != nil {
			return fail(err)
		}

	case "exec-once":
		cmd := applyTokens(a.Cmd, v)
		// 保险 ①：只能执行运行时目录内的二进制。
		// 注意 applyTokens 会把 `\` 统一转 `/`（nginx 转义坑），而 v.RuntimeDir
		// 是原生反斜杠路径 —— 必须归一后再比对，否则合法的 {bin} 永远被拒
		//（MySQL 首装实测：mysqld --initialize-insecure 被拒）。
		cmdAbs := filepath.Clean(filepath.FromSlash(cmd))
		rtAbs := filepath.Clean(v.RuntimeDir)
		withinRuntime := strings.EqualFold(cmdAbs, rtAbs) ||
			strings.HasPrefix(strings.ToLower(cmdAbs), strings.ToLower(rtAbs)+string(filepath.Separator))
		if !withinRuntime {
			return fail(fmt.Errorf("exec-once 只允许执行运行时二进制，已拒绝: %s", cmd))
		}
		// 保险 ②：幂等标记存在即跳过。
		if a.IdempotentBy != "" {
			mark, err := resolveIn(v.Dir, a.IdempotentBy, v)
			if err == nil {
				if _, err := os.Stat(mark); err == nil {
					return nil // 已初始化过
				}
			}
		}
		// 保险 ③：需要用户确认而未确认 → 中断等待。
		if a.Confirm && !confirmed[fmt.Sprint(i)] {
			return &ErrConfirmNeeded{InstanceID: v.ID, Index: i, Action: a.Action, Cmd: cmd}
		}
		args := make([]string, len(a.Args))
		for j, s := range a.Args {
			args[j] = applyTokens(s, v)
		}
		run, err := execOnce(cmd, args, v.Dir)
		if err != nil {
			return fail(err)
		}
		if auditLog != nil {
			auditLog.Ok("instance", "action:exec-once", v.ID, map[string]any{
				"index": i, "cmd": cmd, "args": args, "exit": run,
			})
		}

	default:
		// 白名单之外的动作：明确报错，绝不静默执行。
		return fail(fmt.Errorf("未声明的动作类型 %q（白名单: mkdir/copy/write/render/exec-once）", a.Action))
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// verAtLeast 版本逐段数值比较：v ≥ min 为 true。缺段按 0（"8.0" == "8.0.0"）。
// 非数字段按 0 处理，绝不 panic —— 清单数据驱动的比较必须无害。
func verAtLeast(v, min string) bool {
	vs := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	ms := strings.Split(strings.TrimPrefix(strings.TrimSpace(min), "v"), ".")
	for i := 0; i < 3; i++ {
		a, b := 0, 0
		if i < len(vs) {
			a, _ = strconv.Atoi(strings.TrimSpace(vs[i]))
		}
		if i < len(ms) {
			b, _ = strconv.Atoi(strings.TrimSpace(ms[i]))
		}
		if a != b {
			return a > b
		}
	}
	return true
}

// ErrConfirmNeeded 满足 errors.As。
var _ error = (*ErrConfirmNeeded)(nil)

func asConfirmNeeded(err error) (*ErrConfirmNeeded, bool) {
	var e *ErrConfirmNeeded
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
