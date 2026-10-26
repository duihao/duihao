# -*- coding: utf-8 -*-
"""M12 站点基本配置 i18n 词条（zh-CN / zh-TW / en；其它语言回退 en，见 i18n/index.ts 的 translate）。

对齐 phpStudy「网站」对话框第一屏：域名/第二域名/端口(http|https)/根目录/
创建环境(创建FTP·创建数据库·同步hosts·生产环境)/程序类型 PHP/版本+到期日期/备注。
"""
import json
import os

ZH = {
    'sites.createTitle': '新建站点',
    'sites.secondDomain': '第二域名',
    'sites.secondDomainHint': '可留空；多个用逗号或空格分隔，都会写进 server_name',
    'sites.httpsHint': 'https 用每个站点自签名的证书，浏览器会提示「不安全」，点继续访问即可；改域名会自动重签',
    'sites.createEnv': '创建环境',
    'sites.createFtp': '创建 FTP',
    'sites.createDb': '创建数据库',
    'sites.syncHosts': '同步 hosts',
    'sites.production': '生产环境',
    'sites.envHint': '勾选的项在站点建好后自动创建；不同环境可分开勾选，失败只提示不中断建站。',
    'sites.envUncheckHint': '',
    'sites.envFtpLinked': '已有 FTP 账号：{user}（取消勾选不会删除账号）',
    'sites.envPartial': '站点已保存，但这些环境未完成：{list}',
    'sites.envFtpNoInstance': '没有可用的 FTPServer 实例（先在运行时面板添加）',
    'sites.envDbNoInstance': '没有可用的 MySQL 实例（先在运行时面板添加）',
    'sites.ftpInstance': 'FTP 实例',
    'sites.ftpUser': '账号',
    'sites.ftpPassword': '密码',
    'sites.ftpNoInstance': '未安装 FTPServer',
    'sites.dbInstance': 'MySQL 实例',
    'sites.dbNoInstance': '未安装 MySQL',
    'sites.dbUser': '库用户',
    'sites.dbPassword': '库密码',
    'sites.dbEnvHint': '建库会同时创建同名用户并只授权该库；MySQL 还没设置 root 密码时这一步会失败（先去「运行时 → 管理」设置）。',
    'sites.programType': '程序类型',
    'sites.phpVersion': 'PHP 版本',
    'sites.webServer': 'Web 服务器',
    'sites.productionHint': '生产环境：PHP 错误只写日志，不输出到页面',
}

TW = {
    'sites.createTitle': '新增網站',
    'sites.secondDomain': '第二網域',
    'sites.secondDomainHint': '可留空；多個用逗號或空格分隔，都會寫進 server_name',
    'sites.httpsHint': 'https 使用每個網站自簽名的憑證，瀏覽器會提示「不安全」，點繼續存取即可；換網域會自動重簽',
    'sites.createEnv': '建立環境',
    'sites.createFtp': '建立 FTP',
    'sites.createDb': '建立資料庫',
    'sites.syncHosts': '同步 hosts',
    'sites.production': '生產環境',
    'sites.envHint': '勾選的項目會在網站建立後自動建立；不同環境可分開勾選，失敗只提示不中斷建站。',
    'sites.envUncheckHint': '',
    'sites.envFtpLinked': '已有 FTP 帳號：{user}（取消勾選不會刪除帳號）',
    'sites.envPartial': '網站已儲存，但這些環境未完成：{list}',
    'sites.envFtpNoInstance': '沒有可用的 FTPServer 實例（先在執行時面板新增）',
    'sites.envDbNoInstance': '沒有可用的 MySQL 實例（先在執行時面板新增）',
    'sites.ftpInstance': 'FTP 實例',
    'sites.ftpUser': '帳號',
    'sites.ftpPassword': '密碼',
    'sites.ftpNoInstance': '未安裝 FTPServer',
    'sites.dbInstance': 'MySQL 實例',
    'sites.dbNoInstance': '未安裝 MySQL',
    'sites.dbUser': '資料庫使用者',
    'sites.dbPassword': '資料庫密碼',
    'sites.dbEnvHint': '建庫會同時建立同名使用者並只授權該庫；MySQL 還沒設定 root 密碼時這一步會失敗（先去「執行時 → 管理」設定）。',
    'sites.programType': '程式類型',
    'sites.phpVersion': 'PHP 版本',
    'sites.webServer': 'Web 伺服器',
    'sites.productionHint': '生產環境：PHP 錯誤只寫日誌，不輸出到頁面',
}

EN = {
    'sites.createTitle': 'New site',
    'sites.secondDomain': 'Second domain',
    'sites.secondDomainHint': 'Optional; separate multiple entries with commas or spaces — all go into server_name',
    'sites.httpsHint': 'https uses a self-signed certificate per site. Browsers will warn once; continue to proceed. Certificates are re-issued when domains change.',
    'sites.createEnv': 'Create environment',
    'sites.createFtp': 'Create FTP',
    'sites.createDb': 'Create database',
    'sites.syncHosts': 'Sync hosts',
    'sites.production': 'Production',
    'sites.envHint': 'Checked items are created right after the site. Each can be switched separately; failures are reported without failing the site.',
    'sites.envUncheckHint': '',
    'sites.envFtpLinked': 'FTP account exists: {user} (unchecking will not delete it)',
    'sites.envPartial': 'Site saved, but these environment steps failed: {list}',
    'sites.envFtpNoInstance': 'No FTPServer instance available (add one in the runtime panel)',
    'sites.envDbNoInstance': 'No MySQL instance available (add one in the runtime panel)',
    'sites.ftpInstance': 'FTP instance',
    'sites.ftpUser': 'User',
    'sites.ftpPassword': 'Password',
    'sites.ftpNoInstance': 'FTPServer not installed',
    'sites.dbInstance': 'MySQL instance',
    'sites.dbNoInstance': 'MySQL not installed',
    'sites.dbUser': 'DB user',
    'sites.dbPassword': 'DB password',
    'sites.dbEnvHint': 'Creates the database plus a same-named user with privileges on that database only. Fails until a root password is set (Runtime → Manage).',
    'sites.programType': 'Program type',
    'sites.phpVersion': 'PHP version',
    'sites.webServer': 'Web server',
    'sites.productionHint': 'Production: PHP errors go to the log only, never to the page',
}

BASE = os.path.join(os.path.dirname(__file__), '..', 'frontend', 'src', 'i18n', 'locales')

for name, table in (('zh-CN', ZH), ('zh-TW', TW), ('en', EN)):
    path = os.path.join(BASE, name + '.json')
    with open(path, 'r', encoding='utf-8') as f:
        data = json.load(f)
    # 空串词条不落库：模板里对应位置已删除，避免留无用键
    data.update({k: v for k, v in table.items() if v != ''})
    with open(path, 'w', encoding='utf-8', newline='\n') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')
    print(f'{name}: {len(data)} keys')
