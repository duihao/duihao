import 'dart:convert';

import 'package:http/http.dart' as http;

/// 客户端契约（/-api/v1/*）的 Dart 侧封装。
///
/// 认证方式：P1 用 HTTP Basic（与 WebDAV 客户端同一套账号），
/// 手机端把凭据存在 SharedPreferences 里；等 P1-b 的设备令牌做好后，
/// 这里只需把 Authorization 换成 Bearer，其余代码不用动。
class ApiClient {
  ApiClient({required String base, this.user, this.pass, http.Client? client})
      : base = normalizeBase(base),
        _client = client ?? http.Client();

  final String base;
  final String? user;
  final String? pass;
  final http.Client _client;

  /// 用户可能只输 `192.168.1.10:8080`，这里补协议、去尾部斜杠。
  static String normalizeBase(String raw) {
    var s = raw.trim();
    if (s.isEmpty) return s;
    if (!s.startsWith('http://') && !s.startsWith('https://')) {
      s = 'http://$s';
    }
    while (s.endsWith('/')) {
      s = s.substring(0, s.length - 1);
    }
    return s;
  }

  Map<String, String> get headers {
    final h = <String, String>{'Accept': 'application/json'};
    if (user != null && user!.isNotEmpty) {
      final token = base64Encode(utf8.encode('$user:${pass ?? ''}'));
      h['Authorization'] = 'Basic $token';
    }
    return h;
  }

  Uri uri(String path, [Map<String, Object?>? query]) {
    var u = Uri.parse('$base$path');
    if (query != null && query.isNotEmpty) {
      final q = <String, String>{};
      query.forEach((k, v) {
        if (v != null && '$v'.isNotEmpty) q[k] = '$v';
      });
      u = u.replace(queryParameters: {...u.queryParameters, ...q});
    }
    return u;
  }

  /// 拉 JSON，统一解 `{"data": ...}` / `{"error": {...}}` 信封。
  Future<Map<String, dynamic>> getJson(String path,
      [Map<String, Object?>? query]) async {
    final res = await _client
        .get(uri(path, query), headers: headers)
        .timeout(const Duration(seconds: 20));
    Map<String, dynamic> body;
    try {
      body = jsonDecode(utf8.decode(res.bodyBytes)) as Map<String, dynamic>;
    } catch (_) {
      throw ApiException(res.statusCode, 'bad_body',
          '服务端返回的不是 JSON（HTTP ${res.statusCode}），请确认地址指向的是 NAS 端口');
    }
    if (res.statusCode >= 400) {
      final err = body['error'];
      final code = err is Map ? '${err['code'] ?? 'error'}' : 'error';
      final msg = err is Map ? '${err['msg'] ?? ''}' : 'HTTP ${res.statusCode}';
      throw ApiException(res.statusCode, code, msg.isEmpty ? '请求失败' : msg);
    }
    final data = body['data'];
    if (data is Map<String, dynamic>) return data;
    return <String, dynamic>{};
  }

  Future<Me> me() async => Me.fromJson(await getJson('/-api/v1/me'));

  Future<FsPage> listDir(String path, {String? cursor, int limit = 200}) async {
    return FsPage.fromJson(await getJson('/-api/v1/fs/list', {
      'path': path,
      'cursor': cursor,
      'limit': limit,
    }));
  }

  Future<PhotoPage> photos({
    String dir = '/',
    String? cursor,
    int limit = 120,
    int w = 320,
  }) async {
    return PhotoPage.fromJson(await getJson('/-api/v1/photos', {
      'dir': dir,
      'cursor': cursor,
      'limit': limit,
      'w': w,
    }));
  }

  Future<VideoPage> videos({String dir = '', String? cursor}) async {
    return VideoPage.fromJson(await getJson('/-api/v1/videos', {
      'dir': dir,
      'cursor': cursor,
    }));
  }

  Future<VideoPlay> videoPlay(String path) async {
    return VideoPlay.fromJson(await getJson('/-api/v1/videos/play', {
      'path': path,
    }));
  }

  String thumbUrl(String path, {int w = 320}) =>
      uri('/-api/v1/thumb', {'path': path, 'w': w}).toString();

  /// 原始文件地址（预览大图 / 播放视频都直连共享路径，支持 Range）。
  String rawUrl(String path) => uri(path).toString();

  void close() => _client.close();
}

class ApiException implements Exception {
  ApiException(this.status, this.code, this.message);

  final int status;
  final String code;
  final String message;

  /// 客户端要给「该干什么」的指引，而不是把服务端原文砸给用户。
  String get friendly {
    switch (status) {
      case 401:
        return '账号或密码不对（HTTP 401）';
      case 403:
        return message.isEmpty ? '这个账号没有权限（HTTP 403）' : message;
      case 404:
        return message.isEmpty ? '服务端没找到这个接口，可能版本太旧' : message;
      default:
        return message.isEmpty ? '请求失败（HTTP $status）' : message;
    }
  }

  @override
  String toString() => friendly;
}

// ─── 模型：字段名与 apiv1 的 JSON 一一对应，只加不改 ───

class Me {
  Me({
    required this.user,
    required this.role,
    required this.writable,
    required this.version,
    required this.api,
    required this.caps,
  });

  final String user;
  final String role;
  final bool writable;
  final String version;
  final int api;
  final List<String> caps;

