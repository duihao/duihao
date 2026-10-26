# -*- coding: utf-8 -*-
"""M6 i18n：nginx 默认端口 80 + 实例改端口 + 站点端口留空自动。"""
import json, io, os, collections

BASE = os.path.join('frontend', 'src', 'i18n', 'locales')
LOCALES = ['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'de', 'es', 'fr', 'ru', 'pt', 'vi']

T = {
    'zh-CN': {
        'plugins.changePort': '修改端口',
        'plugins.changePortHint': '点击修改端口（默认 80，被占用时自动顺延）',
        'plugins.portPromptMsg': '新的端口号（1-65535）。1-1023 需以管理员身份运行对号。',
        'plugins.portInvalid': '请输入 1-65535 的整数',
        'plugins.portUpdated': '端口已修改',
        'sites.portHint': '留空自动：优先 80，被占用则顺延（也可手填指定）。每个站点独立端口，浏览器直接访问 http://localhost:端口，无需改 hosts。1-1023 端口需以管理员身份运行对号。',
    },
    'zh-TW': {
        'plugins.changePort': '修改連接埠',
        'plugins.changePortHint': '點擊修改連接埠（預設 80，被佔用時自動順延）',
        'plugins.portPromptMsg': '新的連接埠號（1-65535）。1-1023 需以管理員身分執行對號。',
        'plugins.portInvalid': '請輸入 1-65535 的整數',
        'plugins.portUpdated': '連接埠已修改',
        'sites.portHint': '留空自動：優先 80，被佔用則順延（也可手動指定）。每個網站獨立連接埠，瀏覽器直接存取 http://localhost:連接埠，無需改 hosts。1-1023 連接埠需以管理員身分執行對號。',
    },
    'en': {
        'plugins.changePort': 'Change port',
        'plugins.changePortHint': 'Click to change port (default 80, falls back if taken)',
        'plugins.portPromptMsg': 'New port (1-65535). Ports 1-1023 require running Duihao as administrator.',
        'plugins.portInvalid': 'Enter an integer between 1 and 65535',
        'plugins.portUpdated': 'Port updated',
        'sites.portHint': 'Leave empty to auto-pick: 80 first, next free if taken. Each site has its own port — open http://localhost:<port>, no hosts edit needed. Ports 1-1023 need administrator privileges.',
    },
    'ja': {
        'plugins.changePort': 'ポート変更',
        'plugins.changePortHint': 'クリックでポート変更（既定 80、使用中は自動で次へ）',
        'plugins.portPromptMsg': '新しいポート番号（1-65535）。1-1023 は管理者権限が必要です。',
        'plugins.portInvalid': '1〜65535 の整数を入力してください',
        'plugins.portUpdated': 'ポートを変更しました',
        'sites.portHint': '空欄で自動：80 を優先し、使用中なら次の空きポート。各サイト固有のポートで http://localhost:ポート に直接アクセス（hosts 変更不要）。1-1023 は管理者権限が必要です。',
    },
    'ko': {
        'plugins.changePort': '포트 변경',
        'plugins.changePortHint': '클릭하여 포트 변경 (기본 80, 사용 중이면 자동 대체)',
        'plugins.portPromptMsg': '새 포트 번호(1-65535). 1-1023은 관리자 권한이 필요합니다.',
        'plugins.portInvalid': '1-65535 사이의 정수를 입력하세요',
        'plugins.portUpdated': '포트가 변경되었습니다',
        'sites.portHint': '비워두면 자동: 80 우선, 사용 중이면 다음 빈 포트. 사이트마다 고유 포트로 http://localhost:포트 에 바로 접속(hosts 수정 불필요). 1-1023은 관리자 권한 필요.',
    },
    'de': {
        'plugins.changePort': 'Port ändern',
        'plugins.changePortHint': 'Klicken zum Ändern (Standard 80, bei Belegung automatisch ausweichen)',
        'plugins.portPromptMsg': 'Neuer Port (1-65535). Ports 1-1023 erfordern Administratorrechte.',
        'plugins.portInvalid': 'Ganzzahl zwischen 1 und 65535 eingeben',
        'plugins.portUpdated': 'Port geändert',
        'sites.portHint': 'Leer lassen = automatisch: zuerst 80, sonst nächster freier Port. Jede Site hat einen eigenen Port — http://localhost:Port ohne hosts-Änderung. Ports 1-1023 benötigen Administratorrechte.',
    },
    'es': {
        'plugins.changePort': 'Cambiar puerto',
        'plugins.changePortHint': 'Clic para cambiar el puerto (por defecto 80, se desplaza si está ocupado)',
        'plugins.portPromptMsg': 'Nuevo puerto (1-65535). Los puertos 1-1023 requieren ejecutar como administrador.',
        'plugins.portInvalid': 'Introduce un entero entre 1 y 65535',
        'plugins.portUpdated': 'Puerto actualizado',
        'sites.portHint': 'Vacío = automático: primero 80, si está ocupado el siguiente libre. Cada sitio tiene su propio puerto — abre http://localhost:puerto sin tocar hosts. Los puertos 1-1023 requieren administrador.',
    },
    'fr': {
        'plugins.changePort': 'Changer le port',
        'plugins.changePortHint': 'Cliquez pour changer le port (80 par défaut, reporté si occupé)',
        'plugins.portPromptMsg': 'Nouveau port (1-65535). Les ports 1-1023 nécessitent les droits administrateur.',
        'plugins.portInvalid': 'Saisissez un entier entre 1 et 65535',
        'plugins.portUpdated': 'Port modifié',
        'sites.portHint': "Vide = automatique : 80 d'abord, sinon le suivant libre. Chaque site a son propre port — ouvrez http://localhost:port sans modifier hosts. Les ports 1-1023 nécessitent les droits administrateur.",
    },
    'ru': {
        'plugins.changePort': 'Изменить порт',
        'plugins.changePortHint': 'Нажмите, чтобы изменить порт (по умолчанию 80, при занятости — следующий свободный)',
        'plugins.portPromptMsg': 'Новый порт (1-65535). Для портов 1-1023 нужны права администратора.',
        'plugins.portInvalid': 'Введите целое число от 1 до 65535',
        'plugins.portUpdated': 'Порт изменён',
        'sites.portHint': 'Пусто = автоматически: сначала 80, если занят — следующий свободный. У каждого сайта свой порт — открывайте http://localhost:порт без правки hosts. Порты 1-1023 требуют прав администратора.',
    },
    'pt': {
        'plugins.changePort': 'Alterar porta',
        'plugins.changePortHint': 'Clique para alterar a porta (padrão 80, avança se ocupada)',
        'plugins.portPromptMsg': 'Nova porta (1-65535). Portas 1-1023 exigem executar como administrador.',
        'plugins.portInvalid': 'Informe um inteiro entre 1 e 65535',
        'plugins.portUpdated': 'Porta alterada',
        'sites.portHint': 'Vazio = automático: 80 primeiro, senão a próxima livre. Cada site tem sua porta — acesse http://localhost:porta sem editar hosts. Portas 1-1023 exigem administrador.',
    },
    'vi': {
        'plugins.changePort': 'Đổi cổng',
        'plugins.changePortHint': 'Nhấp để đổi cổng (mặc định 80, tự động nhảy nếu bị chiếm)',
        'plugins.portPromptMsg': 'Cổng mới (1-65535). Cổng 1-1023 cần chạy bằng quyền quản trị.',
        'plugins.portInvalid': 'Nhập số nguyên từ 1 đến 65535',
        'plugins.portUpdated': 'Đã đổi cổng',
        'sites.portHint': 'Để trống = tự động: ưu tiên 80, nếu bị chiếm thì cổng trống kế tiếp. Mỗi site một cổng riêng — mở http://localhost:cổng, không cần sửa hosts. Cổng 1-1023 cần quyền quản trị.',
    },
}

for loc in LOCALES:
    p = os.path.join(BASE, loc + '.json')
    raw = open(p, 'rb').read()
    bom = raw.startswith(b'\xef\xbb\xbf')
    d = json.loads(raw.decode('utf-8-sig'), object_pairs_hook=collections.OrderedDict)
    added = 0
    for k, v in T[loc].items():
        if k not in d:
            d[k] = v
            added += 1
        else:
            d[k] = v
    t = json.dumps(d, ensure_ascii=False, indent=2)
    open(p, 'wb').write((('\ufeff' if bom else '') + t).encode('utf-8'))
    print(f'{loc}: {added} new, {len(T[loc])} total set')
