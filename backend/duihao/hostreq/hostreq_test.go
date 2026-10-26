package hostreq

import (
	"net"
	"strconv"
	"testing"
)

func TestCheckPass(t *testing.T) {
	// 找一个空闲端口做通过样例
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	fails := Check([]string{"disk:free>=1M", "port:free:" + strconv.Itoa(port)}, ".")
	if len(fails) != 0 {
		t.Fatalf("应全部通过: %+v", fails)
	}
}

func TestCheckPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	fails := Check([]string{"port:free:" + strconv.Itoa(port)}, ".")
	if len(fails) != 1 {
		t.Fatalf("占用端口应不通过: %+v", fails)
	}
}

func TestCheckDiskTooSmall(t *testing.T) {
	fails := Check([]string{"disk:free>=99999T"}, ".")
	if len(fails) != 1 {
		t.Fatalf("超大磁盘要求应不通过: %+v", fails)
	}
}

func TestCheckUnknownConditionFails(t *testing.T) {
	fails := Check([]string{"registry:key:foo"}, ".")
	if len(fails) != 1 {
		t.Fatalf("未知条件应缺省拒绝: %+v", fails)
	}
}
