# -*- coding: utf-8 -*-
"""M8：settings.* 11 语言词条（扁平点号键，勿用嵌套结构）。"""
import json, io, os, collections

BASE = os.path.join(os.path.dirname(__file__), '..', 'frontend', 'src', 'i18n', 'locales')

LOCALES = ['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'de', 'es', 'fr', 'ru', 'pt', 'vi']

KEYS = {
    'zh-CN': {
        'settings.title': '设置',
        'settings.loading': '加载设置…',
        'settings.none': '该插件未声明可视化设置',
        'settings.configFile': '配置文件',
        'settings.fileHint': '直接编辑配置原文；与表单修改相互覆盖，以后保存的为准。',
        'settings.fileSaved': '配置文件已保存',
        'settings.saveFile': '保存文件',
        'settings.confirm': '确认',
        'settings.cancel': '取消',
        'settings.setDefaults': '设置默认',
        'settings.resetConfirm': '恢复全部设置为默认值？（端口不变）',
        'settings.resetDone': '已恢复默认值',
        'settings.saved': '设置已保存并应用',
        'settings.savedRestarted': '设置已保存，实例已重启生效',
    },
    'zh-TW': {
        'settings.title': '設定', 'settings.loading': '載入設定…',
        'settings.none': '此外掛未宣告視覺化設定', 'settings.configFile': '設定檔',
        'settings.fileHint': '直接編輯設定原文；與表單修改相互覆蓋，以後儲存的為準。',
        'settings.fileSaved': '設定檔已儲存', 'settings.saveFile': '儲存檔案',
        'settings.confirm': '確認', 'settings.cancel': '取消', 'settings.setDefaults': '設為預設',
        'settings.resetConfirm': '恢復全部設定為預設值？（連接埠不變）',
        'settings.resetDone': '已恢復預設值', 'settings.saved': '設定已儲存並套用',
        'settings.savedRestarted': '設定已儲存，實例已重啟生效',
    },
    'en': {
        'settings.title': 'Settings', 'settings.loading': 'Loading settings…',
        'settings.none': 'No visual settings declared for this plugin',
        'settings.configFile': 'Config file',
        'settings.fileHint': 'Edit the raw config here; it and the form overwrite each other — last save wins.',
        'settings.fileSaved': 'Config file saved', 'settings.saveFile': 'Save file',
        'settings.confirm': 'Apply', 'settings.cancel': 'Cancel', 'settings.setDefaults': 'Set defaults',
        'settings.resetConfirm': 'Reset all settings to defaults? (ports unchanged)',
        'settings.resetDone': 'Defaults restored', 'settings.saved': 'Settings saved and applied',
        'settings.savedRestarted': 'Settings saved; instance restarted',
    },
    'ja': {
        'settings.title': '設定', 'settings.loading': '設定を読み込み中…',
        'settings.none': 'このプラグインには視覚的設定がありません', 'settings.configFile': '設定ファイル',
        'settings.fileHint': '設定ファイルを直接編集。フォームと相互に上書きされます（後保存優先）。',
        'settings.fileSaved': '設定ファイルを保存しました', 'settings.saveFile': 'ファイル保存',
        'settings.confirm': '適用', 'settings.cancel': 'キャンセル', 'settings.setDefaults': 'デフォルトに戻す',
        'settings.resetConfirm': 'すべての設定をデフォルトに戻しますか？（ポートは変更なし）',
        'settings.resetDone': 'デフォルトに戻しました', 'settings.saved': '設定を保存して適用しました',
        'settings.savedRestarted': '設定を保存し、インスタンスを再起動しました',
    },
    'ko': {
        'settings.title': '설정', 'settings.loading': '설정 로드 중…',
        'settings.none': '이 플러그인에는 시각적 설정이 없습니다', 'settings.configFile': '설정 파일',
        'settings.fileHint': '설정 파일을 직접 편집합니다. 폼과 서로 덮어쓰며 나중에 저장한 값이 우선합니다.',
        'settings.fileSaved': '설정 파일이 저장되었습니다', 'settings.saveFile': '파일 저장',
        'settings.confirm': '적용', 'settings.cancel': '취소', 'settings.setDefaults': '기본값으로',
        'settings.resetConfirm': '모든 설정을 기본값으로 되돌립니까? (포트 유지)',
        'settings.resetDone': '기본값으로 복원되었습니다', 'settings.saved': '설정이 저장 및 적용되었습니다',
        'settings.savedRestarted': '설정 저장 후 인스턴스가 재시작되었습니다',
    },
    'de': {
        'settings.title': 'Einstellungen', 'settings.loading': 'Einstellungen laden…',
        'settings.none': 'Keine visuellen Einstellungen für dieses Plugin',
        'settings.configFile': 'Konfigurationsdatei',
        'settings.fileHint': 'Rohe Konfiguration direkt bearbeiten; überschreibt das Formular gegenseitig — letzte Speicherung gewinnt.',
        'settings.fileSaved': 'Konfigurationsdatei gespeichert', 'settings.saveFile': 'Datei speichern',
        'settings.confirm': 'Anwenden', 'settings.cancel': 'Abbrechen', 'settings.setDefaults': 'Standard setzen',
        'settings.resetConfirm': 'Alle Einstellungen auf Standard zurücksetzen? (Ports bleiben)',
        'settings.resetDone': 'Standardwerte wiederhergestellt', 'settings.saved': 'Einstellungen gespeichert und angewendet',
        'settings.savedRestarted': 'Einstellungen gespeichert; Instanz neu gestartet',
    },
    'es': {
        'settings.title': 'Ajustes', 'settings.loading': 'Cargando ajustes…',
        'settings.none': 'Este plugin no declara ajustes visuales', 'settings.configFile': 'Archivo de configuración',
        'settings.fileHint': 'Edite la configuración directamente; se sobrescriben mutuamente con el formulario — vale el último guardado.',
        'settings.fileSaved': 'Archivo de configuración guardado', 'settings.saveFile': 'Guardar archivo',
        'settings.confirm': 'Aplicar', 'settings.cancel': 'Cancelar', 'settings.setDefaults': 'Valores predeterminados',
        'settings.resetConfirm': '¿Restablecer todos los ajustes? (los puertos no cambian)',
        'settings.resetDone': 'Valores predeterminados restaurados', 'settings.saved': 'Ajustes guardados y aplicados',
        'settings.savedRestarted': 'Ajustes guardados; instancia reiniciada',
    },
    'fr': {
        'settings.title': 'Paramètres', 'settings.loading': 'Chargement des paramètres…',
        'settings.none': "Ce plugin ne déclare pas de paramètres visuels", 'settings.configFile': 'Fichier de configuration',
        'settings.fileHint': 'Modifiez la configuration brute ici ; le formulaire et ce fichier s’écrasent mutuellement — la dernière sauvegarde gagne.',
        'settings.fileSaved': 'Fichier de configuration enregistré', 'settings.saveFile': 'Enregistrer le fichier',
        'settings.confirm': 'Appliquer', 'settings.cancel': 'Annuler', 'settings.setDefaults': 'Valeurs par défaut',
        'settings.resetConfirm': 'Réinitialiser tous les paramètres ? (ports inchangés)',
        'settings.resetDone': 'Valeurs par défaut restaurées', 'settings.saved': 'Paramètres enregistrés et appliqués',
        'settings.savedRestarted': 'Paramètres enregistrés ; instance redémarrée',
    },
    'ru': {
        'settings.title': 'Настройки', 'settings.loading': 'Загрузка настроек…',
        'settings.none': 'Плагин не объявляет визуальных настроек', 'settings.configFile': 'Файл конфигурации',
        'settings.fileHint': 'Редактируйте конфиг напрямую; форма и файл перезаписывают друг друга — действует последняя запись.',
        'settings.fileSaved': 'Файл конфигурации сохранён', 'settings.saveFile': 'Сохранить файл',
        'settings.confirm': 'Применить', 'settings.cancel': 'Отмена', 'settings.setDefaults': 'По умолчанию',
        'settings.resetConfirm': 'Сбросить все настройки к значениям по умолчанию? (порты не меняются)',
        'settings.resetDone': 'Значения по умолчанию восстановлены', 'settings.saved': 'Настройки сохранены и применены',
        'settings.savedRestarted': 'Настройки сохранены; экземпляр перезапущен',
    },
    'pt': {
        'settings.title': 'Configurações', 'settings.loading': 'Carregando configurações…',
        'settings.none': 'Este plugin não declara configurações visuais', 'settings.configFile': 'Arquivo de configuração',
        'settings.fileHint': 'Edite a configuração diretamente; formulário e arquivo se sobrescrevem — vale o último salvo.',
        'settings.fileSaved': 'Arquivo de configuração salvo', 'settings.saveFile': 'Salvar arquivo',
        'settings.confirm': 'Aplicar', 'settings.cancel': 'Cancelar', 'settings.setDefaults': 'Padrões',
        'settings.resetConfirm': 'Restaurar todas as configurações? (portas inalteradas)',
        'settings.resetDone': 'Padrões restaurados', 'settings.saved': 'Configurações salvas e aplicadas',
        'settings.savedRestarted': 'Configurações salvas; instância reiniciada',
    },
    'vi': {
        'settings.title': 'Cài đặt', 'settings.loading': 'Đang tải cài đặt…',
        'settings.none': 'Plugin này không khai báo cài đặt trực quan', 'settings.configFile': 'Tệp cấu hình',
        'settings.fileHint': 'Chỉnh sửa trực tiếp tệp cấu hình; biểu mẫu và tệp ghi đè lẫn nhau — lần lưu sau cùng được ưu tiên.',
        'settings.fileSaved': 'Đã lưu tệp cấu hình', 'settings.saveFile': 'Lưu tệp',
        'settings.confirm': 'Áp dụng', 'settings.cancel': 'Hủy', 'settings.setDefaults': 'Mặc định',
        'settings.resetConfirm': 'Đặt lại tất cả cài đặt về mặc định? (cổng không đổi)',
        'settings.resetDone': 'Đã khôi phục mặc định', 'settings.saved': 'Đã lưu và áp dụng cài đặt',
        'settings.savedRestarted': 'Đã lưu cài đặt; phiên bản đã khởi động lại',
    },
}

# plugins.settings 按钮（复用既有结构：只加一个键）
BTN = {
    'zh-CN': '配置', 'zh-TW': '設定', 'en': 'Config', 'ja': '設定', 'ko': '설정',
    'de': 'Konfig.', 'es': 'Config.', 'fr': 'Config.', 'ru': 'Настройки', 'pt': 'Config.', 'vi': 'Cấu hình',
}

for loc in LOCALES:
    p = os.path.join(BASE, loc + '.json')
    raw = open(p, 'rb').read()
    bom = raw.startswith(b'\xef\xbb\xbf')
    d = json.loads(raw.decode('utf-8-sig'), object_pairs_hook=collections.OrderedDict)
    d['plugins.settings'] = BTN[loc]
    for k, v in KEYS[loc].items():
        d[k] = v
    t = json.dumps(d, ensure_ascii=False, indent=2)
    open(p, 'wb').write((('\ufeff' if bom else '') + t).encode('utf-8'))
    print(loc, 'ok')
print('ALL-DONE')
