import 'package:flutter/material.dart';

import '../api.dart';
import '../settings.dart';
import '../theme.dart';

/// 接入页：填地址 + NAS 账号。
///
/// P1-b 会在这里加「扫桌面二维码」。现在先给「填地址 + 测试连接」，
/// 因为这条路任何情况下都能用（二维码只是个更省事的入口）。
class SetupPage extends StatefulWidget {
  const SetupPage({super.key, required this.settings, required this.onSaved});

  final AppSettings settings;
  final VoidCallback onSaved;

  @override
  State<SetupPage> createState() => _SetupPageState();
}

class _SetupPageState extends State<SetupPage> {
  late final TextEditingController _base =
      TextEditingController(text: widget.settings.base);
  late final TextEditingController _user =
      TextEditingController(text: widget.settings.user);
  late final TextEditingController _pass =
      TextEditingController(text: widget.settings.pass);

  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _base.dispose();
    _user.dispose();
    _pass.dispose();
    super.dispose();
  }

  Future<void> _connect() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    final trial = ApiClient(
      base: _base.text,
      user: _user.text.trim(),
      pass: _pass.text,
    );
    try {
      await trial.me(); // 探活：地址/凭据/权限都对才落盘
      await widget.settings.save();
      // 探活通过就直接进首页：省一次点击。
      widget.onSaved();
    } on ApiException catch (e) {
      setState(() => _error = e.friendly);
    } catch (e) {
      setState(() => _error = '连不上：$e');
    } finally {
      trial.close();
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('连接到自家 NAS')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 32),
        children: [
          const Text(
            '对号跑在你自己的电脑上。手机上填这台电脑的地址，同一个 Wi-Fi 下就能连；'
            '在外面连需要先在桌面端打开「公网访问」。',
            style: TextStyle(color: DhColors.tx2, height: 1.7, fontSize: 13.5),
          ),
          const SizedBox(height: 22),
          const _Label('NAS 地址'),
          TextField(
            controller: _base,
            keyboardType: TextInputType.url,
            autocorrect: false,
            decoration: const InputDecoration(
              hintText: '192.168.1.10:8080',
              prefixIcon: Icon(Icons.lan_outlined, size: 20),
            ),
          ),
          const SizedBox(height: 8),
          const Text('在桌面端「连接管理」里能看到端口，形如 192.168.1.10:8080',
              style: TextStyle(color: DhColors.mut, fontSize: 12)),
          const SizedBox(height: 18),
          const _Label('账号'),
          TextField(
            controller: _user,
            autocorrect: false,
            decoration: const InputDecoration(
              hintText: 'NAS 账号（不是会员账号）',
              prefixIcon: Icon(Icons.person_outline, size: 20),
            ),
          ),
          const SizedBox(height: 18),
          const _Label('密码'),
          TextField(
            controller: _pass,
            obscureText: true,
            decoration: const InputDecoration(
              hintText: '登录密码',
              prefixIcon: Icon(Icons.lock_outline, size: 20),
            ),
            onSubmitted: (_) => _connect(),
          ),
          if (_error != null) ...[
            const SizedBox(height: 16),
            _Banner(error: _error!),
          ],
          const SizedBox(height: 26),
          FilledButton(
            onPressed: _busy ? null : _connect,
            child: _busy
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: Colors.white),
                  )
                : const Text('测试并进入'),
          ),
          const SizedBox(height: 14),
          const Text(
            '凭据只存在手机本地（SharedPreferences），不经过任何第三方服务器。',
            textAlign: TextAlign.center,
            style: TextStyle(color: DhColors.mut, fontSize: 11.5),
          ),
        ],
      ),
    );
  }
}

class _Label extends StatelessWidget {
  const _Label(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(text,
            style: const TextStyle(
                fontSize: 13, fontWeight: FontWeight.w600, color: DhColors.tx2)),
      );
}

class _Banner extends StatelessWidget {
  const _Banner({required this.error});

  final String error;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: DhColors.danger.withValues(alpha: .1),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: DhColors.danger.withValues(alpha: .4)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.error_outline, size: 18, color: DhColors.danger),
          const SizedBox(width: 8),
          Expanded(
            child: Text(error,
                style: const TextStyle(color: DhColors.danger, fontSize: 12.5)),
          ),
        ],
      ),
    );
  }
}