  static Me fromJson(Map<String, dynamic> j) => Me(
        user: '${j['user'] ?? ''}',
        role: '${j['role'] ?? ''}',
        writable: j['writable'] == true,
        version: '${j['version'] ?? ''}',
        api: (j['api'] as num?)?.toInt() ?? 0,
        caps: (j['caps'] as List?)?.map((e) => '$e').toList() ?? const [],
      );
}

class FsEntry {
  FsEntry({
    required this.name,
    required this.path,
    required this.dir,
    required this.size,
    required this.mtime,
    required this.ext,
  });

  final String name;
  final String path;
  final bool dir;
  final int size;
  final int mtime;
  final String ext;

  static FsEntry fromJson(Map<String, dynamic> j) => FsEntry(
        name: '${j['name'] ?? ''}',
        path: '${j['path'] ?? '/'}',
        dir: j['dir'] == true,
        size: (j['size'] as num?)?.toInt() ?? 0,
        mtime: (j['mtime'] as num?)?.toInt() ?? 0,
        ext: '${j['ext'] ?? ''}',
      );
}

class FsPage {
  FsPage({
    required this.path,
    required this.parent,
    required this.items,
    required this.next,
    required this.total,
  });

  final String path;
  final String parent;
  final List<FsEntry> items;
  final String next;
  final int total;

  static FsPage fromJson(Map<String, dynamic> j) => FsPage(
        path: '${j['path'] ?? '/'}',
        parent: '${j['parent'] ?? '/'}',
        items: (j['items'] as List?)
                ?.map((e) => FsEntry.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        next: '${j['next'] ?? ''}',
        total: (j['total'] as num?)?.toInt() ?? 0,
      );
}

class PhotoItem {
  PhotoItem({
    required this.name,
    required this.path,
    required this.size,
    required this.mtime,
    required this.thumb,
    required this.kind,
  });

  final String name;
  final String path;
  final int size;
  final int mtime;
  final String thumb;
  final String kind;

  bool get isHeic => kind == 'heic';

  static PhotoItem fromJson(Map<String, dynamic> j) => PhotoItem(
        name: '${j['name'] ?? ''}',
        path: '${j['path'] ?? ''}',
        size: (j['size'] as num?)?.toInt() ?? 0,
        mtime: (j['mtime'] as num?)?.toInt() ?? 0,
        thumb: '${j['thumb'] ?? ''}',
        kind: '${j['kind'] ?? 'image'}',
      );
}

class PhotoMonth {
  PhotoMonth({required this.month, required this.items});

  final String month;
  final List<PhotoItem> items;

  static PhotoMonth fromJson(Map<String, dynamic> j) => PhotoMonth(
        month: '${j['month'] ?? ''}',
        items: (j['items'] as List?)
                ?.map((e) => PhotoItem.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}

class PhotoPage {
  PhotoPage({
    required this.months,
    required this.next,
    required this.total,
    required this.truncated,
  });

  final List<PhotoMonth> months;
  final String next;
  final int total;

  /// 服务端扫到上限了（目录太大），提示用户「只显示最近的一部分」。
  final bool truncated;

  static PhotoPage fromJson(Map<String, dynamic> j) => PhotoPage(
        months: (j['months'] as List?)
                ?.map((e) => PhotoMonth.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        next: '${j['next'] ?? ''}',
        total: (j['total'] as num?)?.toInt() ?? 0,
        truncated: j['truncated'] == true,
      );
}

class VideoItem {
  VideoItem({
    required this.name,
    required this.path,
    required this.size,
    required this.mtime,
    required this.ext,
    required this.playable,
    required this.hint,
  });

  final String name;
  final String path;
  final int size;
  final int mtime;
  final String ext;
  final bool playable;
  final String hint;

  static VideoItem fromJson(Map<String, dynamic> j) => VideoItem(
        name: '${j['name'] ?? ''}',
        path: '${j['path'] ?? ''}',
        size: (j['size'] as num?)?.toInt() ?? 0,
        mtime: (j['mtime'] as num?)?.toInt() ?? 0,
        ext: '${j['ext'] ?? ''}',
        playable: j['playable'] == true,
        hint: '${j['hint'] ?? ''}',
      );
}

class VideoPage {
  VideoPage({
    required this.items,
    required this.next,
    required this.total,
    required this.missing,
    required this.hint,
  });

  final List<VideoItem> items;
  final String next;
  final int total;
  final bool missing;
  final String hint;

  static VideoPage fromJson(Map<String, dynamic> j) => VideoPage(
        items: (j['items'] as List?)
                ?.map((e) => VideoItem.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        next: '${j['next'] ?? ''}',
        total: (j['total'] as num?)?.toInt() ?? 0,
        missing: j['missing'] == true,
        hint: '${j['hint'] ?? ''}',
      );
}

class VideoPlay {
  VideoPlay({
    required this.name,
    required this.path,
    required this.url,
    required this.mime,
    required this.playable,
    required this.hint,
  });

  final String name;
  final String path;
  final String url;
  final String mime;
  final bool playable;
  final String hint;

  static VideoPlay fromJson(Map<String, dynamic> j) => VideoPlay(
        name: '${j['name'] ?? ''}',
        path: '${j['path'] ?? ''}',
        url: '${j['url'] ?? ''}',
        mime: '${j['mime'] ?? ''}',
        playable: j['playable'] == true,
        hint: '${j['hint'] ?? ''}',
      );
}
