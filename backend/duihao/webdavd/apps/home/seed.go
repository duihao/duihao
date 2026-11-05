package home

import (
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// 默认收藏数据（2026-10-29 用户提供，已去掉「易等」分组）：
// 首次 list 按 uid 播种一次，删除后不复活（hub_meta.bookmarks.seeded 标记）。

type seedItem struct{ Name, URL string }

type seedGroup struct {
	Title string
	Items []seedItem
}

var seedData = []seedGroup{
	{Title: "搜索引擎", Items: []seedItem{
		{"百度", "https://www.baidu.com"},
		{"google", "https://www.google.com"},
		{"Bing", "https://www.bing.com"},
		{"头条", "https://www.toutiao.com"},
	}},
	{Title: "AI", Items: []seedItem{
		{"豆包", "https://www.doubao.com/chat/"},
		{"可灵AI", "https://klingai.kuaishou.com/"},
		{"Gemini", "https://gemini.google.com/app"},
		{"即梦", "https://jimeng.jianying.com/"},
		{"哩布哩布AI", "https://www.liblib.art/"},
	}},
	{Title: "视频平台", Items: []seedItem{
		{"抖音", "https://www.douyin.com/"},
		{"bilibili", "https://www.bilibili.com/"},
		{"腾讯视频", "https://v.qq.com/"},
		{"爱奇艺", "https://www.iqiyi.com"},
		{"芒果 TV", "https://www.mgtv.com"},
	}},
	{Title: "音乐", Items: []seedItem{
		{"网易云音乐", "https://music.163.com/"},
		{"QQ音乐", "https://y.qq.com/"},
		{"酷狗音乐", "https://www.kugou.com/"},
		{"酷我音乐", "https://www.kuwo.cn"},
		{"汽水音乐", "https://music.douyin.com"},
	}},
	{Title: "装机必备", Items: []seedItem{
		{"ToDesk", "https://www.todesk.com/"},
		{"UU远程", "https://uuyc.163.com/download/"},
		{"QQ", "https://im.qq.com/index/?from=qqnews"},
		{"迅雷", "https://www.xunlei.com/"},
		{"微信", "https://weixin.qq.com/"},
		{"winrar", "https://www.winrar.com.cn/download.htm"},
		{"windows11", "https://www.microsoft.com/zh-cn/software-download/windows11"},
		{"向日葵", "https://sunlogin.oray.com/download"},
		{"chrome", "https://www.google.com/chrome/"},
		{"百度网盘", "https://pan.baidu.com/"},
		{"NVIDIA录屏", "https://www.nvidia.cn/software/nvidia-app-enterprise/"},
		{"office", "https://www.office.com"},
	}},
	{Title: "登录入口", Items: []seedItem{
		{"农业银行", "https://www.abchina.com/cn/"},
		{"网易邮箱", "https://mail.163.com/"},
		{"工商银行", "https://www.icbc.com.cn/icbc/"},
		{"gmail", "https://accounts.google.com/"},
		{"路由器", "http://192.168.0.1/"},
		{"最简单AI数字人整合包VideoReTalking，一键生成AI数字人！ - 腾讯云开发者社区-腾讯云", "https://cloud.tencent.com/developer/news/1230347"},
	}},
	{Title: "购物", Items: []seedItem{
		{"京东", "https://www.jd.com"},
		{"淘宝", "https://www.taobao.com/"},
		{"闲鱼", "https://www.goofish.com/"},
		{"唯品会", "https://www.vip.com/"},
		{"得物", "https://www.dewu.com/"},
	}},
	{Title: "知识", Items: []seedItem{
		{"百度翻译", "https://fanyi.baidu.com/"},
		{"phet", "https://phet.colorado.edu/zh_CN/"},
		{"中小学智慧平台", "https://basic.smartedu.cn/"},
		{"百度文库", "https://wenku.baidu.com/"},
	}},
	{Title: "内网穿透", Items: []seedItem{
		{"花生壳", "https://hsk.oray.com/download"},
		{"cpolar", "https://www.cpolar.com/"},
		{"路由侠", "https://www.luyouxia.com/"},
	}},
	{Title: "代码库", Items: []seedItem{
		{"GitHub - chenggx/douyin-parse-danmu: 抖音直播弹幕抓取", "https://github.com/chenggx/douyin-parse-danmu"},
		{"GitHub - Sjj1024/douyin-live: Python学习项目，仅供学习参考，严禁一切不合规使用", "https://github.com/Sjj1024/douyin-live"},
		{"GitHub - OpenTalker/video-retalking: [SIGGRAPH Asia 2022] VideoReTalking: Audio-based Lip Synchronization for Talking Head Video Editing In the Wild", "https://github.com/OpenTalker/video-retalking"},
	}},
	{Title: "接口", Items: []seedItem{
		{"微信公众平台", "https://mp.weixin.qq.com/"},
		{"小程序多端框架", "https://developers.weixin.qq.com/platform/miniapp"},
	}},
	{Title: "其它", Items: []seedItem{
		{"工作台 - Vben Admin Ele", "https://ele.vben.pro/#/workspace"},
		{"Stable Diffusion秋叶WebUI安装包下载 Windows系统_StableDiffusion中文网", "https://www.stablediffusion.cn/a14.html"},
		{"Feather – Simply beautiful open source icons", "https://feathericons.com/"},
		{"Search - Iconify", "https://icon-sets.iconify.design/?query=edit"},
		{"在线AI转换官网-在线文字转语音,在线语音转文字,在线文字识别,在线图像处理", "https://www.zaixianai.cn/"},
		{"企业猫源码网-专注于分享精品源码的源码网", "https://www.qymao.cn/"},
		{"Online FlowChart & Diagrams Editor - Mermaid Live Editor", "https://mermaid-js.github.io/mermaid-live-editor/edit"},
		{"REECHO 睿声 - 超拟真AI语音合成与瞬时克隆平台", "https://community.reecho.cn/"},
		{"寻一点 | 致力打造实用、智能的在线工具箱", "https://xunyidian.com/"},
		{"在线AI创作-智能写作生成神器- AI文案制作-AI表单制作-易企秀", "https://ai.eqxiu.com/"},
		{"Admin | 表格", "http://103.39.211.179:8080/admin/components/grid"},
		{"Overview 组件总览 | Element Plus", "https://element-plus.org/zh-CN/component/overview.html"},
		{"支付 - 抖音开放平台", "https://developer.open-douyin.com/microapp/tt300946b4af3121d501/pay"},
		{"华为商城", "https://www.vmall.com/?cid=125903"},
		{"宝塔Linux面板", "https://59.110.223.185:8999/duihao"},
		{"IPPBX管理平台", "https://www.fspbx.cn/#/login"},
		{"线下门店查询-绿联经销网络-UGREEN绿联官网", "https://www.lulian.cn/about/798.html"},
		{"uTools AI 生成应用", "https://www.u-tools.cn/ai-apps/"},
	}},
}

// seed 首次 list 播种默认收藏；只播一次，删光也不复活。
func seed(c appkit.Context) error {
	var n int
	if err := c.DB.QueryRow(`select count(*) from hub_meta where uid=? and key='bookmarks.seeded'`, c.User).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	for gi, g := range seedData {
		gid := "sg-" + itoa(gi+1)
		if _, err := tx.Exec(`insert or ignore into bookmark_groups(uid,id,title,sort,created_at) values(?,?,?,?,?)`,
			c.User, gid, g.Title, (gi+1)*10, now); err != nil {
			_ = tx.Rollback()
			return err
		}
		for ii, it := range g.Items {
			u, err := normalizeURL(it.URL)
			if err != nil {
				continue // 脏数据不阻塞播种
			}
			if _, err := tx.Exec(`insert or ignore into bookmarks(uid,id,group_id,title,url,note,account,pwd_hint,sort,created_at,updated_at)
				values(?,?,?,?,?,?,?,?,?,?,?)`,
				c.User, gid+"-b"+itoa(ii+1), gid, it.Name, u, "", "", "", (ii+1)*10, now, now); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if _, err := tx.Exec(`insert or replace into hub_meta(uid,key,val) values(?,?,'1')`, c.User, "bookmarks.seeded"); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
