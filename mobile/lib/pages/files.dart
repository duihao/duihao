import 'package:flutter/material.dart';

import '../api.dart';
import '../theme.dart';
import 'photos.dart';

/// 文件浏览：就是一个「能进能出」的目录列表。
/// P1 只读；上传/删除等写操作等 P2（要设备令牌 + 权限判定）。
class FilesPage extends StatefulWidget {
  const FilesPage({super.key, required this.api, this.start = '/'});

  final ApiClient api;
  final String start;

  @override
  State<FilesPage> createState() => _FilesPageState();
}

class _FilesPageState extends State<FilesPage> {
  late String _path = widget.start;
  List<FsEntry> _items = const [];
  String _parent = '/';
  bool _loading = false;
  String? _err;

  @override
  void initState() {
    super.initState();
    _load(_path);
  }

  Future<void> _load(String path) async {
    setState(() {
      _loading = true;
      _err = null;
    });
    try {
      final page = await widget.api.listDir(path);
      if (!mounted) return;
      setState(() {
        _path = page.path;
        _parent = page.parent;
        _items = page.items;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _err = e.friendly);
    } catch (e) {
      if (mounted) setState(() => _err = '打不开这个目录：$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  IconData _iconFor(FsEntry e) {
    if (e.dir) return Icons.folder_rounded;
    switch (e.ext) {
      case 'jpg':
      case 'jpeg':
      case 'png':
      case 'webp':
      case 'gif':
      case 'heic':
        return Icons.image_outlined;
      case 'mp4':
      case 'mkv':
      case 'mov':
      case 'avi':
        return Icons.movie_outlined;
      case 'mp3':
      case 'flac':
      case 'm4a':
      case 'wav':
        return Icons.music_note_outlined;
      case 'pdf':
      case 'doc':
      case 'docx':
      case 'xls':
      case 'xlsx':
      case 'txt':
      case 'md':
        return Icons.description_outlined;
      case 'zip':
      case 'rar':
      case '7z':
        return Icons.folder_zip_outlined;
    }
    return Icons.insert_drive_file_outlined;
  }

  bool _isImage(FsEntry e) => const [
        'jpg',
        'jpeg',
        'png',
        'webp',
        'gif',
        'heic',
        'heif',
        'bmp',
      ].contains(e.ext);

  Future<void> _tap(FsEntry e) async {
    if (e.dir) {
      await _load(e.path);
      return;
    }
    if (_isImage(e)) {
      final item = PhotoItem(
        name: e.name,
        path: e.path,
        size: e.size,
        mtime: e.mtime,
        thumb: widget.api.thumbUrl(e.path),
        kind: (e.ext == 'heic' || e.ext == 'heif') ? 'heic' : 'image',
      );
      if (!mounted) return;
      await Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => PhotoViewPage(item: item, api: widget.api),
        ),
      );
      return;
    }
    if (!mounted) return;
    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(e.name, style: const TextStyle(fontSize: 15)),
        content: Text(
          '${e.ext.isEmpty ? '文件' : e.ext.toUpperCase()} · ${humanSize(e.size)}\n'
          '${humanDate(e.mtime)}',
          style: const TextStyle(fontSize: 13, height: 1.7),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx), child: const Text('关闭')),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final atRoot = _path == '/';
    return PopScope(
      canPop: atRoot,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop && !atRoot) _load(_parent);
      },
      child: Scaffold(
        appBar: AppBar(
          leading: IconButton(
            icon: Icon(atRoot ? Icons.close : Icons.arrow_back),
            onPressed: () {
              if (atRoot) {
                Navigator.of(context).maybePop();
              } else {
                _load(_parent);
              }
            },
          ),
          title: Text(_path, style: const TextStyle(fontSize: 13.5)),
          actions: [
            IconButton(
              tooltip: '刷新',
              onPressed: _loading ? null : () => _load(_path),
              icon: const Icon(Icons.refresh, size: 20),
            ),
          ],
        ),
        body: RefreshIndicator(
          onRefresh: () => _load(_path),
          child: ListView.separated(
            padding: const EdgeInsets.only(bottom: 24),
            itemCount: _items.length + (_err != null ? 1 : 0),
            separatorBuilder: (_, __) =>
                const Divider(height: 1, color: DhColors.line),
            itemBuilder: (ctx, i) {
              if (_err != null && i == 0) {
                return Padding(
                  padding: const EdgeInsets.all(16),
                  child: Text(_err!,
                      style: const TextStyle(color: DhColors.danger)),
                );
              }
              final e = _items[_err != null ? i - 1 : i];
              return ListTile(
                onTap: () => _tap(e),
                leading: Icon(
                  _iconFor(e),
                  color: e.dir ? const Color(0xFF60A5FA) : DhColors.tx2,
                ),
                title: Text(e.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 14)),
                subtitle: e.dir
                    ? null
                    : Text('${humanSize(e.size)} · ${humanDate(e.mtime)}',
                        style: const TextStyle(
                            fontSize: 11.5, color: DhColors.mut)),
                trailing: e.dir
                    ? const Icon(Icons.chevron_right,
                        size: 18, color: DhColors.mut)
                    : null,
              );
            },
          ),
        ),
      ),
    );
  }
}
