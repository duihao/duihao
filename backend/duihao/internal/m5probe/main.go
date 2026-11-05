// m5probe：验证数据根目录判定（便携优先 / 回退）。
package main

import (
	"fmt"

	duihao "github.com/duihao/duihao/backend/duihao"
)

func main() {
	fmt.Printf("DataRoot=%s\nPortable=%v\nRuntimes=%s\n", duihao.DataRoot(), duihao.Portable(), duihao.DirRuntimes())
}
