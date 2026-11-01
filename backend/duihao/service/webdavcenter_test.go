package service

import "testing"

// 首屏「访问地址」下拉：三条地址的可用性判定与拼装。
func TestBuildDavAddrs(t *testing.T) {
	type want struct {
		kind, url string
		ok        bool
		note, fix string
	}
	cases := []struct {
		name   string
		scheme string
		port   int
		bind   string
		lan    string
		ip6    string
		want   []want
	}{
		{
			// 默认监听（127.0.0.1）：只本机可用，另两条置灰并带 fix=lan
			// —— 首屏据此显示「开启」按钮走一键改监听。
			name: "loopback-only", scheme: "https", port: 9111,
			bind: "127.0.0.1", lan: "192.168.1.5", ip6: "2408:8207::9",
			want: []want{
				{"local", "http://127.0.0.1:9111/", true, "", ""},
				{"lan", "https://192.168.1.5:9111/", false, "仅本机可访问", "lan"},
				{"v6", "https://[2408:8207::9]:9111/", false, "仅本机可访问", "lan"},
			},
		},
		{
			// 对外监听 + TLS：三条全可用，IPv6 必须带方括号
			name: "public-tls", scheme: "https", port: 9111,
			bind: "0.0.0.0", lan: "192.168.1.5", ip6: "2408:8207::9",
			want: []want{
				{"local", "http://127.0.0.1:9111/", true, "", ""},
				{"lan", "https://192.168.1.5:9111/", true, "", ""},
				{"v6", "https://[2408:8207::9]:9111/", true, "", ""},
			},
		},
		{
			// 对外监听但没开 TLS：能用，但提示明文风险（不用给 fix，改设置也修不了）
			name: "public-plain", scheme: "http", port: 9111,
			bind: "0.0.0.0", lan: "10.0.0.7", ip6: "",
			want: []want{
				{"local", "http://127.0.0.1:9111/", true, "", ""},
				{"lan", "http://10.0.0.7:9111/", true, "明文 HTTP，建议开启 TLS", ""},
				{"v6", "—", false, "没有公网 IPv6（光猫/路由要开 IPv6）", ""},
			},
		},
		{
			// 没有局域网地址（虚拟网卡被判掉）：占位符 + 原因，且不该给「开启」按钮
			name: "no-lan", scheme: "https", port: 9111,
			bind: "0.0.0.0", lan: "", ip6: "2408::1",
			want: []want{
				{"local", "http://127.0.0.1:9111/", true, "", ""},
				{"lan", "—", false, "没有找到局域网 IPv4", ""},
				{"v6", "https://[2408::1]:9111/", true, "", ""},
			},
		},
		{
			// 只听本机 + 又没局域网地址：原因取「没找到」，仍不给 fix（一键也救不回来）
			name: "loopback-no-lan", scheme: "https", port: 9111,
			bind: "127.0.0.1", lan: "", ip6: "",
			want: []want{
				{"local", "http://127.0.0.1:9111/", true, "", ""},
				{"lan", "—", false, "没有找到局域网 IPv4", ""},
				{"v6", "—", false, "没有公网 IPv6（光猫/路由要开 IPv6）", ""},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildDavAddrs(c.scheme, c.port, c.bind, c.lan, c.ip6)
			if len(got) != len(c.want) {
				t.Fatalf("want %d addrs, got %d", len(c.want), len(got))
			}
			for i, w := range c.want {
				g := got[i]
				if g.Kind != w.kind || g.URL != w.url || g.OK != w.ok || g.Note != w.note || g.Fix != w.fix {
					t.Errorf("#%d got {%s %q ok=%v note=%q fix=%q}, want {%s %q ok=%v note=%q fix=%q}",
						i, g.Kind, g.URL, g.OK, g.Note, g.Fix, w.kind, w.url, w.ok, w.note, w.fix)
				}
			}
		})
	}
}
