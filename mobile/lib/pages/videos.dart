import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';

import '../api.dart';
import '../theme.dart';

/// 影视列表：_appfiles/video 下的片子，按加入时间倒序。
/// 不能直放的格式（AVI/WMV/RMVB…）灰掉并给出「去桌面端转码」的说明，
/// 而不是点进去黑屏。
class VideosPage extends StatefulWidget {
  const VideosPage({super.key, required this.api});

  final ApiClient api;

  @override
  State<VideosPage> createState() => _VideosPageState();
}

class _VideosPageState extends State<VideosPage> {
  final ScrollController _sc = ScrollController();

  final List<VideoItem> _items = [];
  String _next = '';
  bool _loading = false;
  bool _missing = false;
  String _hint = '';
  String? _err;

  @override
  void initState() {
    super.initState();
    _load(reset: true);
    _sc.addListener(() {
      if (!_sc.hasClients) return;
      if (_sc.position.extentAfter < 400) _load();
    });
  }

  @override
  void dispose() {
    _sc.dispose();
    super.dispose();
  }

  Future<void> _load({bool reset = false}) async {
    if (_loading) return;
    if (!reset && _next.isEmpty) return;
    setState(() {
      _loading = true;
      _err = null;
      if (reset) {
        _items.clear();
        _next = '';
      }
    });
    try {
      final page = await widget.api.videos(cursor: reset ? null : _next);
      if (!mounted) return;
      setState(() {
        _items.addAll(page.items);
        _next = page.next;
        _missing = page.missing;
        _hint = page.hint;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _err = e.friendly);
    } catch (e) {
      if (mounted) setState(() => _err = '加载失败：$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _open(VideoItem item) async {
    if (!item.playable) {
      await showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: Text(item.name, style: const TextStyle(fontSize: 15)),
          content: Text(item.hint.isEmpty ? '手机端放不了这个格式' : item.hint),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx), child: const Text('知道了')),
          ],
        ),
      );
      return;
    }
    if (!mounted) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerPage(
          api: widget.api,
          path: item.path,
          title: item.name,
        ),
      ),
    );
  }

  Widget _emptyState() {
    if (_err != null) {
      return Padding(
        padding: const EdgeInsets.fromLTRB(24, 60, 24, 20),
        child: Text(_err!,
            textAlign: TextAlign.center,
            style: const TextStyle(color: DhColors.danger, height: 1.7)),
      );
    }
    return Padding(
      padding: const EdgeInsets.fromLTRB(24, 60, 24, 20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.movie_creation_outlined, size: 34, color: DhColors.mut),
          const SizedBox(height: 12),
          Text(
            _missing
                ? '共享里还没有 _appfiles/video 目录。把视频放进去后下拉刷新。'
                : (_hint.isEmpty ? '还没有片子。把视频放进共享的 _appfiles/video。' : _hint),
            textAlign: TextAlign.center,
            style: const TextStyle(
                color: DhColors.tx2, fontSize: 13, height: 1.7),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final empty = _items.isEmpty;
    return Scaffold(
      appBar: AppBar(
        title: const Text('影视'),
        actions: [
          if (_items.isNotEmpty)
            Center(
              child: Padding(
                padding: const EdgeInsets.only(right: 14),
                child: Text('${_items.length} 部',
                    style: const TextStyle(fontSize: 12, color: DhColors.mut)),
              ),
            ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => _load(reset: true),
        child: ListView.separated(
          controller: _sc,
          padding: const EdgeInsets.only(bottom: 28),
          itemCount: empty ? 1 : _items.length + (_loading ? 1 : 0),
          separatorBuilder: (_, __) =>
              const Divider(height: 1, color: DhColors.line),
          itemBuilder: (ctx, i) {
            if (empty) return _emptyState();
            if (i >= _items.length) {
              return const Padding(
                padding: EdgeInsets.symmetric(vertical: 18),
                child: Center(
                  child: SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2)),
                ),
              );
            }
            final item = _items[i];
            return ListTile(
              onTap: () => _open(item),
              leading: Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: DhColors.card,
                  borderRadius: BorderRadius.circular(10),
                  border: Border.all(color: DhColors.line),
                ),
                child: Icon(
                  item.playable ? Icons.play_arrow_rounded : Icons.block,
                  color:
                      item.playable ? const Color(0xFFFFB020) : DhColors.mut,
                ),
              ),
              title: Text(item.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 14)),
              subtitle: Text(
                '${item.ext.toUpperCase()} · ${humanSize(item.size)}'
                '${item.playable ? '' : ' · 需转码'}',
                style: const TextStyle(fontSize: 11.5, color: DhColors.mut),
              ),
            );
          },
        ),
      ),
    );
  }
}

/// 播放器：直连共享路径（服务端支持 Range，所以能拖进度）。
class PlayerPage extends StatefulWidget {
  const PlayerPage({
    super.key,
    required this.api,
    required this.path,
    required this.title,
  });

  final ApiClient api;
  final String path;
  final String title;

  @override
  State<PlayerPage> createState() => _PlayerPageState();
}

class _PlayerPageState extends State<PlayerPage> {
  VideoPlayerController? _ctrl;
  String? _err;
  bool _ready = false;

  @override
  void initState() {
    super.initState();
    _boot();
  }

  Future<void> _boot() async {
    try {
      // 先问服务端这份文件能不能直放（顺带拿到准确的 mime）。
      final play = await widget.api.videoPlay(widget.path);
      if (!play.playable) {
        if (mounted) setState(() => _err = play.hint);
        return;
      }
      final url = Uri.parse('${widget.api.base}${play.url}');
      final ctrl = VideoPlayerController.networkUrl(
        url,
        httpHeaders: widget.api.headers,
      );
      _ctrl = ctrl;
      await ctrl.initialize();
      await ctrl.play();
      if (!mounted) return;
      setState(() => _ready = true);
    } catch (e) {
      if (mounted) setState(() => _err = '播放失败：$e');
    }
  }

  @override
  void dispose() {
    _ctrl?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final ctrl = _ctrl;
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        title: Text(widget.title,
            style: const TextStyle(fontSize: 14),
            overflow: TextOverflow.ellipsis),
      ),
      body: Center(
        child: _err != null
            ? Padding(
                padding: const EdgeInsets.all(24),
                child: Text(_err!,
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: DhColors.tx2, height: 1.8)),
              )
            : (!_ready || ctrl == null)
                ? const SizedBox(
                    width: 24,
                    height: 24,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      AspectRatio(
                        aspectRatio: ctrl.value.aspectRatio == 0
                            ? 16 / 9
                            : ctrl.value.aspectRatio,
                        child: GestureDetector(
                          onTap: () {
                            if (ctrl.value.isPlaying) {
                              ctrl.pause();
                            } else {
                              ctrl.play();
                            }
                            setState(() {});
                          },
                          child: Stack(
                            alignment: Alignment.center,
                            children: [
                              VideoPlayer(ctrl),
                              if (!ctrl.value.isPlaying)
                                const Icon(Icons.play_circle_fill,
                                    size: 56, color: Colors.white70),
                            ],
                          ),
                        ),
                      ),
                      VideoProgressIndicator(ctrl,
                          allowScrubbing: true,
                          colors: const VideoProgressColors(
                            playedColor: DhColors.acc,
                            bufferedColor: Color(0x55FFFFFF),
                            backgroundColor: Color(0x22FFFFFF),
                          )),
                    ],
                  ),
      ),
    );
  }
}
