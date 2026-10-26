package webdavd

import "testing"

// 路径安全单测（v3.1 §6.2 硬要求：.. / 保留名 / 非法字符三条必须覆盖）。
func TestCheckRequestPath(t *testing.T) {
	cases := []struct {
		name    string
		decoded string
		raw     string
		wantErr bool
	}{
		{"普通路径", "/dir/file.txt", "", false},
		{"中文", "/目录/文件.txt", "", false},
		{"明文穿越", "/../etc/passwd", "", true},
		{"段中穿越", "/a/../b", "", true},
		{"尾段穿越", "/a/b/..", "", true},
		{"编码穿越", "/a/../b", "/a/%2e%2e/b", true},
		{"保留名 CON", "/CON", "", true},
		{"保留名小写", "/con", "", true},
		{"保留名带扩展", "/CON.txt", "", true},
		{"保留名 COM1", "/COM1", "", true},
		{"非法字符冒号", "/a:b", "", true},
		{"非法字符星号", "/a*b", "", true},
		{"非法字符问号", "/a?b", "", true},
		{"非法字符竖线", "/a|b", "", true},
		{"非法字符引号", `/a"b`, "", true},
		{"非法字符小于", "/a<b", "", true},
		{"尾点", "/file.", "", true},
		{"尾空格", "/file ", "", true},
		{"正常点开头", "/.hidden", "", false},
		{"正常当前段", "/a/./b", "", false},
		{"双斜杠空段", "/a//b", "", false},
		{"控制字符", "/a\x01b", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckRequestPath(c.decoded, c.raw)
			if (err != nil) != c.wantErr {
				t.Fatalf("CheckRequestPath(%q, %q) err=%v, wantErr=%v", c.decoded, c.raw, err, c.wantErr)
			}
		})
	}
}

func TestCheckRelPath(t *testing.T) {
	if err := CheckRelPath("/a/b/../c"); err == nil {
		t.Fatal("逻辑路径穿越未拦截")
	}
	if err := CheckRelPath("/正常/目录/文件.zip"); err != nil {
		t.Fatalf("正常中文路径被误拦: %v", err)
	}
}

func TestCheckRoot(t *testing.T) {
	ok := []string{`D:\www\dav`, "D:/www/dav", `E:\data`, `C:\Users\me\网盘`}
	for _, r := range ok {
		if err := CheckRoot(r); err != nil {
			t.Errorf("CheckRoot(%q) 应通过, got %v", r, err)
		}
	}
	bad := []string{`C:\`, `D:\`, "D:/", `\\server\share`, "//server/share", "www/dav", "", "  "}
	for _, r := range bad {
		if err := CheckRoot(r); err == nil {
			t.Errorf("CheckRoot(%q) 应拒绝", r)
		}
	}
}
