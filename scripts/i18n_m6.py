"""M6：站点端口相关词条（扁平点号键，保留 BOM）。"""
import json, os, collections

BASE = os.path.join(os.path.dirname(__file__), "..", "frontend", "src", "i18n", "locales")

K = {
    "zh-CN": {
        "sites.port": "端口",
        "sites.portHint": "默认 80。每个站点独立端口，浏览器直接访问 http://localhost:端口，无需改 hosts。1-1023 端口需以管理员身份运行对号。",
        "sites.changePort": "修改端口",
        "sites.portPrompt": "新的端口号（1-65535）",
        "sites.portInvalid": "请输入 1-65535 的整数",
        "sites.portUpdated": "端口已改为 {port}",
    },
    "zh-TW": {
        "sites.port": "連接埠",
        "sites.portHint": "預設 80。每個網站獨立連接埠，瀏覽器直接存取 http://localhost:連接埠，無需修改 hosts。1-1023 連接埠需以管理員身分執行對號。",
        "sites.changePort": "修改連接埠",
        "sites.portPrompt": "新的連接埠號（1-65535）",
        "sites.portInvalid": "請輸入 1-65535 的整數",
        "sites.portUpdated": "連接埠已改為 {port}",
    },
    "en": {
        "sites.port": "Port",
        "sites.portHint": "Default 80. Each site listens on its own port — open http://localhost:PORT directly, no hosts edit needed. Ports 1-1023 require running DUIHAO as administrator.",
        "sites.changePort": "Change port",
        "sites.portPrompt": "New port (1-65535)",
        "sites.portInvalid": "Enter an integer between 1 and 65535",
        "sites.portUpdated": "Port changed to {port}",
    },
    "ja": {
        "sites.port": "ポート",
        "sites.portHint": "既定は 80。サイトごとに独立したポートを使うため、http://localhost:ポート で直接開けます（hosts 編集不要）。1-1023 は管理者権限が必要です。",
        "sites.changePort": "ポートを変更",
        "sites.portPrompt": "新しいポート番号（1-65535）",
        "sites.portInvalid": "1〜65535 の整数を入力してください",
        "sites.portUpdated": "ポートを {port} に変更しました",
    },
    "ko": {
        "sites.port": "포트",
        "sites.portHint": "기본 80. 사이트마다 독립 포트를 사용하므로 http://localhost:포트 로 바로 접속할 수 있습니다(hosts 수정 불필요). 1-1023 포트는 관리자 권한이 필요합니다.",
        "sites.changePort": "포트 변경",
        "sites.portPrompt": "새 포트 번호(1-65535)",
        "sites.portInvalid": "1-65535 사이의 정수를 입력하세요",
        "sites.portUpdated": "포트가 {port}(으)로 변경되었습니다",
    },
    "de": {
        "sites.port": "Port",
        "sites.portHint": "Standard 80. Jede Website nutzt einen eigenen Port – http://localhost:PORT direkt öffnen, kein hosts-Eintrag nötig. Ports 1-1023 erfordern Administratorrechte.",
        "sites.changePort": "Port ändern",
        "sites.portPrompt": "Neuer Port (1-65535)",
        "sites.portInvalid": "Bitte eine ganze Zahl zwischen 1 und 65535 eingeben",
        "sites.portUpdated": "Port auf {port} geändert",
    },
    "es": {
        "sites.port": "Puerto",
        "sites.portHint": "Por defecto 80. Cada sitio usa su propio puerto: abre http://localhost:PUERTO directamente, sin editar hosts. Los puertos 1-1023 requieren ejecutar como administrador.",
        "sites.changePort": "Cambiar puerto",
        "sites.portPrompt": "Nuevo puerto (1-65535)",
        "sites.portInvalid": "Introduce un entero entre 1 y 65535",
        "sites.portUpdated": "Puerto cambiado a {port}",
    },
    "fr": {
        "sites.port": "Port",
        "sites.portHint": "80 par défaut. Chaque site utilise son propre port : ouvrez http://localhost:PORT directement, sans modifier hosts. Les ports 1-1023 nécessitent les droits administrateur.",
        "sites.changePort": "Changer le port",
        "sites.portPrompt": "Nouveau port (1-65535)",
        "sites.portInvalid": "Saisissez un entier entre 1 et 65535",
        "sites.portUpdated": "Port changé en {port}",
    },
    "ru": {
        "sites.port": "Порт",
        "sites.portHint": "По умолчанию 80. У каждого сайта свой порт — открывайте http://localhost:ПОРТ напрямую, без правки hosts. Порты 1-1023 требуют прав администратора.",
        "sites.changePort": "Изменить порт",
        "sites.portPrompt": "Новый порт (1-65535)",
        "sites.portInvalid": "Введите целое число от 1 до 65535",
        "sites.portUpdated": "Порт изменён на {port}",
    },
    "pt": {
        "sites.port": "Porta",
        "sites.portHint": "Padrão 80. Cada site usa a própria porta: abra http://localhost:PORTA direto, sem editar hosts. Portas 1-1023 exigem executar como administrador.",
        "sites.changePort": "Alterar porta",
        "sites.portPrompt": "Nova porta (1-65535)",
        "sites.portInvalid": "Informe um inteiro entre 1 e 65535",
        "sites.portUpdated": "Porta alterada para {port}",
    },
    "vi": {
        "sites.port": "Cổng",
        "sites.portHint": "Mặc định 80. Mỗi site dùng cổng riêng — mở http://localhost:CỔNG trực tiếp, không cần sửa hosts. Cổng 1-1023 cần chạy bằng quyền quản trị.",
        "sites.changePort": "Đổi cổng",
        "sites.portPrompt": "Cổng mới (1-65535)",
        "sites.portInvalid": "Nhập số nguyên từ 1 đến 65535",
        "sites.portUpdated": "Đã đổi cổng thành {port}",
    },
}

for loc, kv in K.items():
    p = os.path.abspath(os.path.join(BASE, loc + ".json"))
    raw = open(p, "rb").read()
    bom = raw.startswith(b"\xef\xbb\xbf")
    d = json.loads(raw.decode("utf-8-sig"), object_pairs_hook=collections.OrderedDict)
    d.update(kv)
    text = json.dumps(d, ensure_ascii=False, indent=2)
    open(p, "wb").write((("\ufeff" if bom else "") + text).encode("utf-8"))
    print(f"{loc}: +{len(kv)} keys")
