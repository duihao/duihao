"""M5：补「数据目录」词条（扁平点号键，保留 BOM）。"""
import json, io, os, collections

BASE = os.path.join(os.path.dirname(__file__), "..", "frontend", "src", "i18n", "locales")

WORDS = {
    "zh-CN": "数据目录",
    "zh-TW": "資料目錄",
    "en": "Data folder",
    "ja": "データフォルダ",
    "ko": "데이터 폴더",
    "de": "Datenordner",
    "es": "Carpeta de datos",
    "fr": "Dossier de données",
    "ru": "Папка данных",
    "pt": "Pasta de dados",
    "vi": "Thư mục dữ liệu",
}

for loc, word in WORDS.items():
    p = os.path.abspath(os.path.join(BASE, loc + ".json"))
    raw = open(p, "rb").read()
    bom = raw.startswith(b"\xef\xbb\xbf")
    d = json.loads(raw.decode("utf-8-sig"), object_pairs_hook=collections.OrderedDict)
    d["plugins.dataDir"] = word
    text = json.dumps(d, ensure_ascii=False, indent=2)
    open(p, "wb").write((("\ufeff" if bom else "") + text).encode("utf-8"))
    print(f"{loc}: plugins.dataDir = {word}")
