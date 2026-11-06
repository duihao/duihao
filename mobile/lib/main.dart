import 'package:flutter/material.dart';

import 'pages/home.dart';
import 'pages/setup.dart';
import 'settings.dart';
import 'theme.dart';

void main() => runApp(const DuihaoApp());

/// 根：先读本地配置，再决定进「配对页」还是「首页」。
/// 不在启动时强依赖网络 —— 家里断网也能打开 App，错误留给页面自己提示。
class DuihaoApp extends StatefulWidget {
  const DuihaoApp({super.key});

  @override
  State<DuihaoApp> createState() => _DuihaoAppState();
}

class _DuihaoAppState extends State<DuihaoApp> {
  AppSettings? _settings;

  @override
  void initState() {
    super.initState();
    AppSettings.load().then((s) {
      if (mounted) setState(() => _settings = s);
    });
  }

  Future<void> _signOut() async {
    await _settings?.clear();
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final s = _settings;
    return MaterialApp(
      title: '对号',
      debugShowCheckedModeBanner: false,
      theme: duihaoTheme(),
      home: s == null
          ? const _Splash()
          : s.configured
              ? HomePage(settings: s, onSignOut: _signOut)
              : SetupPage(
                  settings: s,
                  onSaved: () => setState(() {}),
                ),
    );
  }
}

class _Splash extends StatelessWidget {
  const _Splash();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      body: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            _Logo(),
            SizedBox(height: 18),
            Text('对号', style: TextStyle(fontSize: 20, fontWeight: FontWeight.w600)),
            SizedBox(height: 6),
            Text('隐私数据，只在自家',
                style: TextStyle(fontSize: 13, color: DhColors.mut)),
            SizedBox(height: 26),
            SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ],
        ),
      ),
    );
  }
}

class _Logo extends StatelessWidget {
  const _Logo();

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 56,
      height: 56,
      decoration: BoxDecoration(
        color: DhColors.acc,
        borderRadius: BorderRadius.circular(16),
      ),
      alignment: Alignment.center,
      child: const Text('✓',
          style: TextStyle(
              fontSize: 30, color: Colors.white, fontWeight: FontWeight.w700)),
    );
  }
}
