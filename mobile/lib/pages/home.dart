import 'package:flutter/material.dart';

import '../api.dart';
import '../settings.dart';
import '../theme.dart';
import '../widgets.dart';
import 'files.dart';
import 'photos.dart';
import 'videos.dart';

/// 首页：先给「能干什么」，再给「最近的照片」。
/// 不做花哨的启动动画 —— 打开就要看到自家的东西。
class HomePage extends StatefulWidget {
  const HomePage({super.key, required this.settings, required this.onSignOut});

  final AppSettings settings;
  final Future<void> Function() onSignOut;

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  late final ApiClient _api = widget.settings.client();

  Me? _me;
  String? _err;
  List<PhotoItem> _recent = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _api.close();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _err = null;
    });
    try {
      final me = await _api.me();
      final photos = await _api.photos(limit: 12, w: 240);
      final flat = <PhotoItem>[
        for (final m in photos.months) ...m.items,
      ];
      if (!mounted) return;
      setState(() {
        _me = me;
        _recent = flat.take(12).toList();
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _err = e.friendly);
    } catch (e) {
      if (mounted) setState(() => _err = '连不上：$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  void _soon(String label) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('$label 的手机端在后续版本开放')),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Container(
              width: 26,
              height: 26,
              decoration: BoxDecoration(
                color: DhColors.acc,
                borderRadius: BorderRadius.circular(8),
              ),
              alignment: Alignment.center,
              child: const Text('✓',
                  style: TextStyle(
                      color: Colors.white,
                      fontSize: 15,
                      fontWeight: FontWeight.w700)),
            ),
            const SizedBox(width: 9),
            const Text('对号'),
            const SizedBox(width: 8),
            if (_me != null)
              Text(
                _me!.writable ? '· 可读写' : '· 只读',
                style: const TextStyle(fontSize: 12, color: DhColors.mut),
              ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: '刷新',
            onPressed: _loading ? null : _load,
            icon: const Icon(Icons.refresh, size: 20),
          ),
          PopupMenuButton<String>(
            onSelected: (v) async {
              if (v == 'signout') await widget.onSignOut();
            },
            itemBuilder: (_) => const [
              PopupMenuItem(value: 'signout', child: Text('换一台设备 / 退出')),
            ],
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 28),
          children: [
            if (_err != null) _ErrorCard(message: _err!, onRetry: _load),
            if (_me != null) _StatusCard(me: _me!, base: widget.settings.base),
            const SizedBox(height: 16),
            _TileGrid(onSoon: _soon, api: _api, me: _me),
            const SizedBox(height: 22),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                const Text('最近照片',
                    style: TextStyle(
                        fontSize: 15, fontWeight: FontWeight.w600)),
                TextButton(
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(builder: (_) => PhotosPage(api: _api)),
                  ),
                  child: const Text('全部'),
                ),
              ],
            ),
            const SizedBox(height: 6),
            if (_loading && _recent.isEmpty)
              const SizedBox(
                height: 96,
                child: Center(
                  child: SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
              )
            else if (_recent.isEmpty)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 22),
                child: Text('还没有照片。把图放进共享目录，这里就会自动出现。',
                    style: TextStyle(color: DhColors.mut, fontSize: 13)),
              )
            else
              SizedBox(
                height: 96,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  itemCount: _recent.length,
                  separatorBuilder: (_, __) => const SizedBox(width: 8),
                  itemBuilder: (_, i) {
                    final item = _recent[i];
                    return GestureDetector(
                      onTap: () => Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) =>
                              PhotoViewPage(item: item, api: _api),
                        ),
                      ),
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(10),
                        child: SizedBox(
                          width: 96,
                          height: 96,
                          child: Image.network(
                            _api.thumbUrl(item.path, w: 240),
                            headers: _api.headers,
                            fit: BoxFit.cover,
                            errorBuilder: (_, __, ___) =>
                                const ThumbFallback(),
                          ),
                        ),
                      ),
                    );
                  },
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _TileGrid extends StatelessWidget {
  const _TileGrid({required this.onSoon, required this.api, required this.me});

  final void Function(String) onSoon;
  final ApiClient api;
  final Me? me;

  @override
  Widget build(BuildContext context) {
    final tiles = <_Tile>[
      _Tile('照片', '按时间看', Icons.photo_library_outlined, DhColors.acc, () {
        Navigator.of(context).push(
          MaterialPageRoute(builder: (_) => PhotosPage(api: api)),
        );
      }),
      _Tile('影视', '点开就播', Icons.movie_outlined, const Color(0xFFFFB020),
          () {
        Navigator.of(context).push(
          MaterialPageRoute(builder: (_) => VideosPage(api: api)),
        );
      }),
      _Tile('文件', '整块硬盘', Icons.folder_open_outlined,
          const Color(0xFF60A5FA), () {
        Navigator.of(context).push(
          MaterialPageRoute(builder: (_) => FilesPage(api: api)),
        );
      }),
      _Tile('笔记', '随手记', Icons.edit_note_outlined, const Color(0xFF4ADE80),
          () => onSoon('笔记')),
      _Tile('账本', '收支有数', Icons.account_balance_wallet_outlined,
          const Color(0xFFA78BFA), () => onSoon('账本')),
      _Tile('监控', '录像在自家', Icons.videocam_outlined, const Color(0xFFF87171),
          () => onSoon('监控')),
    ];
    return GridView.count(
      crossAxisCount: 3,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      mainAxisSpacing: 10,
      crossAxisSpacing: 10,
      childAspectRatio: .92,
      children: [
        for (final t in tiles)
          InkWell(
            borderRadius: BorderRadius.circular(14),
            onTap: t.onTap,
            child: Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: DhColors.card,
                borderRadius: BorderRadius.circular(14),
                border: Border.all(color: DhColors.line),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: 34,
                    height: 34,
                    decoration: BoxDecoration(
                      color: t.color.withValues(alpha: .14),
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: Icon(t.icon, size: 19, color: t.color),
                  ),
                  const Spacer(),
                  Text(t.label,
                      style: const TextStyle(
                          fontSize: 14, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(t.sub,
                      style: const TextStyle(
                          fontSize: 11, color: DhColors.mut)),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

class _Tile {
  _Tile(this.label, this.sub, this.icon, this.color, this.onTap);

  final String label;
  final String sub;
  final IconData icon;
  final Color color;
  final VoidCallback onTap;
}

class _StatusCard extends StatelessWidget {
  const _StatusCard({required this.me, required this.base});

  final Me me;
  final String base;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: DhColors.card,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: DhColors.line),
      ),
      child: Row(
        children: [
          const Icon(Icons.check_circle_outline, size: 16, color: DhColors.ok),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              '${me.user} @ $base',
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12.5, color: DhColors.tx2),
            ),
          ),
          Text(
            me.version.isEmpty ? '' : 'v${me.version}',
            style: const TextStyle(fontSize: 11.5, color: DhColors.mut),
          ),
        ],
      ),
    );
  }
}

class _ErrorCard extends StatelessWidget {
  const _ErrorCard({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: DhColors.danger.withValues(alpha: .08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: DhColors.danger.withValues(alpha: .35)),
      ),
      child: Row(
        children: [
          const Icon(Icons.cloud_off, size: 18, color: DhColors.danger),
          const SizedBox(width: 8),
          Expanded(
            child: Text(message,
                style: const TextStyle(color: DhColors.danger, fontSize: 12.5)),
          ),
          TextButton(onPressed: onRetry, child: const Text('重试')),
        ],
      ),
    );
  }
}
