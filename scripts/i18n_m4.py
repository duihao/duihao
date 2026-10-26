import io, json, os, collections

LOCALES = ['zh-CN','zh-TW','en','ja','ko','de','es','fr','ru','pt','vi']

# key -> [zh-CN, zh-TW, en, ja, ko, de, es, fr, ru, pt, vi]
KEYS = {
 'sidebar.sites': ['网站','網站','Sites','サイト','사이트','Websites','Sitios','Sites','Сайты','Sites','Trang web'],

 'plugins.runtimeTitle': ['运行时','執行環境','Runtime','ランタイム','런타임','Laufzeitumgebungen','Entornos','Environnements','Среды выполнения','Ambientes','Môi trường'],
 'plugins.runtimeHint': ['本地环境','本機環境','Local environment','ローカル環境','로컬 환경','Lokale Umgebung','Entorno local','Environnement local','Локальное окружение','Ambiente local','Môi trường cục bộ'],
 'plugins.install': ['安装','安裝','Install','インストール','설치','Installieren','Instalar','Installer','Установить','Instalar','Cài đặt'],
 'plugins.running': ['运行中','執行中','Running','実行中','실행 중','Läuft','En ejecución','En cours','Работает','Em execução','Đang chạy'],
 'plugins.stopped': ['已停止','已停止','Stopped','停止済み','중지됨','Gestoppt','Detenido','Arrêté','Остановлен','Parado','Đã dừng'],
 'plugins.stateStarting': ['启动中','啟動中','Starting','起動中','시작 중','Startet','Iniciando','Démarrage','Запуск','Iniciando','Đang khởi động'],
 'plugins.stateStopping': ['停止中','停止中','Stopping','停止中','중지 중','Stoppt','Deteniendo','Arrêt en cours','Остановка','Parando','Đang dừng'],
 'plugins.stateError': ['异常','異常','Error','異常','오류','Fehler','Error','Erreur','Ошибка','Erro','Lỗi'],
 'plugins.copyPort': ['复制端口','複製連接埠','Copy port','ポートをコピー','포트 복사','Port kopieren','Copiar puerto','Copier le port','Копировать порт','Copiar porta','Sao chép cổng'],
 'plugins.copied': ['已复制','已複製','Copied','コピーしました','복사됨','Kopiert','Copiado','Copié','Скопировано','Copiado','Đã sao chép'],
 'plugins.removeInstance': ['卸载实例','卸載實例','Remove instance','インスタンスを削除','인스턴스 제거','Instanz entfernen','Quitar instancia',"Supprimer l'instance",'Удалить экземпляр','Remover instância','Gỡ bỏ phiên'],
 'plugins.toolsNote': [
   '工具类（Composer、WP-CLI、mysqldump）不占主面板，由站点操作按需调用。',
   '工具類（Composer、WP-CLI、mysqldump）不佔主面板，由網站操作按需呼叫。',
   'Tools such as Composer, WP-CLI and mysqldump are not listed here — site actions call them on demand.',
   'Composer・WP-CLI・mysqldump などのツールはここには表示されず、サイト操作から必要時に呼び出されます。',
   'Composer, WP-CLI, mysqldump 같은 도구는 여기 표시되지 않으며 사이트 작업에서 필요할 때 호출됩니다.',
   'Werkzeuge wie Composer, WP-CLI und mysqldump erscheinen hier nicht – Website-Aktionen rufen sie bei Bedarf auf.',
   'Herramientas como Composer, WP-CLI y mysqldump no se muestran aquí: las acciones del sitio las invocan cuando hacen falta.',
   "Les outils comme Composer, WP-CLI et mysqldump n'apparaissent pas ici : les actions de site les appellent à la demande.",
   'Инструменты вроде Composer, WP-CLI и mysqldump здесь не показаны — действия сайта вызывают их по необходимости.',
   'Ferramentas como Composer, WP-CLI e mysqldump não aparecem aqui — as ações do site as chamam quando necessário.',
   'Các công cụ như Composer, WP-CLI, mysqldump không hiển thị ở đây — thao tác trang sẽ gọi khi cần.',
 ],

 'sites.title': ['网站','網站','Websites','サイト','사이트','Websites','Sitios','Sites','Сайты','Sites','Trang web'],
 'sites.new': ['新建站点','新增網站','New site','新規サイト','사이트 추가','Neue Website','Nuevo sitio','Nouveau site','Новый сайт','Novo site','Trang mới'],
 'sites.newTitle': ['新建站点','新增網站','New site','新規サイト','새 사이트','Neue Website','Nuevo sitio','Nouveau site','Новый сайт','Novo site','Tạo trang mới'],
 'sites.empty': ['还没有站点，点「新建站点」开始。','還沒有網站，點「新增網站」開始。','No sites yet — click “New site” to start.','サイトがありません。「新規サイト」から作成してください。','사이트가 없습니다. "사이트 추가"로 시작하세요.','Noch keine Websites – „Neue Website“ klicken.','Aún no hay sitios: pulsa «Nuevo sitio».','Aucun site pour l’instant — cliquez sur « Nouveau site ».','Сайтов пока нет — нажмите «Новый сайт».','Nenhum site ainda — clique em “Novo site”.','Chưa có trang nào — bấm “Trang mới”.'],
 'sites.name': ['名称','名稱','Name','名前','이름','Name','Nombre','Nom','Имя','Nome','Tên'],
 'sites.namePlaceholder': ['可选，默认用域名','選填，預設用網域','Optional, defaults to domain','任意、既定はドメイン','선택, 기본은 도메인','Optional, Standard ist die Domain','Opcional, por defecto el dominio','Facultatif, le domaine par défaut','Необязательно, по умолчанию домен','Opcional, padrão é o domínio','Tùy chọn, mặc định là tên miền'],
 'sites.domains': ['域名','網域','Domains','ドメイン','도메인','Domains','Dominios','Domaines','Домены','Domínios','Tên miền'],
 'sites.domainsHint': ['多个用逗号分隔；留空自动生成 <站点 id>.test','多個用逗號分隔；留空自動產生 <網站 id>.test','Comma-separated; empty generates <site id>.test','カンマ区切り。空なら <サイト id>.test を自動生成','쉼표로 구분. 비우면 <사이트 id>.test 자동 생성','Kommagetrennt; leer erzeugt <Site-ID>.test','Separados por comas; vacío genera <id>.test','Séparés par des virgules ; vide génère <id>.test','Через запятую; пусто → <id>.test','Separados por vírgula; vazio gera <id>.test','Cách nhau bằng dấu phẩy; trống sẽ tạo <id>.test'],
 'sites.root': ['根目录','根目錄','Root folder','ドキュメントルート','루트 폴더','Stammverzeichnis','Carpeta raíz','Racine','Корневой каталог','Pasta raiz','Thư mục gốc'],
 'sites.rootHint': ['绝对路径，不存在会自动创建','絕對路徑，不存在會自動建立','Absolute path; created if missing','絶対パス。無ければ自動作成','절대 경로, 없으면 자동 생성','Absoluter Pfad; wird bei Bedarf erstellt','Ruta absoluta; se crea si falta','Chemin absolu ; créé si absent','Абсолютный путь; создаётся при отсутствии','Caminho absoluto; criado se faltar','Đường dẫn tuyệt đối; tự tạo nếu chưa có'],
 'sites.stack': ['服务栈','服務組合','Stack','構成','구성','Stack','Pila','Pile','Стек','Pilha','Ngăn xếp'],
 'sites.web': ['Web 服务器','Web 伺服器','Web server','Web サーバー','웹 서버','Webserver','Servidor web','Serveur web','Веб-сервер','Servidor web','Máy chủ web'],
 'sites.php': ['PHP 运行时','PHP 執行環境','PHP runtime','PHP ランタイム','PHP 런타임','PHP-Laufzeit','Entorno PHP','Environnement PHP','Среда PHP','Ambiente PHP','Môi trường PHP'],
 'sites.phpNone': ['静态站点（不用 PHP）','靜態網站（不用 PHP）','Static site (no PHP)','静的サイト（PHP なし）','정적 사이트(PHP 없음)','Statische Website (kein PHP)','Sitio estático (sin PHP)','Site statique (sans PHP)','Статический сайт (без PHP)','Site estático (sem PHP)','Trang tĩnh (không PHP)'],
 'sites.database': ['数据库','資料庫','Database','データベース','데이터베이스','Datenbank','Base de datos','Base de données','База данных','Banco de dados','Cơ sở dữ liệu'],
 'sites.dbNone': ['不使用','不使用','None','使用しない','사용 안 함','Keine','Ninguna','Aucune','Нет','Nenhum','Không dùng'],
 'sites.dbName': ['库名','資料庫名稱','Database name','DB 名','DB 이름','Datenbankname','Nombre de la base','Nom de la base','Имя базы','Nome do banco','Tên CSDL'],
 'sites.create': ['创建','建立','Create','作成','만들기','Erstellen','Crear','Créer','Создать','Criar','Tạo'],
 'sites.cancel': ['取消','取消','Cancel','キャンセル','취소','Abbrechen','Cancelar','Annuler','Отмена','Cancelar','Hủy'],
 'sites.created': ['站点已创建','網站已建立','Site created','サイトを作成しました','사이트 생성됨','Website erstellt','Sitio creado','Site créé','Сайт создан','Site criado','Đã tạo trang'],
 'sites.open': ['打开','開啟','Open','開く','열기','Öffnen','Abrir','Ouvrir','Открыть','Abrir','Mở'],
 'sites.start': ['启动','啟動','Start','起動','시작','Starten','Iniciar','Démarrer','Запустить','Iniciar','Khởi động'],
 'sites.stop': ['停止','停止','Stop','停止','중지','Stoppen','Detener','Arrêter','Остановить','Parar','Dừng'],
 'sites.logs': ['日志','日誌','Logs','ログ','로그','Protokolle','Registros','Journaux','Логи','Logs','Nhật ký'],
 'sites.delete': ['删除站点','刪除網站','Delete site','サイトを削除','사이트 삭제','Website löschen','Eliminar sitio','Supprimer le site','Удалить сайт','Excluir site','Xóa trang'],
 'sites.deleteConfirm': ['删除站点 {id}？根目录里的文件不会被删除。','刪除網站 {id}？根目錄裡的檔案不會被刪除。','Delete site {id}? Files in the root folder are kept.','サイト {id} を削除しますか？ルート内のファイルは残ります。','{id} 사이트를 삭제할까요? 루트 폴더 파일은 유지됩니다.','Website {id} löschen? Dateien im Stammverzeichnis bleiben erhalten.','¿Eliminar el sitio {id}? Los archivos de la carpeta raíz se conservan.',"Supprimer le site {id} ? Les fichiers de la racine sont conservés.",'Удалить сайт {id}? Файлы в корневом каталоге сохранятся.','Excluir o site {id}? Os arquivos da pasta raiz são mantidos.','Xóa trang {id}? Tệp trong thư mục gốc được giữ lại.'],
 'sites.rebuild': ['重新生成配置','重新產生設定','Regenerate config','設定を再生成','설정 재생성','Konfiguration neu erzeugen','Regenerar configuración','Régénérer la configuration','Пересоздать конфигурацию','Regenerar configuração','Tạo lại cấu hình'],
 'sites.rebuilt': ['配置已重新生成','設定已重新產生','Config regenerated','設定を再生成しました','설정이 재생성됨','Konfiguration neu erzeugt','Configuración regenerada','Configuration régénérée','Конфигурация пересоздана','Configuração regenerada','Đã tạo lại cấu hình'],
 'sites.hostsReady': ['hosts 已绑定','hosts 已綁定','hosts bound','hosts 設定済み','hosts 설정됨','hosts gesetzt','hosts listo','hosts prêt','hosts задан','hosts pronto','Đã gán hosts'],
 'sites.hostsMissing': ['hosts 未绑定','hosts 未綁定','hosts missing','hosts 未設定','hosts 미설정','hosts fehlt','hosts ausente','hosts manquant','hosts не задан','hosts ausente','Chưa gán hosts'],
 'sites.hostsTitle': ['hosts 绑定','hosts 綁定','hosts binding','hosts 設定','hosts 설정','hosts-Zuordnung','Enlace hosts','Liaison hosts','Привязка hosts','Vinculação hosts','Gán hosts'],
 'sites.hostsHint': ['把下面这行加入系统 hosts 文件，域名才能在本机访问。','把下面這行加入系統 hosts 檔案，網域才能在本機存取。','Add this line to the system hosts file so the domain resolves locally.','この行を hosts ファイルに追加すると、ドメインがローカルで解決されます。','이 줄을 hosts 파일에 추가하면 도메인이 로컬에서 해석됩니다.','Diese Zeile in die hosts-Datei eintragen, damit die Domain lokal auflöst.','Añade esta línea al archivo hosts para que el dominio resuelva en local.','Ajoutez cette ligne au fichier hosts pour résoudre le domaine localement.','Добавьте строку в файл hosts, чтобы домен разрешался локально.','Adicione esta linha ao arquivo hosts para o domínio resolver localmente.','Thêm dòng này vào tệp hosts để tên miền phân giải cục bộ.'],
 'sites.copy': ['复制','複製','Copy','コピー','복사','Kopieren','Copiar','Copier','Копировать','Copiar','Sao chép'],
 'sites.copied': ['已复制','已複製','Copied','コピーしました','복사됨','Kopiert','Copiado','Copié','Скопировано','Copiado','Đã sao chép'],
 'sites.writeHosts': ['一键写入（UAC）','一鍵寫入（UAC）','Write automatically (UAC)','自動書き込み（UAC）','자동 쓰기(UAC)','Automatisch schreiben (UAC)','Escribir automáticamente (UAC)','Écrire automatiquement (UAC)','Записать автоматически (UAC)','Gravar automaticamente (UAC)','Ghi tự động (UAC)'],
 'sites.hostsDone': ['hosts 已写入','hosts 已寫入','hosts updated','hosts を更新しました','hosts 기록됨','hosts aktualisiert','hosts actualizado','hosts mis à jour','hosts обновлён','hosts atualizado','Đã ghi hosts'],
 'sites.needWeb': ['需要先安装 Nginx 运行时（左侧「运行时」面板）','需要先安裝 Nginx 執行環境（左側「執行環境」面板）','Install an Nginx runtime first (Runtime panel on the left)','先に Nginx ランタイムをインストールしてください（左の「ランタイム」パネル）','먼저 Nginx 런타임을 설치하세요(왼쪽 "런타임" 패널)','Zuerst eine Nginx-Laufzeit installieren (Panel „Laufzeitumgebungen“ links)','Instala primero un entorno Nginx (panel «Entornos» a la izquierda)','Installez d’abord un environnement Nginx (panneau « Environnements » à gauche)','Сначала установите среду Nginx (панель «Среды выполнения» слева)','Instale primeiro um ambiente Nginx (painel “Ambientes” à esquerda)','Hãy cài môi trường Nginx trước (bảng “Môi trường” bên trái)'],
}

base = r'E:/workbudy/duihao/app/frontend/src/i18n/locales'
for idx, loc in enumerate(LOCALES):
    p = os.path.join(base, loc + '.json')
    raw = open(p, 'rb').read()
    has_bom = raw.startswith(b'\xef\xbb\xbf')
    d = json.loads(raw.decode('utf-8-sig'), object_pairs_hook=collections.OrderedDict)
    added = 0
    for k, vals in KEYS.items():
        if k not in d:
            d[k] = vals[idx]
            added += 1
    text = json.dumps(d, ensure_ascii=False, indent=2)
    out = ('\ufeff' + text if has_bom else text).encode('utf-8')
    open(p, 'wb').write(out)
    print(loc, 'added', added, 'total', len(d))
