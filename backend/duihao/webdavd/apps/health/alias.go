package health

import (
	"strings"
)

// aliasTable 同义项归并表：key 是「归一化后的名字（小写、去空格/连字符/括号内容）」，
// value 是稳定的短标识。只做归并，不做单位换算，也不做医学解释。
//
// 为什么需要：2022 年的单据写「ALT」，2025 年写「谷丙转氨酶」，
// 不归并的话趋势图会被拆成两条互不相干的线。
var aliasTable = map[string]string{
	// 肝功能
	"alt": "alt", "谷丙转氨酶": "alt", "丙氨酸氨基转移酶": "alt", "丙氨酸转氨酶": "alt",
	"ast": "ast", "谷草转氨酶": "ast", "天冬氨酸氨基转移酶": "ast", "天门冬氨酸氨基转移酶": "ast",
	"ggt": "ggt", "谷氨酰转肽酶": "ggt", "γ谷氨酰转肽酶": "ggt", "γ谷氨酰基转移酶": "ggt", "ggtp": "ggt",
	"alp": "alp", "碱性磷酸酶": "alp", "akp": "alp",
	"tbil": "tbil", "总胆红素": "tbil", "tbil总胆红素": "tbil",
	"dbil": "dbil", "直接胆红素": "dbil", "结合胆红素": "dbil",
	"ibil": "ibil", "间接胆红素": "ibil", "非结合胆红素": "ibil",
	"tp": "tp", "总蛋白": "tp",
	"alb": "alb", "白蛋白": "alb", "清蛋白": "alb",
	"glb": "glb", "球蛋白": "glb",
	"ag": "ag", "白球比": "ag", "白球比值": "ag",

	// 肾功能
	"crea": "crea", "cr": "crea", "肌酐": "crea", "血肌酐": "crea", "scr": "crea",
	"bun": "bun", "尿素": "bun", "尿素氮": "bun", "血尿素氮": "bun",
	"ua": "ua", "尿酸": "ua", "血尿酸": "ua",
	"egfr": "egfr", "肾小球滤过率": "egfr", "估算肾小球滤过率": "egfr",

	// 血脂
	"tc": "tc", "总胆固醇": "tc", "胆固醇": "tc", "chol": "tc",
	"tg": "tg", "甘油三酯": "tg", "三酰甘油": "tg",
	"hdl": "hdl", "hdlc": "hdl", "高密度脂蛋白": "hdl", "高密度脂蛋白胆固醇": "hdl",
	"ldl": "ldl", "ldlc": "ldl", "低密度脂蛋白": "ldl", "低密度脂蛋白胆固醇": "ldl",

	// 血糖
	"glu": "glu", "血糖": "glu", "空腹血糖": "glu", "葡萄糖": "glu", "fpbg": "glu",
	"hba1c": "hba1c", "糖化血红蛋白": "hba1c", "ghb": "hba1c",
	"ins": "ins", "胰岛素": "ins",

	// 血常规
	"wbc": "wbc", "白细胞": "wbc", "白细胞计数": "wbc", "白血球": "wbc",
	"rbc": "rbc", "红细胞": "rbc", "红细胞计数": "rbc",
	"hgb": "hgb", "hb": "hgb", "血红蛋白": "hgb",
	"plt": "plt", "血小板": "plt", "血小板计数": "plt",
	"hct": "hct", "红细胞压积": "hct", "红细胞比容": "hct",
	"mcv": "mcv", "平均红细胞体积": "mcv",
	"mch": "mch", "平均红细胞血红蛋白量": "mch",
	"mchc": "mchc", "平均红细胞血红蛋白浓度": "mchc",
	"neut": "neut", "中性粒细胞": "neut", "中性粒细胞百分比": "neut",
	"lymph": "lymph", "淋巴细胞": "lymph", "淋巴细胞百分比": "lymph",
	"mono": "mono", "单核细胞百分比": "mono",
	"eo": "eo", "嗜酸性粒细胞百分比": "eo",

	// 尿常规
	"upro": "u-pro", "尿蛋白": "u-pro", "蛋白质": "u-pro",
	"uglu": "u-glu", "尿糖": "u-glu", "尿葡萄糖": "u-glu",
	"ubld": "u-bld", "尿潜血": "u-bld", "隐血": "u-bld", "尿隐血": "u-bld",
	"uleu": "u-leu", "尿白细胞": "u-leu",
	"uph": "u-ph", "尿ph": "u-ph", "ph": "u-ph",
	"usg": "u-sg", "尿比重": "u-sg",

	// 炎症 / 甲状腺 / 心肌 / 肿瘤标志物
	"crp": "crp", "c反应蛋白": "crp", "超敏c反应蛋白": "crp", "hscrp": "crp",
	"esr": "esr", "血沉": "esr", "红细胞沉降率": "esr",
	"tsh": "tsh", "促甲状腺激素": "tsh",
	"ft3": "ft3", "游离t3": "ft3", "游离三碘甲状腺原氨酸": "ft3",
	"ft4": "ft4", "游离t4": "ft4", "游离甲状腺素": "ft4",
	"t3": "t3", "三碘甲状腺原氨酸": "t3",
	"t4": "t4", "甲状腺素": "t4",
	"ck": "ck", "肌酸激酶": "ck",
	"ckmb": "ckmb", "肌酸激酶同工酶": "ckmb",
	"ldh": "ldh", "乳酸脱氢酶": "ldh",
	"ctni": "ctni", "肌钙蛋白": "ctni", "肌钙蛋白i": "ctni",
	"afp": "afp", "甲胎蛋白": "afp",
	"cea": "cea", "癌胚抗原": "cea",
	"ca125": "ca125", "ca199": "ca199", "ca153": "ca153", "psa": "psa",
	"hcy": "hcy", "同型半胱氨酸": "hcy",
	"vd": "vd", "维生素d": "vd", "25羟基维生素d": "vd",
	"fer": "fer", "铁蛋白": "fer",

	// 日常自测（H3）：让「自测的体重」和「体检报告里的体重」连成同一条线
	"sbp": "sbp", "收缩压": "sbp", "高压": "sbp",
	"dbp": "dbp", "舒张压": "dbp", "低压": "dbp",
	"weight": "weight", "体重": "weight",
	"pulse": "pulse", "心率": "pulse", "脉搏": "pulse", "静息心率": "pulse",
	"temp": "temp", "体温": "temp",
	"spo2": "spo2", "血氧": "spo2", "血氧饱和度": "spo2",
	"waist": "waist", "腰围": "waist",
	"bmi": "bmi", "体重指数": "bmi",
}

// normalizeAlias 把项目名归一成趋势用的键：
// 全角转半角 → 去括号内容（「谷丙转氨酶(ALT)」→ 谷丙转氨酶）→ 去空格/连字符/下划线 → 查表。
func normalizeAlias(name string) string {
	k := strings.TrimSpace(name)
	if k == "" {
		return ""
	}
	k = strings.NewReplacer("（", "(", "）", ")", "：", ":", "　", "", " ", "").Replace(k)
	if i := strings.Index(k, "("); i > 0 {
		k = k[:i]
	}
	k = strings.ToLower(k)
	k = strings.NewReplacer(" ", "", "-", "", "_", "", "*", "").Replace(k)
	if v, ok := aliasTable[k]; ok {
		return v
	}
	return k
}

// judgeFlag 只按该行自带参考范围判高低：有上限超了算 high，有下限低了算 low。
// 参考范围缺失或项目本身是定性结果（阴性/阳性）时不判定 —— 宁可不报，也不瞎报。
func judgeFlag(v *float64, low, high *float64) string {
	if v == nil {
		return ""
	}
	if high != nil && *v > *high {
		return "high"
	}
	if low != nil && *v < *low {
		return "low"
	}
	return ""
}
