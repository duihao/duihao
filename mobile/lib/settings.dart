import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';

/// 本地保存的接入信息。
///
/// 为什么先用账号密码：P1 阶段服务端还没有设备令牌（那是 P1-b），
/// Basic 认证与 WebDAV 客户端同一套，拿来即用。等令牌好了，这里换成
/// token + deviceId 即可，界面不动。
class AppSettings {
  AppSettings({this.base = '', this.user = '', this.pass = ''});

  String base;
  String user;
  String pass;

  bool get configured => base.trim().isNotEmpty && user.trim().isNotEmpty;

  ApiClient client() =>
      ApiClient(base: ApiClient.normalizeBase(base), user: user, pass: pass);

  static const _kBase = 'dh.base';
  static const _kUser = 'dh.user';
  static const _kPass = 'dh.pass';

  static Future<AppSettings> load() async {
    final sp = await SharedPreferences.getInstance();
    return AppSettings(
      base: sp.getString(_kBase) ?? '',
      user: sp.getString(_kUser) ?? '',
      pass: sp.getString(_kPass) ?? '',
    );
  }

  Future<void> save() async {
    final sp = await SharedPreferences.getInstance();
    await sp.setString(_kBase, ApiClient.normalizeBase(base));
    await sp.setString(_kUser, user.trim());
    await sp.setString(_kPass, pass);
    base = ApiClient.normalizeBase(base);
    user = user.trim();
  }

  Future<void> clear() async {
    final sp = await SharedPreferences.getInstance();
    await sp.remove(_kBase);
    await sp.remove(_kUser);
    await sp.remove(_kPass);
    base = '';
    user = '';
    pass = '';
  }
}
