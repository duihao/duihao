package download

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/childproc"
	"github.com/duihao/duihao/backend/duihao/webdavd/compkit"
)

// 下载引擎（aria2c 走 RPC 常驻）。
//
// 生命周期口径：
//   - **用到才起**：只有真正要下东西（add / resume）才拉进程；单纯打开页面
//     看看列表不起 —— 跟 ffmpeg 那条「组件用到才下」是同一个立场。
//   - **闲着就关**：最后一个任务结束后再挂 10 分钟（给「下一集连着下」
//     留余量），然后进程退掉，内存还给系统。
//   - **对号死它必死**：Job Object（KILL_ON_JOB_CLOSE）+ aria2 自己的
//     --stop-with-process=<对号 pid>，双保险，不留孤儿进程占着 6881 端口。

const (
	// rpcPortRange RPC 只在回环上，端口从 6800 起找空位。
	// 避开 phpstudy 常驻的 80/8080/9000 与数据库/站点端口段。
	rpcPortMin = 6800
	rpcPortMax = 6899
	// engineIdle 最后一个任务结束后多久关引擎。
	engineIdle = 10 * time.Minute
	// engineLog 引擎日志（aria2 自己的 warn 输出 + 启动失败原因）。
	engineLog = "_appfiles/download/engine.log"
)

// engine 一个运行中的 aria2c 实例。整机一份（不按账号）。
type engine struct {
	proc     *childproc.Proc
	rpc      *rpcClient
	port     int
	secret   string
	root     string // 实例根，换账号/换实例就重启
	lastBusy time.Time
}

var (
	engMu sync.Mutex
	eng   *engine
)

// pickPort 在回环上找一个空闲端口。
// 先 Listen 再关是标准做法：短暂竞争可以接受（真抢不到会启动失败并报错）。
func pickPort() (int, error) {
	for p := rpcPortMin; p <= rpcPortMax; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("回环端口 %d-%d 全被占用，找不到给下载引擎用的端口", rpcPortMin, rpcPortMax)
}

func randSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// engineArgs 组装 aria2c 参数。抽成纯函数是为了能在单测里断言几个安全开关。
func engineArgs(port int, secret, defaultDir string, st Setting) []string {
	args := []string{
		"--enable-rpc=true",
		"--rpc-listen-all=false", // 只回环：局域网内谁都能扫到 6800 就麻烦了
		"--rpc-allow-origin-all=false",
		"--rpc-listen-port=" + strconv.Itoa(port),
		"--rpc-secret=" + secret,
		"--dir=" + defaultDir,
		"--continue=true",            // 断点续传：重启对号接着下
		"--file-allocation=none",     // 预分配在 NTFS 上要几分钟，不值
		"--auto-file-renaming=false", // 同名就报错，不要偷偷变成 file.1.mkv
		"--allow-overwrite=false",
		"--max-concurrent-downloads=" + strconv.Itoa(st.MaxConc),
		"--max-overall-download-limit=" + strconv.Itoa(st.DownLimit) + "K",
		"--max-overall-upload-limit=" + strconv.Itoa(st.UpLimit) + "K",
		"--disable-ipv6=true",
		"--summary-interval=0", // 进度走 RPC，不需要它自己刷屏
		"--console-log-level=warn",
		"--follow-torrent=mem", // 种子下载后不落 .torrent 垃圾文件
		"--bt-save-metadata=true",
		"--stop-with-process=" + strconv.Itoa(os.Getpid()), // 对号没了它就退
	}
	if st.Seed {
		// 做种 24 小时后自行退出（用户主动开了才这样）
		args = append(args, "--seed-time=1440")
	} else {
		args = append(args, "--seed-time=0") // 下完即停，不做种
	}
	if st.BtNet {
		args = append(args,
			"--enable-dht=true",
			"--enable-dht6=false",
			"--dht-listen-port=6881-6899",
			"--listen-port=6881-6899",
			"--bt-enable-lpd=false",
		)
	} else {
		// 关掉 BT 网络：磁力链接基本就找不source 了（页面已写明），
		// 但换来的是「对号不往公网暴露任何 UDP/TCP 监听」。
		args = append(args,
			"--enable-dht=false",
			"--enable-dht6=false",
			"--listen-port=0",
			"--bt-enable-lpd=false",
		)
	}
	return args
}

// engineUp 取当前在跑的引擎（不启动）。没在跑返回 nil。
func engineUp() *engine {
	engMu.Lock()
	defer engMu.Unlock()
	if eng != nil && eng.proc.Running() {
		return eng
	}
	if eng != nil { // 进程自己退了（崩溃 / 被关）：清掉，等下次 ensure 重起
		eng = nil
	}
	return nil
}

