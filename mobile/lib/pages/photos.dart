import 'package:flutter/material.dart';

import '../api.dart';
import '../theme.dart';
import '../widgets.dart';

/// 照片时间线：按月份分组的三列网格。
///
/// 只拉缩略图（w=320），点开才请求原图 —— 4G 下几千张照片的目录也敢打开。
class PhotosPage extends StatefulWidget {
  const PhotosPage({super.key, required this.api, this.dir = '/'});

  final ApiClient api;
  final String dir;

  @override
  State<PhotosPage> createState() => _PhotosPageState();
}

class _PhotosPageState extends State<PhotosPage> {
  final ScrollController _sc = ScrollController();

  final List<PhotoMonth> _months = [];
  String _next = '';
  int _total = 0;
  bool _loading = false;
  String? _err;
  bool _truncated = false;

  @override
  void initState() {
    super.initState();
    _load(reset: true);
    _sc.addListener(_onScroll);
  }

  @override
  void dispose() {
    _sc.removeListener(_onScroll);
    _sc.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_sc.hasClients) return;
    if (_sc.position.pixels >= _sc.position.maxScrollExtent - 600) {
      _load();
    }
  }

  Future<void> _load({bool reset = false}) async {
    if (_loading) return;
    if (!reset && _next.isEmpty) return;
    setState(() {
      _loading = true;
      _err = null;
      if (reset) {
        _months.clear();
        _next = '';
      }
    });
    try {
      final page = await widget.api.photos(
        dir: widget.dir,
        cursor: reset ? null : _next,
        limit: 120,
      );
      if (!mounted) return;
      setState(() {
        _months.addAll(page.months);
        _next = page.next;
        _total = page.total;
        _truncated = _truncated || page.truncated;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _err = e.friendly);
    } catch (e) {
      if (mounted) setState(() => _err = '加载失败：$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final all = <PhotoItem>[for (final m in _months) ...m.items];
    return Scaffold(
      appBar: AppBar(
        title: const Text('照片'),
        actions: [
          if (_total > 0)
            Center(
              child: Padding(
                padding: const EdgeInsets.only(right: 14),
                child: Text('$_total 张',
                    style: const TextStyle(fontSize: 12, color: DhColors.mut)),
              ),
            ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => _load(reset: true),
        child: CustomScrollView(
          controller: _sc,
          slivers: [
            if (_err != null)
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Text(_err!,
                      style:
                          const TextStyle(color: DhColors.danger, fontSize: 13)),
                ),
              ),
            if (all.isEmpty && !_loading && _err == null)
              const SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Text('这里还没有照片',
                      style: TextStyle(color: DhColors.mut)),
                ),
              ),
            for (final m in _months) ...[
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(14, 16, 14, 8),
                  child: Row(
                    children: [
                      Text(m.month,
                          style: const TextStyle(
                              fontSize: 14, fontWeight: FontWeight.w600)),
                      const SizedBox(width: 8),
                      Text('${m.items.length}',
                          style: const TextStyle(
                              fontSize: 11.5, color: DhColors.mut)),
                    ],
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: 2),
                sliver: SliverGrid(
                  gridDelegate:
                      const SliverGridDelegateWithFixedCrossAxisCount(
                    crossAxisCount: 3,
                    mainAxisSpacing: 2,
                    crossAxisSpacing: 2,
                  ),
                  delegate: SliverChildBuilderDelegate(
                    (ctx, i) {
                      return _PhotoCell(
                        item: m.items[i],
                        api: widget.api,
                        siblings: all,
                      );
                    },
                    childCount: m.items.length,
                  ),
                ),
              ),
            ],
            if (_loading)
              const SliverToBoxAdapter(
                child: Padding(
                  padding: EdgeInsets.symmetric(vertical: 26),
                  child: Center(
                    child: SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  ),
                ),
              ),
            if (_truncated)
              const SliverToBoxAdapter(
                child: Padding(
                  padding: EdgeInsets.fromLTRB(16, 10, 16, 30),
                  child: Text('照片太多，只显示最近的一部分',
                      style: TextStyle(fontSize: 11.5, color: DhColors.mut)),
                ),
              ),
            const SliverToBoxAdapter(child: SizedBox(height: 26)),
          ],
        ),
      ),
    );
  }
}

class _PhotoCell extends StatelessWidget {
  const _PhotoCell({
    required this.item,
    required this.api,
    required this.siblings,
  });

  final PhotoItem item;
  final ApiClient api;
  final List<PhotoItem> siblings;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) =>
              PhotoViewPage(item: item, api: api, siblings: siblings),
        ),
      ),
      child: item.isHeic
          ? const ThumbFallback(label: 'HEIC')
          : Image.network(
              api.thumbUrl(item.path),
              headers: api.headers,
              fit: BoxFit.cover,
              // 缩略图很小，直接按屏幕像素解码，省内存
              cacheWidth: 360,
              errorBuilder: (_, __, ___) => const ThumbFallback(),
            ),
    );
  }
}

/// 大图查看：原图 + 双指缩放 + 上一张/下一张。
class PhotoViewPage extends StatefulWidget {
  const PhotoViewPage({
    super.key,
    required this.item,
    required this.api,
    this.siblings = const [],
  });

  final PhotoItem item;
  final ApiClient api;
  final List<PhotoItem> siblings;

  @override
  State<PhotoViewPage> createState() => _PhotoViewPageState();
}

class _PhotoViewPageState extends State<PhotoViewPage> {
  late PhotoItem _cur = widget.item;

  @override
  Widget build(BuildContext context) {
    final list = widget.siblings.isEmpty ? [widget.item] : widget.siblings;
    final idx = list.indexWhere((e) => e.path == _cur.path);
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        title: Text(_cur.name,
            style: const TextStyle(fontSize: 14),
            overflow: TextOverflow.ellipsis),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 14),
            child: Center(
              child: Text(
                '${humanSize(_cur.size)} · ${humanDate(_cur.mtime)}',
                style: const TextStyle(fontSize: 11, color: DhColors.mut),
              ),
            ),
          ),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: InteractiveViewer(
              maxScale: 4,
              child: Center(
                child: _cur.isHeic
                    ? const Padding(
                        padding: EdgeInsets.all(24),
                        child: Text(
                          'HEIC 原图手机端暂时解不开，\n请在桌面端转成 JPEG。',
                          textAlign: TextAlign.center,
                          style: TextStyle(color: DhColors.tx2, height: 1.8),
                        ),
                      )
                    : Image.network(
                        widget.api.rawUrl(_cur.path),
                        headers: widget.api.headers,
                        fit: BoxFit.contain,
                        loadingBuilder: (ctx, child, progress) {
                          if (progress == null) return child;
                          return const Center(
                            child: SizedBox(
                              width: 22,
                              height: 22,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            ),
                          );
                        },
                        errorBuilder: (_, __, ___) => const Text('这张图打不开',
                            style: TextStyle(color: DhColors.mut)),
                      ),
              ),
            ),
          ),
          if (list.length > 1)
            SafeArea(
              top: false,
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 6),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.spaceAround,
                  children: [
                    IconButton(
                      onPressed: idx > 0
                          ? () => setState(() => _cur = list[idx - 1])
                          : null,
                      icon: const Icon(Icons.chevron_left),
                    ),
                    Text('${idx + 1} / ${list.length}',
                        style: const TextStyle(
                            fontSize: 12, color: DhColors.mut)),
                    IconButton(
                      onPressed: idx >= 0 && idx < list.length - 1
                          ? () => setState(() => _cur = list[idx + 1])
                          : null,
                      icon: const Icon(Icons.chevron_right),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
