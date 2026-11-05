// m21driver M21 网盘网页消费级改版的手工验收服务。
//
// 起一个真实 webdavd（临时目录 + 固定端口 18640 + user/pass=duihao/duihao123），
// 预置各类扩展名文件（文档/图片/视频/音频/压缩包/其它），打印快传链接后挂起，
// 供浏览器 / headless 截图验证：左栏存储卡与分类、列表/缩略图双视图、移动端 chips。
// 用完杀进程即可，临时目录留系统清理。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/duihao/duihao/backend/duihao/webdavd"
)

func main() {
	root, err := os.MkdirTemp("", "m21dav")
	if err != nil {
		panic(err)
	}
	sub := filepath.Join(root, "资料")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		panic(err)
	}
	files := map[string][]byte{
		"建站方案-v3.1.docx": []byte("doc"),
		"说明文档.md":        []byte("md"),
		"产品图-01.jpg":      []byte("fakejpeg"),
		"风景.png":          []byte("fakepng"),
		"演示录屏.mp4":        []byte("fakemp4"),
		"podcast.mp3":     []byte("fakemp3"),
		"nginx-1.28.zip":  []byte("fakezip"),
		"main.go":         []byte("package main"),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(sub, n), b, 0o644); err != nil {
			panic(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.txt"), []byte("root file"), 0o644); err != nil {
		panic(err)
	}

	sh, err := webdavd.NewShare("duihao", "duihao123", root, false, "网盘", "")
	if err != nil {
		panic(err)
	}
	if err := webdavd.SaveShares(root, []webdavd.Share{sh}); err != nil {
		panic(err)
	}
	srv, err := webdavd.New(webdavd.Config{
		Bind:        "127.0.0.1",
		Port:        18640,
		Shares:      []webdavd.Share{sh},
		InstanceDir: root,
	})
	if err != nil {
		panic(err)
	}
	if err := srv.Start(); err != nil {
		panic(err)
	}
	tok, _, err := webdavd.QuickToken(root, "duihao")
	if err != nil {
		panic(err)
	}
	fmt.Println("DAV_READY")
	fmt.Println("LOGIN_URL:", webdavd.QuickLoginURL("http", "127.0.0.1", 18640, tok))
	fmt.Println("ROOT_DIR:", root)
	select {} // 挂起，直到进程被杀
}