// ensureEngine 确保引擎在跑，返回可用的 RPC 客户端。
// 组件没装直接报 409 —— 上层要能让用户看到「去装组件」的引导，而不是干等。
func ensureEngine(c appkit.Context) (*engine, error) {
	bin := binPath("aria2")
	if bin == "" {
		return nil, appkit.Errorf(409, "缺少运行组件 aria2：请在页面点「下载并启用」")
	}

	engMu.Lock()
	defer engMu.Unlock()

	if eng != nil && eng.proc.Running() {
		if eng.root == c.Root {
			eng.lastBusy = time.Now()
			return eng, nil
		}
		// 换了账号/实例：老的必须停，否则任务会写错目录
		eng.proc.Stop()
		eng = nil
	}

	st := loadSetting(c)
	port, err := pickPort()
	if err != nil {
		return nil, appkit.Errorf(500, "%v", err)
	}
	secret := randSecret()
	defDir := filepath.Join(c.Root, "_appfiles/download")
	if err := os.MkdirAll(defDir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(c.Root, engineLog)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}

	proc, _, err := childproc.Start(bin, engineArgs(port, secret, defDir, st), childproc.Options{LogPath: logPath})
	if err != nil {
		return nil, appkit.Errorf(500, "下载引擎启动失败：%v", err)
	}
	e := &engine{proc: proc, rpc: newRPC(port, secret), port: port, secret: secret,
		root: c.Root, lastBusy: time.Now()}

	// 等 RPC 就绪：aria2 起进程到监听成功有 100~800ms 的空档
	var lastErr error
	for i := 0; i < 40; i++ {
		if _, err := e.rpc.version(); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		if !proc.Running() {
			lastErr = fmt.Errorf("进程已退出，请看 %s", engineLog)
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr != nil {
		proc.Stop()
		return nil, appkit.Errorf(500, "下载引擎没起来：%v", lastErr)
	}
	// 起来之后立刻把设置里的限速/并发同步进去（参数里已经带了，这里兜一次
	// 防止用户中途改过设置而引擎是旧的）
	_ = e.rpc.applyLimits(st.DownLimit, st.UpLimit, st.MaxConc)

	log.Printf("download: 引擎就绪 127.0.0.1:%d（并发 %d，限速 %dK/s）", port, st.MaxConc, st.DownLimit)
	eng = e
	return e, nil
}

// stopEngine 主动停引擎（用户点「停止全部」或组件被卸载时）。
func stopEngine() {
	engMu.Lock()
	defer engMu.Unlock()
	if eng != nil {
		eng.proc.Stop()
		eng = nil
	}
}

// reapIdle 空闲回收：最后一批任务结束 10 分钟后把引擎关掉。
// 由 list 轮询驱动，不额外起 goroutine（少一个后台循环就少一处泄漏点）。
func reapIdle() {
	engMu.Lock()
	defer engMu.Unlock()
	if eng == nil || !eng.proc.Running() {
		return
	}
	if time.Since(eng.lastBusy) < engineIdle {
		return
	}
	log.Printf("download: 空闲 %v，关闭引擎", engineIdle)
	eng.proc.Stop()
	eng = nil
}

// componentSpec 引擎组件（aria2c）。GPLv2 —— 对号**不打包**，只在用户机器上
// 调用已有副本，跟 MySQL 的处理方式一致：下载器模式分发。
func componentSpec() compkit.Spec {
	return compkit.Spec{
		Tag:     "download",
		ID:      "aria2",
		Name:    "aria2",
		Version: "1.37.0",
		Bin:     "aria2c.exe",
		Size:    4 << 20,
		Note:    "下载引擎：HTTP/HTTPS/FTP/磁力/BT，一个进程承载整个队列",
		URLs: []string{
			"https://github.com/aria2/aria2/releases/download/release-1.37.0/aria2-1.37.0-win-64bit-build1.zip",
			"https://ghproxy.net/https://github.com/aria2/aria2/releases/download/release-1.37.0/aria2-1.37.0-win-64bit-build1.zip",
			"https://gh-proxy.com/https://github.com/aria2/aria2/releases/download/release-1.37.0/aria2-1.37.0-win-64bit-build1.zip",
		},
	}
}

// binPath 组件可执行文件路径；未装返回空串。
func binPath(id string) string {
	if id != "aria2" {
		return ""
	}
	return compkit.Bin(componentSpec())
}

// compStatus 组件状态（页面引导卡）。
func compStatus() compkit.Status { return compkit.StatusOf(componentSpec()) }

// installComp 后台装组件。
func installComp() error {
	if err := compkit.Install(componentSpec()); err != nil {
		return appkit.Errorf(500, "%v", err)
	}
	return nil
}

// importComp 离线导入 zip。
func importComp(zipPath string) (string, error) {
	dir, err := compkit.Import(componentSpec(), zipPath)
	if err != nil {
		return "", appkit.Errorf(400, "%v", err)
	}
	return dir, nil
}
